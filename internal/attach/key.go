package attach

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// keyLen is how many random bytes a key holds; it is written in hex.
const keyLen = 32

// writeKey makes a new key and puts it in path, mode 0600, whole or not
// at all: a client never reads half of one.
func writeKey(path string) (string, error) {
	var b [keyLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b[:])
	f, err := os.CreateTemp(filepath.Dir(path), ".key-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, werr := f.WriteString(key + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp, 0o600)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return key, nil
}

// readKey is the key in path, when it is a file of this user's that
// nobody else can read, in a folder as Dir must be.
func readKey(path string) (string, error) {
	if err := checkDir(filepath.Dir(path), false); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a file", path)
	case !ok || int(st.Uid) != os.Getuid():
		return "", fmt.Errorf("%s belongs to another user", path)
	case fi.Mode().Perm()&0o077 != 0:
		return "", fmt.Errorf("%s is open to other users (mode %o)", path, fi.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, 4*keyLen))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// sameKey reports whether got is want, taking as long whatever got is.
func sameKey(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
