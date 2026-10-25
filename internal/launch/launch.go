// Package launch starts a program with a window of its own — a browser, an
// editor — apart from nerdr.
package launch

import (
	"os/exec"
	"syscall"
)

// Detached starts argv and returns once it has been started.
func Detached(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	// Not the terminal: a browser or editor prints its startup chatter over
	// the screen, and keeps doing so long after the command has returned.
	// Nil sends them to /dev/null.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// Its own process group, so quitting nerdr doesn't take the program
	// with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The program outlives this call; reap it so it can't linger as a
	// zombie while nerdr stays open.
	go func() { _ = cmd.Wait() }()
	return nil
}
