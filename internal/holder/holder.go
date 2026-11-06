package holder

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Holder is the programs a holder keeps, and the core it serves.
type Holder struct {
	mu    sync.Mutex
	progs map[string]*prog
	order []string // ids by when they were spawned
	// core is what sends to the core served now, nil between one and the
	// next; gone is closed when it goes.
	core *sender
	gone chan struct{}
	// reads wait for output, for the core served now.
	reads map[uint64]*Msg
	// shut is closed once a shutdown op has the holder stop.
	shut     chan struct{}
	shutOnce sync.Once
	// paused stops what moves bytes, for an upgrade; pausing is closed
	// then, for those waiting to see.
	paused  bool
	pausing chan struct{}
	// upgrade is what an upgrade op runs, nil to refuse it.
	upgrade func(seq uint64) error
	// execArgv is how an upgrade starts the holder again.
	execArgv func() ([]string, error)
	// coreArgv is the core the holder starts, corePid the one running.
	coreArgv []string
	corePid  int
}

// prog is one program and what is kept of it.
type prog struct {
	id      string
	spawn   Spawn // Sandboxed, Secret and Label as they were given
	pid     int
	started int64

	out *os.File // its terminal, or the pipe it prints to
	in  *os.File // the pipe to its standard input, without a terminal
	// stdin is what is left to write to in, once, then in is closed.
	stdin []byte

	ring   *ring
	outEOF bool
	// changed is closed and replaced when output comes, output ends or
	// the program goes.
	changed chan struct{}

	exited bool
	exit   *Exit
	reaped bool
	// exitAcked: the core has the exit; done: it wants nothing more.
	exitAcked, done bool
	acked           int64

	// groups are the process groups of p's session the holder has seen
	// while p was not yet reaped, so in its session for sure; pgGone is
	// that p's own group was seen empty after p was reaped. A group's id
	// is not reused while the group has a process in it, so the holder
	// signals them until it sees them gone, and then never again.
	groups map[int]bool
	pgGone bool

	ckPos  int64
	ckData []byte

	// writes wait for the writer, in order.
	writes  []*write
	wake    chan struct{} // the writer has work, or should stop
	readerQ chan struct{} // closed when the reader stops
	writerQ chan struct{} // closed when the writer stops
}

// write is bytes for a program's terminal, and the request to answer
// once they are written.
type write struct {
	seq  uint64
	data []byte
	n    int
	to   *sender
}

// New is a holder with no programs.
func New() *Holder {
	return &Holder{
		progs: map[string]*prog{}, reads: map[uint64]*Msg{},
		shut: make(chan struct{}), pausing: make(chan struct{}),
		execArgv: defaultExecArgv,
	}
}

// ErrShutdown is what Serve returns after a shutdown op.
var ErrShutdown = errors.New("holder: shut down")

// Serve serves the core at the other end of c until it goes, and closes
// c. Only one core is served at a time: a new one waits for the last.
func (h *Holder) Serve(c *conn) error {
	defer c.Close()
	s := newSender(c)
	defer s.stop()
	s.post(&Msg{Op: OpHello, Proto: Proto, Pid: os.Getpid()}, nil)
	m, _, files, err := c.recv()
	closeAll(files)
	if err != nil {
		return err
	}
	if m.Op != OpHello || m.Proto != Proto {
		return fmt.Errorf("holder: core speaks protocol %d, not %d", m.Proto, Proto)
	}
	gone, err := h.attach(s)
	if err != nil {
		return err
	}
	defer h.detach(s, gone)
	h.mu.Lock()
	// The exits the last core did not take.
	for _, id := range h.order {
		if p := h.progs[id]; p.exited && !p.exitAcked {
			s.post(&Msg{Op: OpExit, ID: id, Exit: p.exit}, nil)
		}
	}
	h.reads = map[uint64]*Msg{}
	h.mu.Unlock()
	return h.serve(s, gone)
}

// attach makes s the core served, once the last has gone.
func (h *Holder) attach(s *sender) (chan struct{}, error) {
	gone := make(chan struct{})
	for {
		h.mu.Lock()
		if h.core == nil {
			h.core, h.gone = s, gone
			h.mu.Unlock()
			return gone, nil
		}
		last := h.gone
		h.mu.Unlock()
		select {
		case <-last:
		case <-h.shut:
			return nil, ErrShutdown
		}
	}
}

func (h *Holder) detach(s *sender, gone chan struct{}) {
	h.mu.Lock()
	if h.core == s {
		h.core = nil
		close(gone)
	}
	h.mu.Unlock()
}

// serve handles what the core asks, one request at a time as they come,
// until it goes.
func (h *Holder) serve(s *sender, gone chan struct{}) error {
	reqs := make(chan error, 1)
	go func() {
		for {
			m, body, files, err := s.c.recv()
			if err != nil {
				reqs <- err
				return
			}
			if err := h.handle(s, gone, m, body, files); err != nil {
				reqs <- err
				return
			}
		}
	}()
	select {
	case err := <-reqs:
		return err
	case <-h.shut:
		return ErrShutdown
	}
}

func answer(s *sender, req *Msg, m *Msg, body []byte, err error) {
	if m == nil {
		m = &Msg{}
	}
	m.Op, m.Seq = req.Op, req.Seq
	if err != nil {
		errMsg(m, err)
	}
	s.post(m, body)
}

func (h *Holder) handle(s *sender, gone chan struct{}, m *Msg, body []byte, files []*os.File) error {
	switch m.Op {
	case OpSpawn:
		pid, err := h.spawn(m, body, files)
		closeAll(files)
		answer(s, m, &Msg{ID: m.ID, Pid: pid}, nil, err)
		return nil
	}
	closeAll(files)
	switch m.Op {
	case OpWrite:
		h.queueWrite(s, m, body)
	case OpResize:
		answer(s, m, nil, nil, h.resize(m.ID, m.W, m.H))
	case OpSignal:
		answer(s, m, nil, nil, h.signal(m.ID, m.PG, syscall.Signal(m.Sig)))
	case OpRead:
		h.mu.Lock()
		h.reads[m.Seq] = m
		h.mu.Unlock()
		go h.read(s, gone, m)
	case OpAck:
		answer(s, m, nil, nil, h.ack(m))
	case OpCheckpoint:
		answer(s, m, nil, nil, h.checkpoint(m.ID, m.Pos, body))
	case OpFetch:
		h.mu.Lock()
		p, err := h.get(m.ID)
		var pos int64
		var data []byte
		if err == nil {
			pos, data = p.ckPos, p.ckData
		}
		h.mu.Unlock()
		answer(s, m, &Msg{ID: m.ID, Pos: pos}, data, err)
	case OpList:
		progs, err := h.list(m.ID)
		answer(s, m, &Msg{Progs: progs}, nil, err)
	case OpShutdown:
		h.shutdown(5 * time.Second)
		answer(s, m, nil, nil, nil)
		s.flush()
		h.stop()
		return ErrShutdown
	case OpUpgrade:
		h.mu.Lock()
		up := h.upgrade
		h.mu.Unlock()
		if up == nil {
			answer(s, m, nil, nil, fmt.Errorf("holder: cannot upgrade here"))
			return nil
		}
		// On success it does not return: the holder runs anew, and its
		// reply comes from there.
		answer(s, m, nil, nil, up(m.Seq))
	default:
		answer(s, m, nil, nil, fmt.Errorf("holder: no op %q", m.Op))
	}
	return nil
}

func (h *Holder) stop() { h.shutOnce.Do(func() { close(h.shut) }) }

func (h *Holder) get(id string) (*prog, error) {
	p := h.progs[id]
	if p == nil {
		return nil, fmt.Errorf("holder: no program %q", id)
	}
	return p, nil
}

func (h *Holder) spawn(m *Msg, stdin []byte, files []*os.File) (int, error) {
	s := m.Spawn
	switch {
	case s == nil || m.ID == "":
		return 0, fmt.Errorf("holder: spawn needs an id and a program")
	case len(files) != m.Files:
		return 0, fmt.Errorf("holder: %d descriptors came, %d were sent", len(files), m.Files)
	case s.Ring < 0 || s.Ring > MaxRing:
		return 0, fmt.Errorf("holder: ring of %d bytes", s.Ring)
	}
	h.mu.Lock()
	_, dup := h.progs[m.ID]
	h.mu.Unlock()
	if dup {
		return 0, fmt.Errorf("holder: program %q is there already", m.ID)
	}
	if s.PTY || len(stdin) == 0 {
		stdin = nil
	}
	st, err := start(s, stdin, files)
	if err != nil {
		return 0, err
	}
	p := &prog{
		id: m.ID, spawn: *s, pid: st.pid, started: time.Now().UnixNano(),
		out: st.out, in: st.in, stdin: stdin,
		ring: newRing(s.Ring), changed: make(chan struct{}), ckPos: -1,
	}
	h.mu.Lock()
	h.progs[p.id] = p
	h.order = append(h.order, p.id)
	h.run(p)
	h.mu.Unlock()
	go h.watch(p)
	return p.pid, nil
}

// run starts what moves p's bytes. h.mu is held.
func (h *Holder) run(p *prog) {
	p.wake = make(chan struct{}, 1)
	p.readerQ, p.writerQ = make(chan struct{}), make(chan struct{})
	if p.done || p.outEOF {
		close(p.readerQ)
	} else {
		go h.reader(p)
	}
	go h.writer(p)
}

// signalChanged tells those waiting on p that something has. h.mu is
// held.
func (p *prog) signalChanged() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// reader reads p's output into its ring, always, whoever is there to
// take it.
func (h *Holder) reader(p *prog) {
	defer close(p.readerQ)
	buf := make([]byte, 32<<10)
	for {
		n, err := p.out.Read(buf)
		h.mu.Lock()
		if n > 0 {
			p.ring.write(buf[:n])
			p.signalChanged()
		}
		if err != nil && !(h.paused && errors.Is(err, os.ErrDeadlineExceeded)) {
			p.outEOF = true
			p.signalChanged()
		}
		h.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// writer writes what is queued for p, in order, and answers each write
// once it is written; or first its standard input, then closes that.
func (h *Holder) writer(p *prog) {
	defer close(p.writerQ)
	for {
		h.mu.Lock()
		if h.paused || p.done && p.in == nil {
			h.mu.Unlock()
			return
		}
		var w *write
		var dst *os.File
		if p.in != nil {
			dst, w = p.in, &write{data: p.stdin}
		} else if len(p.writes) > 0 {
			dst, w = p.out, p.writes[0]
		}
		h.mu.Unlock()
		if w == nil {
			<-p.wake
			continue
		}
		n, err := dst.Write(w.data[w.n:])
		h.mu.Lock()
		w.n += n
		if h.paused && errors.Is(err, os.ErrDeadlineExceeded) {
			if dst == p.in {
				p.stdin = w.data[w.n:]
			}
			h.mu.Unlock()
			return
		}
		if dst == p.in {
			// Written, or it will not take it; either way it is done.
			p.in.Close()
			p.in, p.stdin = nil, nil
			h.mu.Unlock()
			continue
		}
		p.writes = p.writes[1:]
		if w.to != nil {
			r := &Msg{Op: OpWrite, Seq: w.seq, Pos: int64(w.n)}
			if err != nil {
				errMsg(r, err)
			}
			w.to.post(r, nil)
		}
		h.mu.Unlock()
	}
}

func (p *prog) poke() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// queueWrite queues body for p's terminal; the writer answers. A write
// does not wait for another program, or for anything else of this one.
func (h *Holder) queueWrite(s *sender, m *Msg, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, err := h.get(m.ID)
	switch {
	case err != nil:
	case !p.spawn.PTY:
		err = os.ErrInvalid
	case p.done:
		err = os.ErrClosed
	}
	if err != nil {
		answer(s, m, nil, nil, err)
		return
	}
	p.writes = append(p.writes, &write{seq: m.Seq, data: body, to: s})
	p.poke()
}

func (h *Holder) resize(id string, w, ht int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, err := h.get(id)
	if err != nil || !p.spawn.PTY || p.done {
		return err
	}
	return setSize(p.out, w, ht)
}

// signal sends sig to process group pg of p: its own, or for a program
// on a terminal one in the session it leads. Its own group is reached
// after p is reaped too, for what is left in it, until it is seen empty;
// another group, only one seen in p's session before p was reaped. So a
// group id that could be another's once its group is gone is not used.
func (h *Holder) signal(id string, pg int, sig syscall.Signal) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, err := h.get(id)
	if err != nil {
		return err
	}
	if pg <= 0 {
		return syscall.ESRCH
	}
	if pg == p.pid {
		if p.pgGone {
			return syscall.ESRCH
		}
	} else {
		if !p.spawn.PTY {
			return syscall.EPERM
		}
		if !p.reaped && !p.groups[pg] {
			sid, err := unix.Getsid(pg)
			if err != nil {
				return syscall.ESRCH
			}
			if sid != p.pid {
				return syscall.EPERM
			}
			p.see(pg)
		}
		if !p.groups[pg] {
			return syscall.ESRCH
		}
	}
	err = syscall.Kill(-pg, sig)
	if err == syscall.ESRCH {
		p.gone(pg)
		h.forgetIfDone(p)
	}
	return err
}

// see notes that pg is a group in p's session. h.mu is held.
func (p *prog) see(pg int) {
	if p.groups == nil {
		p.groups = map[int]bool{}
	}
	p.groups[pg] = true
}

// gone notes that pg was seen empty. h.mu is held.
func (p *prog) gone(pg int) {
	if pg == p.pid {
		p.pgGone = p.reaped
	} else {
		delete(p.groups, pg)
	}
}

// groupsLeft says whether any of p's groups may still have a process,
// looking again at those not yet seen gone. h.mu is held.
func (p *prog) groupsLeft() bool {
	if p.reaped && !p.pgGone && syscall.Kill(-p.pid, 0) == syscall.ESRCH {
		p.pgGone = true
	}
	for pg := range p.groups {
		if syscall.Kill(-pg, 0) == syscall.ESRCH {
			delete(p.groups, pg)
		}
	}
	return !p.pgGone || len(p.groups) > 0
}

// forgetIfDone reaps p once it has exited and the core has let go of it,
// and forgets it once its groups are gone too. h.mu is held.
func (h *Holder) forgetIfDone(p *prog) {
	if !p.done || !p.exited {
		return
	}
	if !p.reaped {
		reap(p.pid)
		p.reaped = true
	}
	if !p.groupsLeft() && h.progs[p.id] == p {
		h.forget(p.id)
	}
}

// read answers m once there is output from m.Pos on, the output ends or
// the program goes. It gives up when the core goes, and leaves m in
// h.reads when an upgrade pauses the holder, to be read from there.
func (h *Holder) read(s *sender, gone chan struct{}, m *Msg) {
	want := min(max(m.Max, 1), MaxBody)
	for {
		h.mu.Lock()
		if h.paused || h.reads[m.Seq] != m {
			h.mu.Unlock()
			return
		}
		p, err := h.get(m.ID)
		if err == nil && p.done {
			err = os.ErrClosed
		}
		if err != nil {
			delete(h.reads, m.Seq)
			answer(s, m, nil, nil, err)
			h.mu.Unlock()
			return
		}
		if p.ring.end != m.Pos || p.outEOF {
			buf := make([]byte, want)
			n, from := p.ring.read(m.Pos, buf)
			r := &Msg{ID: m.ID, Pos: from, Lost: max(from-m.Pos, 0), EOF: p.outEOF && from+int64(n) >= p.ring.end}
			delete(h.reads, m.Seq)
			answer(s, m, r, buf[:n], nil)
			h.mu.Unlock()
			return
		}
		ch, pausing := p.changed, h.pausing
		h.mu.Unlock()
		select {
		case <-ch:
		case <-pausing:
		case <-gone:
			return
		}
	}
}

// watch waits for p to exit, and tells the core.
func (h *Holder) watch(p *prog) {
	e, reaped, err := waitExit(p.pid)
	if err != nil {
		// It cannot be waited for, so it is not this holder's child;
		// say it ended rather than leave the core waiting.
		e, reaped = &Exit{Code: -1}, true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p.exited, p.exit = true, e
	p.reaped = p.reaped || reaped
	p.signalChanged()
	if h.core != nil {
		h.core.post(&Msg{Op: OpExit, ID: p.id, Exit: e}, nil)
	}
	// Let go of already, it is reaped now: no core waits for its exit.
	h.forgetIfDone(p)
}

func (h *Holder) ack(m *Msg) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, err := h.get(m.ID)
	if err != nil {
		return err
	}
	if m.Pos > p.acked {
		p.acked = min(m.Pos, p.ring.end)
	}
	if m.Exit != nil {
		if !p.exited {
			return fmt.Errorf("holder: %q has not exited", m.ID)
		}
		p.exitAcked = true
		if !p.reaped {
			// A zombie is reaped at once; done under h.mu, so an
			// upgrade never sees it marked one way and left the other.
			reap(p.pid)
			p.reaped = true
		}
	}
	if m.Done && !p.done {
		p.done = true
		// Closing does not wait: the reader's Read returns, and the
		// writer's Write.
		p.out.Close()
		p.writes = nil
		p.signalChanged()
		p.poke()
	}
	h.forgetIfDone(p)
	return nil
}

// forget drops id. h.mu is held.
func (h *Holder) forget(id string) {
	delete(h.progs, id)
	for i, o := range h.order {
		if o == id {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
}

func (h *Holder) checkpoint(id string, pos int64, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, err := h.get(id)
	if err != nil {
		return err
	}
	if pos < 0 || pos > p.ring.end {
		return fmt.Errorf("holder: checkpoint at %d, output is at %d", pos, p.ring.end)
	}
	p.ckPos, p.ckData = pos, data
	return nil
}

// Checkpoint is the last checkpoint of id and where it is, -1 for none.
func (h *Holder) Checkpoint(id string) (int64, []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.progs[id]; p != nil {
		return p.ckPos, p.ckData
	}
	return -1, nil
}

func (h *Holder) list(id string) ([]Prog, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := h.order
	if id != "" {
		if _, err := h.get(id); err != nil {
			return nil, err
		}
		ids = []string{id}
	}
	progs := make([]Prog, 0, len(ids))
	for _, id := range ids {
		p := h.progs[id]
		state := Running
		if p.reaped {
			state = Reaped
		} else if p.exited {
			state = Exited
		}
		pr := Prog{
			ID: p.id, Pid: p.pid, Started: p.started,
			Sandboxed: p.spawn.Sandboxed, Secret: p.spawn.Secret, Label: p.spawn.Label,
			PTY: p.spawn.PTY, State: state, Exit: p.exit,
			Pos: p.ring.end, Start: p.ring.start(), Acked: p.acked, Checkpoint: p.ckPos,
		}
		if p.spawn.PTY && !p.done && !p.exited {
			pr.Foreground = foreground(p.out) // an ioctl; it does not wait
			if fg := pr.Foreground; fg > 0 && fg != p.pid {
				if sid, err := unix.Getsid(fg); err == nil && sid == p.pid {
					p.see(fg)
				}
			}
		}
		progs = append(progs, pr)
	}
	// Those let go of whose groups have gone since.
	for _, id := range ids {
		if p := h.progs[id]; p != nil && p.done && p.reaped {
			h.forgetIfDone(p)
		}
	}
	return progs, nil
}

// shutdown hangs up every program, its foreground too, kills what is
// left after grace, and reaps them all.
func (h *Holder) shutdown(grace time.Duration) {
	h.mu.Lock()
	var running []*prog
	for _, id := range h.order {
		p := h.progs[id]
		if p.exited {
			continue
		}
		running = append(running, p)
		_ = syscall.Kill(-p.pid, syscall.SIGHUP)
		if p.spawn.PTY && !p.done {
			if fg := foreground(p.out); fg > 0 && fg != p.pid {
				if sid, err := unix.Getsid(fg); err == nil && sid == p.pid {
					_ = syscall.Kill(-fg, syscall.SIGHUP)
				}
			}
		}
	}
	h.mu.Unlock()
	left := func() []*prog {
		h.mu.Lock()
		defer h.mu.Unlock()
		var l []*prog
		for _, p := range running {
			if !p.exited {
				l = append(l, p)
			}
		}
		return l
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && len(left()) > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	// What is left is killed, all at once; under h.mu, so none is reaped
	// meanwhile and its PID another's.
	h.mu.Lock()
	for _, p := range running {
		if !p.exited {
			_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		}
	}
	h.mu.Unlock()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(left()) > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range h.order {
		p := h.progs[id]
		if p.exited && !p.reaped {
			reap(p.pid)
			p.reaped = true
		}
		if !p.done {
			p.done = true
			p.out.Close()
			p.signalChanged()
			p.poke()
		}
		if p.in != nil {
			p.in.Close()
			p.in = nil
		}
	}
	h.progs, h.order = map[string]*prog{}, nil
}

// sender sends a core what the holder has for it, in order, from one
// goroutine: posting never waits on the socket, so it can be done under
// h.mu, and an upgrade can stop it between frames and take what is left.
type sender struct {
	c       *conn
	mu      sync.Mutex
	q       []frame
	broken  bool
	wake    chan struct{}
	halt    chan struct{}
	stopped chan struct{}
	idle    *sync.Cond
	busy    bool
}

type frame struct {
	M    *Msg   `json:"m"`
	Body []byte `json:"body,omitempty"`
}

func newSender(c *conn) *sender { return newSenderWith(c, nil) }

// newSenderWith is a sender with q to send first.
func newSenderWith(c *conn, q []frame) *sender {
	s := &sender{c: c, q: q, wake: make(chan struct{}, 1)}
	s.idle = sync.NewCond(&s.mu)
	s.start()
	return s
}

// start starts the sender, new or stopped.
func (s *sender) start() {
	s.halt, s.stopped = make(chan struct{}), make(chan struct{})
	go s.loop(s.halt, s.stopped)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sender) post(m *Msg, body []byte) {
	s.mu.Lock()
	if !s.broken {
		s.q = append(s.q, frame{m, body})
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sender) loop(halt, stopped chan struct{}) {
	defer close(stopped)
	for {
		s.mu.Lock()
		select {
		case <-halt:
			s.busy = false
			s.idle.Broadcast()
			s.mu.Unlock()
			return
		default:
		}
		if len(s.q) == 0 {
			s.busy = false
			s.idle.Broadcast()
			s.mu.Unlock()
			select {
			case <-s.wake:
				continue
			case <-halt:
				return
			}
		}
		f := s.q[0]
		s.q = s.q[1:]
		s.busy = true
		s.mu.Unlock()
		if err := s.c.send(f.M, f.Body, nil); err != nil {
			s.mu.Lock()
			s.broken, s.q = true, nil
			s.mu.Unlock()
		}
	}
}

// flush waits until all posted is sent, or cannot be.
func (s *sender) flush() {
	s.mu.Lock()
	for (len(s.q) > 0 || s.busy) && !s.broken {
		s.idle.Wait()
	}
	s.mu.Unlock()
}

// stop stops the sender between frames; what is posted after waits.
func (s *sender) stop() {
	select {
	case <-s.halt:
	default:
		close(s.halt)
	}
	<-s.stopped
}

// pending is a copy of what is left to send.
func (s *sender) pending() []frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]frame(nil), s.q...)
}
