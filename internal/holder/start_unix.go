package holder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// started is a program just started: its process, the end of its
// terminal or output the holder reads, and the pipe to its standard input
// when it has one and no terminal.
type started struct {
	pid int
	out *os.File
	in  *os.File
}

// start starts s, with stdin as its standard input when it has no
// terminal and files as descriptors 3 and on. It starts it with
// os.StartProcess, not exec.Cmd, and never waits for it: the holder reaps
// it only once the core has its exit.
func start(s *Spawn, stdin []byte, files []*os.File) (*started, error) {
	if len(s.Argv) == 0 {
		return nil, fmt.Errorf("holder: no program")
	}
	path := s.Argv[0]
	if filepath.Base(path) == path {
		p, err := exec.LookPath(path)
		if err != nil {
			return nil, err
		}
		path = p
	}
	attr := &os.ProcAttr{Dir: s.Dir, Env: s.Env}
	if attr.Env == nil {
		attr.Env = []string{}
	}
	var st started
	var childEnds []*os.File
	defer func() { closeAll(childEnds) }()
	if s.PTY {
		master, tty, err := openPTY(s.W, s.H)
		if err != nil {
			return nil, err
		}
		childEnds = append(childEnds, tty)
		st.out = master
		attr.Files = []*os.File{tty, tty, tty}
		attr.Sys = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	} else {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		childEnds = append(childEnds, pw)
		st.out = pr
		var in *os.File
		if stdin != nil {
			ir, iw, err := os.Pipe()
			if err != nil {
				pr.Close()
				return nil, err
			}
			childEnds = append(childEnds, ir)
			in, st.in = ir, iw
		} else {
			if in, err = os.Open(os.DevNull); err != nil {
				pr.Close()
				return nil, err
			}
			childEnds = append(childEnds, in)
		}
		attr.Files = []*os.File{in, pw, pw}
		attr.Sys = &syscall.SysProcAttr{Setpgid: true}
	}
	attr.Files = append(attr.Files, files...)
	p, err := os.StartProcess(path, s.Argv, attr)
	if err != nil {
		st.out.Close()
		if st.in != nil {
			st.in.Close()
		}
		return nil, err
	}
	st.pid = p.Pid
	// Release lets go of the process's handle; it does not wait for it.
	p.Release()
	return &st, nil
}

// openPTY opens a terminal w×h: the master the holder keeps, not
// blocking so it can be read with a deadline, and the end the program
// gets.
func openPTY(w, h int) (*os.File, *os.File, error) {
	m, tty, err := pty.Open()
	if err != nil {
		return nil, nil, err
	}
	// pty.Open's master has had Fd called on it, which makes it
	// blocking for good; a copy can be made pollable again.
	fd, err := dupCloexec(int(m.Fd()))
	m.Close()
	if err != nil {
		tty.Close()
		return nil, nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		tty.Close()
		return nil, nil, err
	}
	master := os.NewFile(uintptr(fd), "pty")
	if w > 0 && h > 0 {
		if err := setSize(master, w, h); err != nil {
			master.Close()
			tty.Close()
			return nil, nil, err
		}
	}
	return master, tty, nil
}

func dupCloexec(fd int) (int, error) {
	return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
}

// control runs f on f's descriptor without making it blocking, as Fd
// would.
func control(file *os.File, f func(fd int) error) error {
	rc, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = f(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

func setSize(master *os.File, w, h int) error {
	return control(master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(w), Row: uint16(h)})
	})
}

func foreground(master *os.File) int {
	pg := 0
	if control(master, func(fd int) (err error) { pg, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP); return }) != nil {
		return 0
	}
	return pg
}

// exitOf is how a program ended, from its wait status; nil for exit 0.
func exitOf(ws syscall.WaitStatus) *Exit {
	switch {
	case ws.Signaled():
		return &Exit{Signal: int(ws.Signal()), CoreDumped: ws.CoreDump()}
	case ws.ExitStatus() != 0:
		return &Exit{Code: ws.ExitStatus()}
	}
	return nil
}

// reap reaps pid, which has exited.
func reap(pid int) {
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(pid, &ws, 0, nil)
		if err != syscall.EINTR {
			return
		}
	}
}
