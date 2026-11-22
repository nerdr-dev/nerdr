package attach

import (
	"net"
	"os"
	"syscall"
)

// Pair is two connected ends, already in, for a client and a core in the
// same process: what one sends the other receives, as over the socket.
func Pair() (*Conn, *Conn, error) {
	// Under ForkLock, so no program started meanwhile is born with an
	// end before it is closed on exec.
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, err
	}
	var conns [2]net.Conn
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "nerdr-pair")
		c, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			if i == 1 {
				_ = conns[0].Close()
			} else {
				_ = syscall.Close(fds[1])
			}
			return nil, nil, err
		}
		conns[i] = c
	}
	return newConn(conns[0]), newConn(conns[1]), nil
}
