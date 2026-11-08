package spawn

import (
	"bytes"
	"os"
	"strconv"
)

// Args is the command line process pid was started with, or exec'd into.
func Args(pid int) ([]string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil, err
	}
	var args []string
	for _, a := range bytes.Split(bytes.TrimSuffix(b, []byte{0}), []byte{0}) {
		args = append(args, string(a))
	}
	return args, nil
}
