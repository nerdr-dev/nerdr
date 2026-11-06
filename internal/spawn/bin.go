package spawn

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Bin is the nerdr the agents' hooks and skills run, as NERDR_BIN, and
// that starts a plugin's program: the path nerdr was started by, when
// that is this executable, else the executable itself. os.Executable
// follows links on Linux, to where Homebrew keeps one version of nerdr,
// and an update removes that file; the link nerdr was started through is
// moved to the new one. An agent that runs on through an update keeps
// what it was given, so its hooks still find a nerdr.
var Bin = sync.OnceValues(func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return startedAs(os.Args[0], exe), nil
})

// startedAs is arg0, the name nerdr was started by, as a path of its
// own, when that is the file exe; exe otherwise.
func startedAs(arg0, exe string) string {
	path := arg0
	if !strings.ContainsRune(arg0, filepath.Separator) {
		p, err := exec.LookPath(arg0)
		if err != nil {
			return exe
		}
		path = p
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return exe
	}
	a, err := os.Stat(abs)
	if err != nil {
		return exe
	}
	b, err := os.Stat(exe)
	if err != nil || !os.SameFile(a, b) {
		return exe
	}
	return abs
}
