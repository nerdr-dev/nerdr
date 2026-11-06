package holder

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// An upgrade execs the holder again, as the nerdr it was started through,
// which after an update is the new one: the same process, its programs
// still its children, their terminals and the core's socket kept open
// across. What it has in memory goes in a state file the new holder
// reads, one in memory only where there is one (Linux); it is never left
// on disk, for it holds what the programs printed.

// resumeArg starts an upgraded holder, the descriptor of its state after.
const resumeArg = "--resume"

// stateVersion is the version of the state an upgrade leaves. A holder
// that does not know a state's version does not exec into it, so a newer
// nerdr must read every version still in use.
const stateVersion = 1

// defaultExecArgv is how a holder is started again: as the nerdr it was
// started through.
var defaultExecArgv = func() ([]string, error) {
	bin, err := spawn.Bin()
	if err != nil {
		return nil, err
	}
	return []string{bin, "holder"}, nil
}

type state struct {
	Version  int        `json:"version"`
	CoreArgv []string   `json:"core_argv"`
	CorePid  int        `json:"core_pid"`
	ConnFD   int        `json:"conn_fd"`
	Outbox   []frame    `json:"outbox,omitempty"`
	Reads    []*Msg     `json:"reads,omitempty"`
	Reply    uint64     `json:"reply"` // the upgrade to answer
	Progs    []progSave `json:"progs"`
}

type progSave struct {
	ID        string      `json:"id"`
	Spawn     Spawn       `json:"spawn"`
	Pid       int         `json:"pid"`
	Started   int64       `json:"started"`
	OutFD     int         `json:"out_fd"` // -1 once let go of
	InFD      int         `json:"in_fd"`  // -1 for none
	Stdin     []byte      `json:"stdin,omitempty"`
	Ring      []byte      `json:"ring,omitempty"`
	End       int64       `json:"end"`
	OutEOF    bool        `json:"out_eof,omitempty"`
	Exited    bool        `json:"exited,omitempty"`
	Exit      *Exit       `json:"exit,omitempty"`
	Reaped    bool        `json:"reaped,omitempty"`
	ExitAcked bool        `json:"exit_acked,omitempty"`
	Done      bool        `json:"done,omitempty"`
	Acked     int64       `json:"acked,omitempty"`
	CkPos     int64       `json:"ck_pos"`
	CkData    []byte      `json:"ck_data,omitempty"`
	Writes    []writeSave `json:"writes,omitempty"`
}

type writeSave struct {
	Seq  uint64 `json:"seq"`
	Data []byte `json:"data"`
	N    int    `json:"n"`
}

// doUpgrade is the upgrade op: it stops what moves bytes, between reads
// and writes, and the sender between frames, then execs the holder again
// with all that is left in its state. It returns only when that failed,
// with everything running again as before.
func (h *Holder) doUpgrade(seq uint64) error {
	argv, err := h.execArgv()
	if err != nil {
		return err
	}
	h.mu.Lock()
	s := h.core
	h.paused = true
	close(h.pausing)
	past := time.Unix(1, 0)
	var waits []chan struct{}
	for _, id := range h.order {
		p := h.progs[id]
		if !p.done {
			p.out.SetReadDeadline(past)
			p.out.SetWriteDeadline(past)
		}
		if p.in != nil {
			p.in.SetWriteDeadline(past)
		}
		p.poke()
		waits = append(waits, p.readerQ, p.writerQ)
	}
	h.mu.Unlock()
	for _, w := range waits {
		<-w
	}
	if s != nil {
		s.stop()
	}
	// From here h.mu is held until the exec, which does not return: what
	// is saved is all there is.
	h.mu.Lock()
	st, keep, err := h.save(s, seq)
	if err == nil {
		err = h.exec(argv, st, keep)
	}
	h.resumeAfterFailure(s)
	h.mu.Unlock()
	return fmt.Errorf("holder: upgrade: %w", err)
}

// save is what the holder has, and the descriptors it is in. h.mu is
// held.
func (h *Holder) save(s *sender, seq uint64) (*state, []int, error) {
	st := &state{Version: stateVersion, CoreArgv: h.coreArgv, CorePid: h.corePid, ConnFD: -1, Reply: seq}
	var keep []int
	fdOf := func(f *os.File) (int, error) {
		fd := -1
		err := control(f, func(n int) error { fd = n; return nil })
		if err == nil {
			keep = append(keep, fd)
		}
		return fd, err
	}
	if s != nil {
		rc, err := s.c.c.SyscallConn()
		if err != nil {
			return nil, nil, err
		}
		if err := rc.Control(func(fd uintptr) { st.ConnFD = int(fd) }); err != nil {
			return nil, nil, err
		}
		keep = append(keep, st.ConnFD)
		st.Outbox = s.take()
		for _, m := range h.reads {
			st.Reads = append(st.Reads, m)
		}
	}
	for _, id := range h.order {
		p := h.progs[id]
		ps := progSave{
			ID: p.id, Spawn: p.spawn, Pid: p.pid, Started: p.started, OutFD: -1, InFD: -1,
			Stdin: p.stdin, Ring: p.ring.bytes(), End: p.ring.end, OutEOF: p.outEOF,
			Exited: p.exited, Exit: p.exit, Reaped: p.reaped, ExitAcked: p.exitAcked, Done: p.done,
			Acked: p.acked, CkPos: p.ckPos, CkData: p.ckData,
		}
		var err error
		if !p.done {
			if ps.OutFD, err = fdOf(p.out); err != nil {
				return nil, nil, err
			}
		}
		if p.in != nil {
			if ps.InFD, err = fdOf(p.in); err != nil {
				return nil, nil, err
			}
		}
		for _, w := range p.writes {
			ps.Writes = append(ps.Writes, writeSave{Seq: w.seq, Data: w.data, N: w.n})
		}
		st.Progs = append(st.Progs, ps)
	}
	return st, keep, nil
}

// exec writes st to a state file and execs argv with it; keep are the
// descriptors the new holder needs. It returns only on failure, with
// keep closed on exec again.
func (h *Holder) exec(argv []string, st *state, keep []int) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	f, err := stateFile()
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var sfd int
	if err := control(f, func(fd int) error { sfd = fd; return nil }); err != nil {
		return err
	}
	keep = append(keep, sfd)
	for _, fd := range keep {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
			cloexec(keep)
			return err
		}
	}
	argv = append(argv, resumeArg, strconv.Itoa(sfd))
	err = syscall.Exec(argv[0], argv, os.Environ())
	cloexec(keep)
	return fmt.Errorf("exec %s: %w", argv[0], err)
}

func cloexec(fds []int) {
	for _, fd := range fds {
		syscall.CloseOnExec(fd)
	}
}

// resumeAfterFailure starts again what doUpgrade stopped. h.mu is held.
func (h *Holder) resumeAfterFailure(s *sender) {
	h.paused = false
	h.pausing = make(chan struct{})
	for _, id := range h.order {
		p := h.progs[id]
		if !p.done {
			p.out.SetReadDeadline(time.Time{})
			p.out.SetWriteDeadline(time.Time{})
		}
		if p.in != nil {
			p.in.SetWriteDeadline(time.Time{})
		}
		h.run(p)
	}
	if s != nil {
		s.start()
		for _, m := range h.reads {
			go h.read(s, h.gone, m)
		}
	}
}

// Resume is a holder exec'd by an upgrade: it takes up the state the old
// one left on descriptor fd, and goes on as it did.
func Resume(fd int) error {
	f := os.NewFile(uintptr(fd), "holder state")
	b, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	if st.Version != stateVersion {
		return fmt.Errorf("holder: state of version %d", st.Version)
	}
	h := New()
	h.coreArgv, h.corePid, h.upgrade = st.CoreArgv, st.CorePid, h.doUpgrade
	var s *sender
	var c *conn
	if st.ConnFD >= 0 {
		if c, err = fileConn(pollable(st.ConnFD, "holder socket")); err != nil {
			return err
		}
		s = newSenderWith(c, append(st.Outbox, frame{M: &Msg{Op: OpUpgrade, Seq: st.Reply}}))
	}
	h.mu.Lock()
	for _, ps := range st.Progs {
		p := &prog{
			id: ps.ID, spawn: ps.Spawn, pid: ps.Pid, started: ps.Started, stdin: ps.Stdin,
			ring: newRing(ps.Spawn.Ring), outEOF: ps.OutEOF, changed: make(chan struct{}),
			exited: ps.Exited, exit: ps.Exit, reaped: ps.Reaped, exitAcked: ps.ExitAcked, done: ps.Done,
			acked: ps.Acked, ckPos: ps.CkPos, ckData: ps.CkData,
		}
		p.ring.restore(ps.Ring, ps.End)
		if ps.OutFD >= 0 {
			p.out = pollable(ps.OutFD, "pty")
		}
		if ps.InFD >= 0 {
			p.in = pollable(ps.InFD, "stdin")
		}
		for _, w := range ps.Writes {
			p.writes = append(p.writes, &write{seq: w.Seq, data: w.Data, n: w.N, to: s})
		}
		h.progs[p.id] = p
		h.order = append(h.order, p.id)
		h.run(p)
		if !p.exited {
			go h.watch(p)
		}
	}
	var first func() error
	if s != nil {
		gone := make(chan struct{})
		h.core, h.gone = s, gone
		for _, m := range st.Reads {
			h.reads[m.Seq] = m
			go h.read(s, gone, m)
		}
		served := make(chan error, 1)
		go func() {
			defer s.stop()
			defer h.detach(s, gone)
			served <- h.serve(s, gone)
		}()
		pid := st.CorePid
		first = func() error { return h.superviseCore(pid, c, served) }
	}
	h.mu.Unlock()
	return h.keepCoreUp(st.CoreArgv, first)
}

// pollable is descriptor fd as a file the runtime polls, so deadlines
// work on it, and closed on exec.
func pollable(fd int, name string) *os.File {
	unix.SetNonblock(fd, true)
	syscall.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), name)
}
