package holder

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// waitExit waits for pid to exit and says how, leaving it a zombie: its
// PID cannot go to another process before the holder reaps it. macOS has
// no waitid with WNOWAIT in Go, so a kqueue tells of the exit and its
// status. Should pid have exited before the kqueue is set, it is reaped
// here for its status, the one case where it goes before the core has
// it, and the bool says so.
func waitExit(pid int) (*Exit, bool, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, false, err
	}
	defer unix.Close(kq)
	ev := unix.Kevent_t{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT | unix.NOTE_EXITSTATUS,
	}
	if _, err := unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil); err == unix.ESRCH {
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &ws, 0, nil); err != nil {
			return nil, false, err
		}
		return exitOf(ws), true, nil
	} else if err != nil {
		return nil, false, err
	}
	out := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, nil, out, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if n == 1 {
			return exitOf(syscall.WaitStatus(out[0].Data)), false, nil
		}
	}
}
