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
	"path/filepath"

	"github.com/nerdr-dev/nerdr/internal/rundir"
)

// EnvDir, when set, is the folder of the client sockets, for tests that
// must not meet a real core.
const EnvDir = rundir.EnvDir

// Dir is the folder of the client sockets; see rundir.Dir.
func Dir() (string, error) { return rundir.Dir() }

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

// ensureDir and checkDir are rundir's Ensure and Check.
var (
	ensureDir = rundir.Ensure
	checkDir  = rundir.Check
)
