package spawn

import "golang.org/x/sys/unix"

// exited waits until the child pid has ended, without reaping it, and
// reports whether it could.
func exited(pid int) bool {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err == nil
		}
	}
}
