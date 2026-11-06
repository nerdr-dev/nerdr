package holder

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// si_code of a child's exit.
const (
	cldExited = 1
	cldDumped = 3
)

// sigchld is siginfo_t as waitid fills it for a child: unix.Siginfo with
// the fields it hides.
type sigchld struct {
	Signo, Errno, Code int32
	_                  int32
	Pid                int32
	Uid                uint32
	Status             int32
	_                  [100]byte
}

// waitExit waits for pid to exit and says how, leaving it a zombie: its
// PID cannot go to another process before the holder reaps it. The bool
// says it was reaped all the same, which it is not here.
func waitExit(pid int) (*Exit, bool, error) {
	var si sigchld
	for {
		err := unix.Waitid(unix.P_PID, pid, (*unix.Siginfo)(unsafe.Pointer(&si)), unix.WEXITED|unix.WNOWAIT, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		break
	}
	switch si.Code {
	case cldExited:
		if si.Status == 0 {
			return nil, false, nil
		}
		return &Exit{Code: int(si.Status)}, false, nil
	case cldDumped:
		return &Exit{Signal: int(si.Status), CoreDumped: true}, false, nil
	default: // CLD_KILLED
		return &Exit{Signal: int(si.Status)}, false, nil
	}
}
