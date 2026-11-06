package holder

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openThroughProc(pid, fd int) error {
	f, err := os.OpenFile(fmt.Sprintf("/proc/%d/fd/%d", pid, fd), os.O_RDWR, 0)
	if err == nil {
		f.Close()
	}
	return err
}

func getfd(pid, fd int) error {
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return fmt.Errorf("pidfd_open: %w", err)
	}
	defer unix.Close(pidfd)
	got, err := unix.PidfdGetfd(pidfd, fd, 0)
	if err != nil {
		return fmt.Errorf("pidfd_getfd: %w", err)
	}
	unix.Close(got)
	return nil
}
