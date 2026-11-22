package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

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

// openParentDir walks from anchor to rel's parent folder one component at a
// time with O_DIRECTORY|O_NOFOLLOW, returning the pinned directory
// descriptor and rel's base name. Holding the parent descriptor closes the
// TOCTOU window where an agent replaces a parent folder with a symbolic link
// (such as .git/nerdr -> hooks) between a link check and the open or rename.
func openParentDir(anchor, rel, path string, create bool) (dirFD int, base string, err error) {
	rel = filepath.Clean(rel)
	if !filepath.IsLocal(rel) || rel == "." {
		return -1, "", errLink(path)
	}
	if create {
		if err := denied.Err(os.MkdirAll(anchor, 0o755)); err != nil {
			return -1, "", err
		}
	}
	fd, err := unix.Open(anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", denied.Err(&os.PathError{Op: "open", Path: anchor, Err: err})
	}
	dir := filepath.Dir(rel)
	if dir != "." {
		for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
			if part == "" || part == "." {
				continue
			}
			var next int
			for range 2 {
				next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if err == nil {
					break
				}
				if create && errors.Is(err, unix.ENOENT) {
					merr := unix.Mkdirat(fd, part, 0o755)
					if merr == nil || errors.Is(merr, unix.EEXIST) {
						continue
					}
					_ = unix.Close(fd)
					return -1, "", denied.Err(&os.PathError{Op: "mkdir", Path: path, Err: merr})
				}
				break
			}
			_ = unix.Close(fd)
			if err != nil {
				if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
					return -1, "", errLink(path)
				}
				return -1, "", denied.Err(&os.PathError{Op: "open", Path: path, Err: err})
			}
			fd = next
		}
	}
	return fd, filepath.Base(rel), nil
}

// openFile opens path, which is in the data folder, as os.OpenFile would,
// but neither through a link nor out of the folder it is opened from. The
// folders on the way are made when flag creates the file.
func openFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	anchor, rel := anchorOf(path)
	dirFD, base, err := openParentDir(anchor, rel, path, flag&os.O_CREATE != 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	for range 3 {
		var info unix.Stat_t
		err := unix.Fstatat(dirFD, base, &info, unix.AT_SYMLINK_NOFOLLOW)
		switch {
		case err == nil && info.Mode&unix.S_IFMT != unix.S_IFREG:
			return nil, errLink(path)
		case err != nil && !errors.Is(err, unix.ENOENT):
			return nil, denied.Err(&os.PathError{Op: "lstat", Path: path, Err: err})
		case err != nil && flag&os.O_CREATE == 0:
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		ofd, oerr := unix.Openat(dirFD, base, openFlag(flag, err != nil)|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm))
		if errors.Is(oerr, unix.EEXIST) {
			continue // made meanwhile: looked at again
		}
		if errors.Is(oerr, unix.ELOOP) {
			return nil, errLink(path)
		}
		if oerr != nil {
			return nil, denied.Err(&os.PathError{Op: "open", Path: path, Err: oerr})
		}
		// The file looked at is the one opened, not a link, a pipe or a
		// device put in its place since: O_NONBLOCK keeps the open of a
		// pipe from waiting, and this refuses it.
		var st unix.Stat_t
		if serr := unix.Fstat(ofd, &st); serr != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || (err == nil && (st.Dev != info.Dev || st.Ino != info.Ino)) {
			_ = unix.Close(ofd)
			return nil, errLink(path)
		}
		return os.NewFile(uintptr(ofd), path), nil
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
	dirFD, base, err := openParentDir(anchor, rel, path, true)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	var b [8]byte
	_, _ = rand.Read(b[:])
	tmp := "." + base + "-" + hex.EncodeToString(b[:])
	tfd, err := unix.Openat(dirFD, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm))
	if err != nil {
		return denied.Err(&os.PathError{Op: "open", Path: path, Err: err})
	}
	f := os.NewFile(uintptr(tfd), filepath.Join(filepath.Dir(path), tmp))
	renamed := false
	defer func() {
		if !renamed {
			_ = unix.Unlinkat(dirFD, tmp, 0)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(dirFD, tmp, dirFD, base); err != nil {
		return denied.Err(&os.PathError{Op: "rename", Path: path, Err: err})
	}
	renamed = true
	return nil
}
