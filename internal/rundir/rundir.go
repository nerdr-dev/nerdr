// Package rundir is the folder of nerdr's client sockets, and of what
// else a sandbox must not reach: a folder of mode 0700 outside
// ~/.local/state/nerdr, whose sockets the sandbox docs tell sandboxes to
// allow. A sandbox must deny it.
package rundir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// EnvDir, when set, is the folder of the client sockets, for tests that
// must not meet a real core.
const EnvDir = "NERDR_CLIENT_DIR"

// Dir is the folder of the client sockets: nerdr in $XDG_RUNTIME_DIR;
// without it ~/Library/Application Support/nerdr/run on macOS and
// ~/.cache/nerdr/run elsewhere. Not the temporary folder, which sandboxes
// tend to allow.
func Dir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "nerdr"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "nerdr", "run"), nil
	}
	return filepath.Join(home, ".cache", "nerdr", "run"), nil
}

// Ensure makes dir if it is not there, and checks it is a folder of
// this user's that nobody else can enter, tightening it when it is ours
// but open. It does not follow a link put in its place.
func Ensure(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return Check(dir, true)
}

// Check is Ensure's check; fix says to tighten a folder of ours.
func Check(dir string, fix bool) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot tell who owns " + dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if !fix {
			return fmt.Errorf("%s is open to other users (mode %o)", dir, fi.Mode().Perm())
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
