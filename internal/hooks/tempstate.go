package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The hooks of an agent keep a little between them in files in the
// temporary folder: they run in the agent's sandbox, which may let them
// write nowhere else. On Linux that folder is /tmp, which every user and
// every other agent can list and write in. So a file's name gives nothing
// away, and what is there is read, and replaced, only as a plain file of
// this user's: never through a link another left in its place.

// maxTempState is the most of such a file read.
const maxTempState = 1 << 20

// paneName stands for a tab's secret (NERDR_TAB) in a file's name: the
// secret is all that keeps one agent in a sandbox from reporting as
// another, and a name in /tmp is there for all to read.
func paneName(pane string) string {
	sum := sha256.Sum256([]byte(pane))
	return hex.EncodeToString(sum[:8])
}

// readOwn is what is in file, when it is a plain file of this user's;
// nothing otherwise.
func readOwn(file string) []byte {
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	if !ownFile(f) {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTempState))
	if err != nil {
		return nil
	}
	return b
}

// ownFile reports whether f is a plain file of this user's.
func ownFile(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// writeWhole puts b in file, whole or not at all, for another hook reading
// it meanwhile: as a new file of a name nobody can have guessed, put in
// its place, so that a link left at either is not written through.
func writeWhole(file string, b []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name()) // gone already once renamed
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil && cerr == nil {
		_ = os.Rename(tmp.Name(), file)
	}
}

// ForgetAgent removes what the hooks of the agent that reported as pane
// kept in the temporary folder, once the agent is gone for good. A file an
// agent in a sandbox kept in a temporary folder of its own is not found,
// and is left.
func ForgetAgent(pane string) {
	if pane == "" {
		return
	}
	for _, kind := range subagentKinds {
		file := subagentsFile(kind, pane)
		_ = os.Remove(file)
		_ = os.Remove(file + ".lock")
	}
}
