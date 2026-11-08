package holder

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// Client is a core's end of the socket to its holder. It starts programs
// there as spawn.Local would here: a Proc from it is told as one from
// Local, so the core can use either.
type Client struct {
	c   *conn
	pid int // the holder's

	mu      sync.Mutex
	seq     uint64
	waiting map[uint64]chan reply
	procs   map[string]*Proc
	// exits came for programs no Proc has yet.
	exits map[string]*Exit
	err   error // why the socket is gone, once it is
	gone  chan struct{}
}

type reply struct {
	m    *Msg
	body []byte
}

var _ spawn.Spawner = (*Client)(nil)

// Gone is closed once the holder is gone: the socket to it has ended.
func (cl *Client) Gone() <-chan struct{} { return cl.gone }

// ErrGone is what a Client's calls return once the holder is gone.
var ErrGone = errors.New("holder: gone")

// Dial is a client of the holder at the other end of f, a socket, which
// it takes.
func Dial(f *os.File) (*Client, error) {
	c, err := fileConn(f)
	if err != nil {
		return nil, err
	}
	return newClient(c)
}

func newClient(c *conn) (*Client, error) {
	m, _, files, err := c.recv()
	closeAll(files)
	if err != nil {
		c.Close()
		return nil, err
	}
	if m.Op != OpHello || m.Proto != Proto {
		c.Close()
		return nil, fmt.Errorf("holder speaks protocol %d, not %d", m.Proto, Proto)
	}
	if err := c.send(&Msg{Op: OpHello, Proto: Proto, Pid: os.Getpid()}, nil, nil); err != nil {
		c.Close()
		return nil, err
	}
	cl := &Client{
		c: c, pid: m.Pid,
		waiting: map[uint64]chan reply{}, procs: map[string]*Proc{}, exits: map[string]*Exit{},
		gone: make(chan struct{}),
	}
	go cl.recvLoop()
	return cl, nil
}

// Pid is the holder's process.
func (cl *Client) Pid() int { return cl.pid }

// Close lets go of the socket; the holder and its programs go on.
func (cl *Client) Close() error { return cl.c.Close() }

func (cl *Client) recvLoop() {
	var err error
	for {
		var m *Msg
		var body []byte
		var files []*os.File
		m, body, files, err = cl.c.recv()
		closeAll(files)
		if err != nil {
			break
		}
		if m.Op == OpExit && m.Seq == 0 {
			cl.exited(m.ID, m.Exit)
			continue
		}
		cl.mu.Lock()
		ch := cl.waiting[m.Seq]
		delete(cl.waiting, m.Seq)
		cl.mu.Unlock()
		if ch != nil {
			ch <- reply{m, body}
		}
	}
	cl.mu.Lock()
	cl.err = fmt.Errorf("%w: %v", ErrGone, err)
	close(cl.gone)
	cl.mu.Unlock()
}

func (cl *Client) exited(id string, e *Exit) {
	cl.mu.Lock()
	p := cl.procs[id]
	if p == nil {
		cl.exits[id] = e
	}
	cl.mu.Unlock()
	if p != nil {
		p.setExit(e)
		p.forgetIfClosed()
	}
}

// call sends m and waits for its reply, or for cancel.
func (cl *Client) call(m *Msg, body []byte, files []*os.File, cancel <-chan struct{}) (*Msg, []byte, error) {
	ch := make(chan reply, 1)
	cl.mu.Lock()
	if cl.err != nil {
		err := cl.err
		cl.mu.Unlock()
		return nil, nil, err
	}
	cl.seq++
	m.Seq = cl.seq
	cl.waiting[m.Seq] = ch
	cl.mu.Unlock()
	if err := cl.c.send(m, body, files); err != nil {
		cl.mu.Lock()
		delete(cl.waiting, m.Seq)
		cl.mu.Unlock()
		return nil, nil, err
	}
	select {
	case r := <-ch:
		return r.m, r.body, msgErr(r.m)
	case <-cl.gone:
		cl.mu.Lock()
		err := cl.err
		cl.mu.Unlock()
		return nil, nil, err
	case <-cancel:
		cl.mu.Lock()
		delete(cl.waiting, m.Seq)
		cl.mu.Unlock()
		return nil, nil, os.ErrClosed
	}
}

// Mark is what the holder keeps of a program for a core started later:
// nothing can change it once the program is spawned.
type Mark = spawn.Mark

// Spawn starts s in the holder as s.ID, or under a new id, with s.Mark, or
// marked as sandboxed: what is not known to be otherwise is taken to be.
func (cl *Client) Spawn(s spawn.Spec) (spawn.Proc, error) {
	id, mark := s.ID, Mark{Sandboxed: true}
	if id == "" {
		id = newID()
	}
	if s.Mark != nil {
		mark = *s.Mark
	}
	return cl.SpawnAs(id, s, mark, 0)
}

// SpawnAs starts s in the holder as id, with mark, keeping ring bytes of
// its output (0 for DefaultRing).
func (cl *Client) SpawnAs(id string, s spawn.Spec, mark Mark, ring int) (*Proc, error) {
	m := &Msg{Op: OpSpawn, ID: id, Files: len(s.Files), Spawn: &Spawn{
		Argv: s.Argv, Dir: s.Dir, Env: s.Env, PTY: s.PTY, W: s.W, H: s.H,
		Sandboxed: mark.Sandboxed, Secret: mark.Secret, Label: mark.Label, Ring: ring,
	}}
	if len(s.Stdin) > MaxBody {
		return nil, fmt.Errorf("holder: standard input of %d bytes", len(s.Stdin))
	}
	r, _, err := cl.call(m, s.Stdin, s.Files, nil)
	if err != nil {
		return nil, err
	}
	return cl.attach(id, r.Pid, s.PTY, 0), nil
}

// List is the programs the holder has.
func (cl *Client) List() ([]Prog, error) {
	r, _, err := cl.call(&Msg{Op: OpList}, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	return r.Progs, nil
}

// Attach is a Proc of a program the holder has, as List tells it, read
// from pos on: as a core started after another takes it up.
func (cl *Client) Attach(p Prog, pos int64) *Proc {
	pr := cl.attach(p.ID, p.Pid, p.PTY, pos)
	if p.State != Running {
		pr.setExit(p.Exit)
	}
	return pr
}

func (cl *Client) attach(id string, pid int, pty bool, pos int64) *Proc {
	p := &Proc{cl: cl, id: id, pid: pid, pty: pty, pos: pos, exited: make(chan struct{}), closed: make(chan struct{})}
	if _, start, err := spawn.ProcInfo(pid); err == nil {
		p.start = start
	}
	cl.mu.Lock()
	cl.procs[id] = p
	e, ok := cl.exits[id]
	delete(cl.exits, id)
	cl.mu.Unlock()
	if ok {
		p.setExit(e)
	}
	return p
}

// Checkpoint gives the holder a block to keep for id at pos, unread.
func (cl *Client) Checkpoint(id string, pos int64, block []byte) error {
	_, _, err := cl.call(&Msg{Op: OpCheckpoint, ID: id, Pos: pos}, block, nil, nil)
	return err
}

// FetchCheckpoint is the last block the holder was given for id, and the
// position it is for; -1 and none when it has none. A holder from before
// fetch was added says it has no such op.
func (cl *Client) FetchCheckpoint(id string) (int64, []byte, error) {
	r, body, err := cl.call(&Msg{Op: OpFetch, ID: id}, nil, nil, nil)
	if err != nil {
		return -1, nil, err
	}
	return r.Pos, body, nil
}

// Shutdown has the holder stop every program and end.
func (cl *Client) Shutdown() error {
	_, _, err := cl.call(&Msg{Op: OpShutdown}, nil, nil, nil)
	return err
}

// Upgrade has the holder exec itself again, its programs kept; it
// answers from the new one.
func (cl *Client) Upgrade() error {
	_, _, err := cl.call(&Msg{Op: OpUpgrade}, nil, nil, nil)
	return err
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Proc is a program in the holder, as spawn.Proc.
type Proc struct {
	cl  *Client
	id  string
	pid int
	pty bool
	// start is when the process started, read before the core can have
	// it reaped: the holder waits for its ack.
	start uint64

	rmu  sync.Mutex // one Read at a time
	pos  int64
	lost int64
	eof  bool

	once    sync.Once
	exited  chan struct{}
	exit    *Exit
	closeMu sync.Once
	closed  chan struct{}
}

var _ spawn.Proc = (*Proc)(nil)

func (p *Proc) setExit(e *Exit) {
	p.once.Do(func() {
		p.exit = e
		close(p.exited)
	})
}

// ID is the program's id in the holder.
func (p *Proc) ID() string { return p.id }

// Lost is how many bytes of its output went from the ring before they
// were read.
func (p *Proc) Lost() int64 {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	return p.lost
}

func (p *Proc) Read(b []byte) (int, error) {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	if p.eof {
		return 0, io.EOF
	}
	if len(b) == 0 {
		return 0, nil
	}
	for {
		r, body, err := p.cl.call(&Msg{Op: OpRead, ID: p.id, Pos: p.pos, Max: min(len(b), MaxBody)}, nil, nil, p.closed)
		if err != nil {
			return 0, err
		}
		p.lost += r.Lost
		p.pos = r.Pos + int64(len(body))
		n := copy(b, body)
		if r.EOF {
			p.eof = true
			if n == 0 {
				return 0, io.EOF
			}
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (p *Proc) Write(b []byte) (int, error) {
	if !p.pty {
		return 0, os.ErrInvalid
	}
	n := 0
	for n < len(b) {
		chunk := b[n:min(len(b), n+MaxBody)]
		r, _, err := p.cl.call(&Msg{Op: OpWrite, ID: p.id}, chunk, nil, nil)
		if r != nil {
			n += int(r.Pos)
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (p *Proc) Resize(w, h int) error {
	_, _, err := p.cl.call(&Msg{Op: OpResize, ID: p.id, W: w, H: h}, nil, nil, nil)
	return err
}

// Wait waits for the program to exit, then tells the holder it can reap
// it.
func (p *Proc) Wait() error {
	select {
	case <-p.exited:
	case <-p.cl.gone:
		p.cl.mu.Lock()
		err := p.cl.err
		p.cl.mu.Unlock()
		return err
	}
	// Closed first, the holder reaps it itself and may have forgotten
	// it: the ack is not needed then.
	if _, _, err := p.cl.call(&Msg{Op: OpAck, ID: p.id, Exit: &Exit{}}, nil, nil, nil); err != nil && !p.isClosed() {
		return err
	}
	if p.exit == nil {
		return nil
	}
	return &spawn.ExitError{Code: p.exit.Code, Signal: syscall.Signal(p.exit.Signal), CoreDumped: p.exit.CoreDumped}
}

func (p *Proc) Pid() int { return p.pid }

func (p *Proc) StartTime() uint64 { return p.start }

func (p *Proc) Foreground() (int, bool) {
	if !p.pty {
		return 0, false
	}
	r, _, err := p.cl.call(&Msg{Op: OpList, ID: p.id}, nil, nil, nil)
	if err != nil || len(r.Progs) != 1 || r.Progs[0].Foreground <= 0 {
		return 0, false
	}
	return r.Progs[0].Foreground, true
}

func (p *Proc) Signal(pg int, sig syscall.Signal) error {
	_, _, err := p.cl.call(&Msg{Op: OpSignal, ID: p.id, PG: pg, Sig: int(sig)}, nil, nil, nil)
	return err
}

// Close lets go of its terminal or output: the holder closes it, and
// reaps and forgets the program once it has exited, no ack needed.
func (p *Proc) Close() error {
	var err error
	p.closeMu.Do(func() {
		close(p.closed)
		_, _, err = p.cl.call(&Msg{Op: OpAck, ID: p.id, Done: true}, nil, nil, nil)
		p.forgetIfClosed()
	})
	return err
}

func (p *Proc) isClosed() bool {
	select {
	case <-p.closed:
		return true
	default:
		return false
	}
}

// forgetIfClosed forgets a program closed and exited here: the holder
// has, or will once its groups are gone. One closed while it runs is
// kept until its exit comes, so the exit has somewhere to go.
func (p *Proc) forgetIfClosed() {
	if !p.isClosed() {
		return
	}
	select {
	case <-p.exited:
	default:
		return
	}
	p.cl.mu.Lock()
	if p.cl.procs[p.id] == p {
		delete(p.cl.procs, p.id)
	}
	p.cl.mu.Unlock()
}
