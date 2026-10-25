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

	"github.com/nerdr-dev/nerdr/internal/denied"
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
	if flag&os.O_CREATE != 0 {
		if err := denied.Err(root.MkdirAll(filepath.Dir(rel), 0o755)); err != nil {
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
		f, oerr := root.OpenFile(rel, openFlag(flag, err != nil), perm)
		if errors.Is(oerr, fs.ErrExist) {
			continue // made meanwhile: looked at again
		}
		if denied.Err(oerr) != nil {
			return nil, oerr
		}
		if err == nil {
			// The file looked at is the one opened, not a link put in its
			// place since.
			st, serr := f.Stat()
			if serr != nil || !os.SameFile(st, info) {
				f.Close()
				return nil, errLink(path)
			}
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
	if err := denied.Err(root.MkdirAll(filepath.Dir(rel), 0o755)); err != nil {
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
	return denied.Err(root.Rename(tmp, rel))
}
