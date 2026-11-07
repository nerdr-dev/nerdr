package hooks

import "golang.org/x/sys/unix"

const peerSupported = true

func peerPid(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}
