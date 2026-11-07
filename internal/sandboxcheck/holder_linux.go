package sandboxcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// maxFd is how many of a process's descriptors are tried where they
// cannot be listed.
const maxFd = 64

// probeHolder tries to take the holder's socket from the holder and the
// core, by each way the kernel has: open it through /proc, take it with
// pidfd_getfd, or trace the process. Outside the core's pid namespace
// their ids are of other processes, if any, and nothing is tried.
func probeHolder(t Target) []string {
	if !samePidNS(t.PidNS) {
		return nil
	}
	var out []string
	for _, pid := range t.Pids {
		if throughProc(pid) {
			out = append(out, fmt.Sprintf("/proc %d", pid))
		}
		if getfd(pid) {
			out = append(out, fmt.Sprintf("pidfd_getfd %d", pid))
		}
		if traces(pid) {
			out = append(out, fmt.Sprintf("ptrace %d", pid))
		}
	}
	return out
}

// fds are pid's descriptors that are sockets, or the first maxFd when
// they cannot be listed.
func fds(pid int, sockets bool) []int {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	ents, err := os.ReadDir(dir)
	if err != nil {
		out := make([]int, maxFd)
		for i := range out {
			out[i] = i
		}
		return out
	}
	var out []int
	for _, e := range ents {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if sockets {
			if l, err := os.Readlink(filepath.Join(dir, e.Name())); err != nil || !strings.HasPrefix(l, "socket:") {
				continue
			}
		}
		out = append(out, n)
	}
	return out
}

// throughProc reports whether a socket of pid opens through its /proc
// entry; Linux never opens a socket so, but a sandbox's own /proc might.
func throughProc(pid int) bool {
	for _, fd := range fds(pid, true) {
		f, err := os.OpenFile(fmt.Sprintf("/proc/%d/fd/%d", pid, fd), os.O_RDWR, 0)
		if err == nil {
			f.Close()
			return true
		}
	}
	return false
}

// getfd reports whether pidfd_getfd takes any descriptor of pid.
func getfd(pid int) bool {
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return false
	}
	defer unix.Close(pidfd)
	for _, fd := range fds(pid, false) {
		got, err := unix.PidfdGetfd(pidfd, fd, 0)
		if err == nil {
			unix.Close(got)
			return true
		}
		if err == unix.EPERM || err == unix.ENOSYS || err == unix.EACCES {
			return false // the same for every descriptor
		}
	}
	return false
}

// traces reports whether this process may trace pid. It seizes it, which
// does not stop it, then lets it go at once: stopped just long enough to
// be let go of, as the kernel has a tracer do.
func traces(pid int) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.PtraceSeize(pid); err != nil {
		return false
	}
	if err := unix.PtraceInterrupt(pid); err == nil {
		var ws unix.WaitStatus
		for end := time.Now().Add(time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
			if got, err := unix.Wait4(pid, &ws, unix.WALL|unix.WNOHANG, nil); err != nil || got == pid {
				break
			}
		}
	}
	// Should it fail, the kernel lets pid go when this process ends.
	_ = unix.PtraceDetach(pid)
	return true
}
