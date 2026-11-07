// Package attach is the client socket: how a client (the TUI, later a GUI)
// reaches the core that runs the tabs of a folder, and proves it may.
//
// The socket can do anything the user can, approvals included, so it is
// guarded three ways, none enough alone:
//
//   - It lives in a folder of mode 0700 outside ~/.local/state/nerdr, whose
//     sockets the sandbox docs tell sandboxes to allow (see Dir). A sandbox
//     must deny this folder.
//   - A client must send the key in a file beside the socket, made anew each
//     time a core starts (see Listen). A sandbox that may connect to sockets
//     but not read files is stopped by it.
//   - The core refuses a connection from a process it runs, or one it cannot
//     place, by the kernel's word of who connected (see Config.Check). A
//     process that has left the tree, by forking twice, gets past this.
//
// One client is connected at a time: a new one takes over, and the one
// before is told (see Server). Frames are a type byte, a length and a
// body (see frame.go), over any stream, so the same protocol runs over ssh.
package attach

import (
	"crypto/sha256"
	"encoding/hex"
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

// Paths are the socket of the core of folder root, and its key file.
func Paths(root string) (sock, key string, err error) {
	dir, err := Dir()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(root))
	name := "core-" + hex.EncodeToString(sum[:8])
	return filepath.Join(dir, name+".sock"), filepath.Join(dir, name+".key"), nil
}

// ensureDir makes dir if it is not there, and checks it is a folder of
// this user's that nobody else can enter, tightening it when it is ours
// but open. It does not follow a link put in its place.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return checkDir(dir, true)
}

// checkDir is ensureDir's check; fix says to tighten a folder of ours.
func checkDir(dir string, fix bool) error {
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
