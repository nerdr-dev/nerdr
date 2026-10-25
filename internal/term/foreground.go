package term

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Busy reports whether a program other than the one started runs in the
// terminal's foreground: in a shell's pane, a command it was given that
// has not ended. False when it cannot tell.
func (p *Pane) Busy() bool {
	pg, ok := p.foreground()
	return ok && p.cmd.Process != nil && pg != p.cmd.Process.Pid
}

// SignalForeground sends sig to the terminal's foreground process group,
// when that is not the program started: to stop a command run in a shell
// that did not stop for ctrl+c.
func (p *Pane) SignalForeground(sig syscall.Signal) {
	if pg, ok := p.foreground(); ok && p.cmd.Process != nil && pg != p.cmd.Process.Pid && pg > 0 {
		_ = syscall.Kill(-pg, sig)
	}
}

// foreground is the terminal's foreground process group. It goes through
// SyscallConn, not Fd, which would make the pty blocking.
func (p *Pane) foreground() (int, bool) {
	p.mu.Lock()
	exited := p.exited
	p.mu.Unlock()
	if exited {
		return 0, false
	}
	rc, err := p.pty.SyscallConn()
	if err != nil {
		return 0, false
	}
	pg, ierr := 0, error(nil)
	if err := rc.Control(func(fd uintptr) { pg, ierr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) }); err != nil || ierr != nil {
		return 0, false
	}
	return pg, true
}

// Rerun stops the command running in a shell's terminal, if one is, and
// types command in its place: ctrl+c, as the user would, then SIGTERM and
// SIGKILL to what did not stop. It waits for the shell, so not on the
// screen's goroutine, and reports whether the shell was back to type in.
func (p *Pane) Rerun(command string) bool {
	// ctrl+c clears what is half typed too; what is typed right after it
	// would be thrown away with it, so it waits a moment.
	p.Write([]byte{3})
	time.Sleep(rerunStep)
	for _, sig := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
		if sig != 0 {
			p.SignalForeground(sig)
		}
		for end := time.Now().Add(rerunWait); p.Busy() && time.Now().Before(end); {
			time.Sleep(rerunStep)
		}
		if !p.Busy() {
			time.Sleep(rerunStep) // for the shell's prompt
			p.Write([]byte(command + "\r"))
			return true
		}
	}
	return false
}

// rerunWait is how long Rerun gives a command to stop, at each step;
// rerunStep how often it looks.
var rerunWait, rerunStep = 3 * time.Second, 100 * time.Millisecond
