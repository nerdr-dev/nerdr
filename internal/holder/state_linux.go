package holder

import (
	"os"

	"golang.org/x/sys/unix"
)

// stateFile is where an upgrade leaves the holder's state: a file in
// memory only, never on disk.
func stateFile() (*os.File, error) {
	fd, err := unix.MemfdCreate("nerdr-holder", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "holder state"), nil
}
