// Package spawn starts the programs nerdr runs for its tabs (agents,
// terminals, plugins' screens and runs) and is what nerdr has of each
// while it runs. Local starts them in this process. A holder process that
// keeps them through a restart of nerdr can start them in its place: what
// is asked of a program here is what such a holder can be asked over a
// socket, so nothing else changes when it does.
package spawn

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// Spec is a program to start.
type Spec struct {
	Argv []string
	Dir  string
	Env  []string
	// PTY starts it in a terminal of its own, W×H, as the leader of a
	// session. Without one, Read reads what it prints, standard output and
	// error both; it reads Stdin; and it leads a process group of its own,
	// so a stop stops what it started too.
	PTY   bool
	W, H  int
	Stdin []byte
	// Files are given to it as descriptors 3 and on. They are the
	// caller's to close once Spawn has returned.
	Files []*os.File
	// ID and Mark are what a holder keeps the program under, for a core
	// started after a crash to find it and know it by: its id, unique in
	// the holder, and what nothing can change once it is started. Local
	// has no use for them. With no ID the holder makes one up; with no
	// Mark it takes the program to be sandboxed.
	ID   string
	Mark *Mark
}

// Mark is what a holder keeps of a program for a core started later.
type Mark struct {
	// Sandboxed is set when the program runs in a sandbox.
	Sandboxed bool
	// Secret is what the program's reports to nerdr are known by.
	Secret string
	// Label is what the core needs to take the program up again: which
	// tab it is of, and as what.
	Label string
}

// Proc is a program started, and its terminal or output.
type Proc interface {
	// Read reads what the program wrote; it returns an error once there
	// is no more, or Close was called.
	Read(b []byte) (int, error)
	// Write writes to its terminal, which may be slow to take it.
	Write(b []byte) (int, error)
	// Resize changes the size of its terminal.
	Resize(w, h int) error
	// Wait waits for it to exit, and says how: an *ExitError when it did
	// not exit 0. It is called once.
	Wait() error
	// Pid is its process, which leads its process group.
	Pid() int
	// StartTime is when its process started, as ProcInfo has it, read
	// before it could be reaped: what tells it from a process given its
	// id once it has ended. 0 when not known.
	StartTime() uint64
	// Foreground is the foreground process group of its terminal.
	Foreground() (int, bool)
	// Signal sends sig to process group pg, its own or the foreground one
	// of its terminal; sig 0 asks whether the group is there, and the
	// error is syscall.ESRCH when it is not.
	Signal(pg int, sig syscall.Signal) error
	// Close lets go of its terminal or output.
	Close() error
}

// ExitError is how a program that did not exit 0 ended: the code it
// exited with, or the signal that ended it. It is told as an
// *exec.ExitError is, and a holder can send it over a socket.
type ExitError struct {
	Code       int
	Signal     syscall.Signal
	CoreDumped bool
}

func (e *ExitError) Error() string {
	if e.Signal == 0 {
		return "exit status " + strconv.Itoa(e.Code)
	}
	s := "signal: " + e.Signal.String()
	if e.CoreDumped {
		s += " (core dumped)"
	}
	return s
}

// Status is its exit status as a shell has it: the code, or 128 and the
// signal.
func (e *ExitError) Status() int {
	if e.Signal != 0 {
		return 128 + int(e.Signal)
	}
	return e.Code
}

// Spawner starts programs.
type Spawner interface {
	Spawn(s Spec) (Proc, error)
}

// Local starts programs as children of this process.
type Local struct{}

var _ Spawner = Local{}

func (Local) Spawn(s Spec) (Proc, error) {
	if len(s.Argv) == 0 {
		return nil, errors.New("spawn: no program")
	}
	cmd := exec.Command(s.Argv[0], s.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.ExtraFiles = s.Dir, s.Env, s.Files
	if s.PTY {
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(s.W), Rows: uint16(s.H)})
		if denied.Err(err) != nil {
			return nil, err
		}
		return &local{cmd: cmd, out: f, tty: f, start: startOf(cmd)}, nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	var inR, inW *os.File
	if s.Stdin != nil {
		// Feed Stdin through an *os.File pipe of our own rather than a
		// bytes.Reader: os/exec would otherwise wait on its stdin-copy
		// goroutine inside cmd.Wait (while local.Wait holds l.mu),
		// deadlocking Signal if a grandchild inherited stdin without
		// reading it.
		inR, inW, err = os.Pipe()
		if err != nil {
			pr.Close()
			pw.Close()
			return nil, err
		}
		cmd.Stdin = inR
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		if inR != nil {
			inR.Close()
			inW.Close()
		}
		return nil, err
	}
	pw.Close()
	if inR != nil {
		inR.Close()
		go func(w *os.File, data []byte) {
			_, _ = w.Write(data)
			_ = w.Close()
		}(inW, s.Stdin)
	}
	return &local{cmd: cmd, out: pr, in: inW, start: startOf(cmd)}, nil
}

// startOf is when cmd's process started; it is not reaped before Wait.
func startOf(cmd *exec.Cmd) uint64 {
	if cmd.Process == nil {
		return 0
	}
	_, start, err := ProcInfo(cmd.Process.Pid)
	if err != nil {
		return 0
	}
	return start
}

// local is a program Local started: out is its terminal, tty too, or the
// pipe it prints to, tty nil.
type local struct {
	cmd   *exec.Cmd
	out   *os.File
	tty   *os.File
	in    *os.File // write end of Spec.Stdin pipe, nil when none
	start uint64
	// reaped is that Wait has reaped the process, pgGone that its group
	// was then seen empty. A group's id is not reused while the group has
	// a process in it, so what is left of it is signalled, as a holder
	// does, until it is seen gone, and then never again: the id can then
	// be another's. mu guards both.
	mu             sync.Mutex
	reaped, pgGone bool
}

func (l *local) StartTime() uint64 { return l.start }

func (l *local) Read(b []byte) (int, error) { return l.out.Read(b) }

func (l *local) Write(b []byte) (int, error) {
	if l.tty == nil {
		return 0, os.ErrInvalid
	}
	return l.tty.Write(b)
}

func (l *local) Resize(w, h int) error {
	if l.tty == nil {
		return nil
	}
	return pty.Setsize(l.tty, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
}

func (l *local) Wait() error {
	// Where the process can be waited for without being reaped (exited),
	// it is reaped with mu held, so that no Signal comes between the reap
	// and reaped; elsewhere one can, as it could before.
	held := l.cmd.Process != nil && exited(l.cmd.Process.Pid)
	if held {
		l.mu.Lock()
	}
	err := l.cmd.Wait()
	if !held {
		l.mu.Lock()
	}
	l.reaped = true
	l.mu.Unlock()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		return &ExitError{Code: ee.ExitCode()}
	}
	if ws.Signaled() {
		return &ExitError{Signal: ws.Signal(), CoreDumped: ws.CoreDump()}
	}
	return &ExitError{Code: ws.ExitStatus()}
}

func (l *local) Pid() int {
	if l.cmd.Process == nil {
		return 0
	}
	return l.cmd.Process.Pid
}

// Foreground goes through SyscallConn, not Fd, which would make the
// terminal blocking.
func (l *local) Foreground() (int, bool) {
	if l.tty == nil {
		return 0, false
	}
	rc, err := l.tty.SyscallConn()
	if err != nil {
		return 0, false
	}
	pg, ierr := 0, error(nil)
	if err := rc.Control(func(fd uintptr) { pg, ierr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) }); err != nil || ierr != nil {
		return 0, false
	}
	return pg, true
}

func (l *local) Signal(pg int, sig syscall.Signal) error {
	if pg <= 0 {
		return syscall.ESRCH
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	own := pg == l.Pid()
	if own && l.pgGone {
		return syscall.ESRCH // its group's id may be another's since
	}
	err := syscall.Kill(-pg, sig)
	if own && l.reaped && err == syscall.ESRCH {
		l.pgGone = true
	}
	return err
}

func (l *local) Close() error {
	if l.in != nil {
		_ = l.in.Close()
	}
	return l.out.Close()
}
