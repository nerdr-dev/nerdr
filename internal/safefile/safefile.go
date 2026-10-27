// Package safefile reads and writes nerdr's managed files (skills, hooks
// and plugins) without following symbolic links inside a worktree,
// blocking on named pipes, or overwriting hardlinked files in place.
package safefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// maxFileBytes bounds what ReadRegular reads from a worktree file so a
// large or growing file cannot exhaust memory during synchronous startup.
const maxFileBytes = 1 << 20

// OpenDir opens an os.Root for the directory dir. When worktree is true,
// the root is anchored above dot (.copilot, .claude, .agents) so a
// sandboxed agent in that worktree cannot replace dot with a symbolic link,
// regardless of where the worktree sits on disk. When worktree is false
// (the user's home or state folder), dotfile symlinks (stow, chezmoi) on
// the directory path are followed.
func OpenDir(dir, dot string, worktree, create bool) (root *os.Root, base string, err error) {
	if !worktree {
		if create {
			if err := denied.Err(os.MkdirAll(dir, 0o755)); err != nil {
				return nil, "", err
			}
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			if !create && os.IsNotExist(err) {
				return nil, "", err
			}
			real = dir
		}
		r, err := os.OpenRoot(real)
		return r, ".", err
	}
	anchor, rel := splitRoot(dir, dot)
	if create {
		if err := denied.Err(os.MkdirAll(anchor, 0o755)); err != nil {
			return nil, "", err
		}
	}
	r, err := os.OpenRoot(anchor)
	return r, rel, err
}

// OpenFileRoot opens an os.Root for reading and writing file. When worktree
// is true, the root is anchored above dot (.opencode, .agents). When
// worktree is false, dotfile symlinks in the user's home folder are
// followed.
func OpenFileRoot(file, dot string, worktree bool) (root *os.Root, rel string, err error) {
	if !worktree {
		file = resolveHomeFile(file)
		dir := filepath.Dir(file)
		if err := denied.Err(os.MkdirAll(dir, 0o755)); err != nil {
			return nil, "", err
		}
		real := dir
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			real = resolved
		}
		rt, err := os.OpenRoot(real)
		return rt, filepath.Base(file), err
	}
	anchor, r := splitRoot(file, dot)
	if err := denied.Err(os.MkdirAll(anchor, 0o755)); err != nil {
		return nil, "", err
	}
	rt, err := os.OpenRoot(anchor)
	return rt, r, err
}

func resolveHomeFile(file string) string {
	if resolved, err := filepath.EvalSymlinks(file); err == nil {
		return resolved
	}
	if target, err := os.Readlink(file); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(file), target)
		}
		return filepath.Clean(target)
	}
	return file
}

// OpenSkillsRoot opens an os.Root for a skills folder dir. When worktree is
// true, the root is anchored at the worktree root above .claude/skills or
// .agents/skills. When worktree is false, dotfile symlinks in the user's
// home folder are followed.
func OpenSkillsRoot(dir string, worktree, create bool) (root *os.Root, base string, err error) {
	dir = filepath.Clean(dir)
	if worktree {
		anchor := filepath.Dir(dir)
		parent := filepath.Dir(dir)
		if filepath.Base(dir) == "skills" && strings.HasPrefix(filepath.Base(parent), ".") && filepath.Dir(parent) != parent {
			anchor = filepath.Dir(parent)
		}
		if create {
			if err := os.MkdirAll(anchor, 0o755); err != nil {
				return nil, "", err
			}
		}
		rel, _ := filepath.Rel(anchor, dir)
		r, err := os.OpenRoot(anchor)
		return r, rel, err
	}
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, "", err
		}
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if !create && os.IsNotExist(err) {
			return nil, "", err
		}
		real = dir
	}
	r, err := os.OpenRoot(real)
	return r, ".", err
}

func splitRoot(path, dot string) (anchor, rel string) {
	path = filepath.Clean(path)
	for d := path; filepath.Dir(d) != d; d = filepath.Dir(d) {
		if filepath.Base(d) == dot {
			anchor = filepath.Dir(d)
			rel, _ = filepath.Rel(anchor, path)
			return anchor, rel
		}
	}
	return filepath.Dir(path), filepath.Base(path)
}

// CheckNoLinks verifies that no parent component of rel inside root is a
// symbolic link.
func CheckNoLinks(root *os.Root, rel string) error {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	cur := ""
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "." || parts[i] == "" {
			continue
		}
		if cur == "" {
			cur = parts[i]
		} else {
			cur = filepath.Join(cur, parts[i])
		}
		if info, err := root.Lstat(cur); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link", cur)
		}
	}
	return nil
}

// ReadRegular reads rel inside root after verifying that no parent
// component is a symbolic link and the opened file descriptor is a regular
// file of at most maxFileBytes. O_NONBLOCK prevents blocking if rel is
// replaced with a FIFO.
func ReadRegular(root *os.Root, rel string) ([]byte, error) {
	if err := CheckNoLinks(root, rel); err != nil {
		return nil, err
	}
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || !os.SameFile(info, st) {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if st.Size() > maxFileBytes {
		return nil, fmt.Errorf("%s is too large (%d bytes)", rel, st.Size())
	}
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFileBytes {
		return nil, fmt.Errorf("%s is too large", rel)
	}
	return b, nil
}

// WriteRegular writes data to a new temporary file in rel's directory
// inside root and atomically renames it onto rel, avoiding in-place
// O_TRUNC races, FIFO blocking, and hardlink overwrites. If rel already
// exists as a regular file, its permission bits are preserved.
func WriteRegular(root *os.Root, rel string, data []byte, perm os.FileMode) error {
	if err := CheckNoLinks(root, rel); err != nil {
		return err
	}
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := denied.Err(root.MkdirAll(dir, 0o755)); err != nil {
			return err
		}
	}
	info, err := root.Lstat(rel)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return denied.Err(err)
	}
	var f *os.File
	var tmp string
	for i := 0; i < 8; i++ {
		var b [8]byte
		_, _ = rand.Read(b[:])
		name := ".nerdr-tmp-" + hex.EncodeToString(b[:])
		if dir != "." {
			tmp = filepath.Join(dir, name)
		} else {
			tmp = name
		}
		f, err = root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if denied.Err(err) != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		_ = root.Remove(tmp)
		return denied.Err(werr)
	}
	if cerr != nil {
		_ = root.Remove(tmp)
		return denied.Err(cerr)
	}
	if err := denied.Err(root.Rename(tmp, rel)); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}
