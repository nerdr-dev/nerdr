package holder

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// waitExit waits for pid to exit and says how, leaving it a zombie: its
// PID cannot go to another process before the holder reaps it. A kqueue
// tells of the exit and its status; should pid be a zombie already when
// the kqueue is set, waitid with WNOWAIT reads its status without reaping
// it. The bool says it was reaped all the same, which it is not here.
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
		e, err := waitidNoWait(pid)
		return e, false, err
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

// siginfo is the start of macOS's siginfo_t, with room for the rest.
type siginfo struct {
	Signo, Errno, Code int32
	Pid                int32
	Uid                uint32
	Status             int32
	_                  [128]byte
}

// si_code of a child's exit, and idtype_t P_PID, on macOS.
const (
	cldExited = 1
	cldDumped = 3
	pPID      = 1
)

func waitidNoWait(pid int) (*Exit, error) {
	var si siginfo
	for {
		_, _, errno := unix.Syscall6(unix.SYS_WAITID, pPID, uintptr(pid), uintptr(unsafe.Pointer(&si)), unix.WEXITED|unix.WNOWAIT, 0, 0)
		if errno == unix.EINTR {
			continue
		}
		if errno != 0 {
			return nil, errno
		}
		break
	}
	switch si.Code {
	case cldExited:
		if si.Status == 0 {
			return nil, nil
		}
		return &Exit{Code: int(si.Status)}, nil
	case cldDumped:
		return &Exit{Signal: int(si.Status), CoreDumped: true}, nil
	default:
		return &Exit{Signal: int(si.Status)}, nil
	}
}
