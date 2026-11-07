package hooks

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const peerSupported = true

func peerPid(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(cred.Pid), nil
}

// procInfo reads the parent and the start time from /proc/<pid>/stat,
// after the command's name in parentheses, which may hold any character
// but the last ")": fields 4 and 22, in clock ticks since boot.
func procInfo(pid int) (parent int, start uint64, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, 0, errors.New("no command in stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, 0, errors.New("short stat")
	}
	if parent, err = strconv.Atoi(f[1]); err != nil {
		return 0, 0, err
	}
	if start, err = strconv.ParseUint(f[19], 10, 64); err != nil {
		return 0, 0, err
	}
	return parent, start, nil
}
