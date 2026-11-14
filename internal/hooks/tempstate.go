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

// lockState locks the sidecar of file, file.lock, made when it is not
// there, until the file it returns is closed. ok is false when it cannot:
// the sidecar is a link, or not this user's.
//
// ForgetAgent removes the sidecar, and a hook of a program not yet
// stopped may run meanwhile. One that had opened the sidecar before it
// was removed would hold a lock nobody else can find, beside one that
// made it anew: so the lock taken counts only while it is still the file
// at the path, and is taken again otherwise.
func lockState(file string) (lock *os.File, ok bool) {
	path := file + ".lock"
	for range 64 {
		lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return nil, false
		}
		if !ownFile(lock) || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX) != nil {
			lock.Close()
			return nil, false
		}
		held, herr := lock.Stat()
		at, aerr := os.Lstat(path)
		if herr == nil && aerr == nil && os.SameFile(held, at) {
			return lock, true
		}
		lock.Close() // removed, or replaced, while it was waited for
	}
	return nil, false
}

// ForgetAgent removes what the hooks of the agent that reported as pane
// kept in the temporary folder, once the agent is gone for good. It is
// done under the hooks' own lock, which is removed while held (see
// lockState): a hook of a program still stopping, of an agent put to
// sleep a moment before, say, goes on as before, and at worst leaves its
// file there again. A file an agent in a sandbox kept in a temporary
// folder of its own is not found, and is left.
func ForgetAgent(pane string) {
	if pane == "" {
		return
	}
	for _, kind := range subagentKinds {
		file := subagentsFile(kind, pane)
		_, ferr := os.Lstat(file)
		_, lerr := os.Lstat(file + ".lock")
		if ferr != nil && lerr != nil {
			continue // nothing kept: no lock made to remove it
		}
		lock, ok := lockState(file)
		if !ok {
			continue
		}
		_ = os.Remove(file)
		_ = os.Remove(file + ".lock")
		lock.Close()
	}
}
