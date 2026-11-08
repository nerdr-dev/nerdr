package spawn

import (
	"bytes"
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// Args is the command line process pid was started with, or exec'd into:
// kern.procargs2 has argc, the path executed, padding of NULs, and then
// the arguments, each ended by a NUL.
func Args(pid int) ([]string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	if len(b) < 4 {
		return nil, errors.New("short procargs2")
	}
	argc := int(binary.LittleEndian.Uint32(b))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return nil, errors.New("no path in procargs2")
	}
	b = bytes.TrimLeft(b[i:], "\x00")
	var args []string
	for len(args) < argc && len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			i = len(b)
		}
		args = append(args, string(b[:i]))
		b = b[min(i+1, len(b)):]
	}
	return args, nil
}
