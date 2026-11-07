package spawn

import "golang.org/x/sys/unix"

// ProcInfo is pid's parent, and when it started, in microseconds.
func ProcInfo(pid int) (parent int, start uint64, err error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, 0, err
	}
	tv := kp.Proc.P_starttime
	return int(kp.Eproc.Ppid), uint64(tv.Sec)*1e6 + uint64(tv.Usec), nil
}
