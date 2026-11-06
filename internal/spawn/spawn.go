// Package spawn starts the programs nerdr runs for its tabs (agents,
// terminals, plugins' screens and runs) and is what nerdr has of each
// while it runs. Local starts them in this process. A holder process that
// keeps them through a restart of nerdr can start them in its place: what
// is asked of a program here is what such a holder can be asked over a
// socket, so nothing else changes when it does.
package spawn

import (
	"bytes"
	"os"
	"os/exec"
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
	// Wait waits for it to exit, and says how: an *exec.ExitError when
	// it did not exit 0. It is called once.
	Wait() error
	// Pid is its process, which leads its process group.
	Pid() int
	// Foreground is the foreground process group of its terminal.
	Foreground() (int, bool)
	// Signal sends sig to process group pg, its own or the foreground one
	// of its terminal; sig 0 asks whether the group is there, and the
	// error is syscall.ESRCH when it is not.
	Signal(pg int, sig syscall.Signal) error
	// Close lets go of its terminal or output.
	Close() error
}

// Spawner starts programs.
type Spawner interface {
	Spawn(s Spec) (Proc, error)
}

// Local starts programs as children of this process.
type Local struct{}

var _ Spawner = Local{}

func (Local) Spawn(s Spec) (Proc, error) {
	cmd := exec.Command(s.Argv[0], s.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.ExtraFiles = s.Dir, s.Env, s.Files
	if s.PTY {
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(s.W), Rows: uint16(s.H)})
		if denied.Err(err) != nil {
			return nil, err
		}
		return &local{cmd: cmd, out: f, tty: f}, nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if s.Stdin != nil {
		cmd.Stdin = bytes.NewReader(s.Stdin)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	pw.Close()
	return &local{cmd: cmd, out: pr}, nil
}

// local is a program Local started: out is its terminal, tty too, or the
// pipe it prints to, tty nil.
type local struct {
	cmd *exec.Cmd
	out *os.File
	tty *os.File
}

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

func (l *local) Wait() error { return l.cmd.Wait() }

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
	return syscall.Kill(-pg, sig)
}

func (l *local) Close() error { return l.out.Close() }
