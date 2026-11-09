package config

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// The config file is read, edited and written whole, by a core and by a
// client of it on the same machine, in two processes, and by several
// goroutines of each: each edit has the file to itself, by a mutex in the
// process and a lock on a file beside it, held across processes.

var (
	fileMu sync.Mutex
	reads  uint64 // the reads numbered so far, under fileMu
)

// lockName is the file beside the config file that is locked.
const lockName = ".config.lock"

// locked runs fn with the config file to itself.
func locked(fn func() error) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	dir := filepath.Dir(Path())
	if err := denied.Err(os.MkdirAll(dir, 0o755)); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return denied.Err(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

// Read is what LoadNumbered says of its read: its number, and when the
// file read was last changed, the zero time for none.
type Read struct {
	N   uint64
	Mod time.Time
}

// read is Load, numbered. fileMu is held.
func read() (Config, Read, error) {
	var r Read
	if st, err := os.Stat(Path()); err == nil {
		r.Mod = st.ModTime()
	}
	c, err := Load()
	reads++
	r.N = reads
	return c, r, err
}

// EditAndLoad is SetKeys of edits and then Load of the file as it was
// written, with nothing between; see LoadNumbered.
func EditAndLoad(edits ...Edit) (Config, Read, error) {
	var c Config
	var r Read
	err := locked(func() error {
		if err := editUnlocked(func(text string) string { return setKeys(text, edits) }); err != nil {
			return err
		}
		var err error
		c, r, err = read()
		return err
	})
	return c, r, err
}

// LoadNumbered is Load, with the number of the read, and when the file it
// read was changed: a read of a greater number, in this process, read the
// file as it was later. Where the file cannot be had to itself, as where
// its folder cannot be written, it is read all the same.
func LoadNumbered() (Config, Read, error) {
	var c Config
	var r Read
	var err error
	if locked(func() error { c, r, err = read(); return nil }) != nil {
		fileMu.Lock()
		c, r, err = read()
		fileMu.Unlock()
	}
	return c, r, err
}
