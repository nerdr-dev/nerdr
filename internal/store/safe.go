package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/safefile"
)

// The data folder is where agents write, and nerdr writes there too, as
// the user and outside any sandbox an agent is in: a review's result, a
// walkthrough's page, a handoff. A file there that an agent made a link —
// reviews.jsonl to ~/.bashrc — would have nerdr write wherever it points,
// what an agent wrote. So nerdr opens what it writes there from the folder
// above the data folder, which no agent can replace (the git directory,
// the worktree, the data_dir set), without leaving it, and never through
// a link at the file itself.

// anchors maps each data folder Dir has given to the folder above it that
// it is opened from.
var anchors sync.Map

func remember(dir, anchor string) {
	anchors.Store(filepath.Clean(dir), filepath.Clean(anchor))
}

// anchorOf splits path into the folder it is opened from and the rest: the
// anchor of the data folder it is in, or, for a path in none Dir gave,
// its own folder.
func anchorOf(path string) (anchor, rel string) {
	path = filepath.Clean(path)
	best := ""
	anchors.Range(func(k, v any) bool {
		d := k.(string)
		if (path == d || strings.HasPrefix(path, d+string(filepath.Separator))) && len(d) > len(best) {
			best, anchor = d, v.(string)
		}
		return true
	})
	if best != "" {
		if r, err := filepath.Rel(anchor, path); err == nil && filepath.IsLocal(r) {
			return anchor, r
		}
	}
	return filepath.Dir(path), filepath.Base(path)
}

// errLink is what writing through a link, or to what is not a file, is
// refused with.
func errLink(path string) error {
	return fmt.Errorf("%s is not a regular file: nerdr does not write through links", path)
}

// openFile opens path, which is in the data folder, as os.OpenFile would,
// but neither through a link nor out of the folder it is opened from. The
// folders on the way are made when flag creates the file.
func openFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	anchor, rel := anchorOf(path)
	if flag&os.O_CREATE != 0 {
		if err := denied.Err(os.MkdirAll(anchor, 0o755)); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := noLinks(root, rel, path); err != nil {
		return nil, err
	}
	if flag&os.O_CREATE != 0 {
		if err := denied.Err(root.MkdirAll(filepath.Dir(rel), 0o755)); err != nil {
			return nil, err
		}
		if err := noLinks(root, rel, path); err != nil {
			return nil, err
		}
	}
	for range 3 {
		info, err := root.Lstat(rel)
		switch {
		case err == nil && !info.Mode().IsRegular():
			return nil, errLink(path)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return nil, err
		case err != nil && flag&os.O_CREATE == 0:
			return nil, err
		}
		f, oerr := root.OpenFile(rel, openFlag(flag, err != nil)|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, perm)
		if errors.Is(oerr, fs.ErrExist) {
			continue // made meanwhile: looked at again
		}
		if denied.Err(oerr) != nil {
			return nil, oerr
		}
		// The file looked at is the one opened, not a link, a pipe or a
		// device put in its place since: O_NONBLOCK keeps the open of a
		// pipe from waiting, and this refuses it.
		st, serr := f.Stat()
		if serr != nil || !st.Mode().IsRegular() || (err == nil && !os.SameFile(st, info)) {
			f.Close()
			return nil, errLink(path)
		}
		return f, nil
	}
	return nil, errLink(path)
}

// openFlag is flag for a file that is there, or one to be made: that is
// made only if nothing, a link included, has its name by then.
func openFlag(flag int, create bool) int {
	if create {
		return flag | os.O_EXCL
	}
	return flag &^ os.O_CREATE
}

// WriteFile writes data to path, in the data folder, as os.WriteFile
// would, by putting a new file in its place: a link there is replaced, not
// followed, and the folders on the way do not lead out of the data folder.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	anchor, rel := anchorOf(path)
	if err := denied.Err(os.MkdirAll(anchor, 0o755)); err != nil {
		return err
	}
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := noLinks(root, rel, path); err != nil {
		return err
	}
	if err := denied.Err(root.MkdirAll(filepath.Dir(rel), 0o755)); err != nil {
		return err
	}
	if err := noLinks(root, rel, path); err != nil {
		return err
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	tmp := filepath.Join(filepath.Dir(rel), "."+filepath.Base(rel)+"-"+hex.EncodeToString(b[:]))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if denied.Err(err) != nil {
		return err
	}
	defer root.Remove(tmp) // gone already once renamed
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// A folder on the way made a link since is not renamed through.
	if err := noLinks(root, rel, path); err != nil {
		return err
	}
	return denied.Err(root.Rename(tmp, rel))
}

// noLinks refuses rel, inside root, when a folder on the way to it is a
// link. os.Root keeps to the anchor but follows a link inside it: in the
// git directory an agent could make .git/nerdr a link to hooks, and have
// nerdr write a file there that git would run as the user.
func noLinks(root *os.Root, rel, path string) error {
	if err := safefile.CheckNoLinks(root, rel); err != nil {
		return fmt.Errorf("%s: %w: nerdr does not write through links", path, err)
	}
	return nil
}
