package app

import (
	"os"

	"golang.org/x/sys/unix"
)

// ptyEchoes reports whether the terminal of pty master ptmx shows what is
// typed on it: not while a program reads a password there. It does when
// that cannot be told, so that nothing is counted as hidden that is shown.
func ptyEchoes(ptmx *os.File) bool {
	// Not by Fd, which would have the file block from then on.
	rc, err := ptmx.SyscallConn()
	if err != nil {
		return true
	}
	echoes := true
	_ = rc.Control(func(fd uintptr) {
		if t, err := unix.IoctlGetTermios(int(fd), getTermios); err == nil {
			echoes = t.Lflag&unix.ECHO != 0
		}
	})
	return echoes
}
