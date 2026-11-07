package hooks

import "golang.org/x/sys/unix"

const peerSupported = true

func peerPid(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}

// procInfo is pid's parent, and when it started, in microseconds.
func procInfo(pid int) (parent int, start uint64, err error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, 0, err
	}
	tv := kp.Proc.P_starttime
	return int(kp.Eproc.Ppid), uint64(tv.Sec)*1e6 + uint64(tv.Usec), nil
}
