package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// WorkTree is the id of the tree git would commit if everything in the
// worktree were added, new files too: the same work gives the same id,
// committed or not. The worktree's own index is left alone; a copy of it
// is used, so files git already knows are not read again.
func WorkTree(worktreePath string) (string, error) {
	out, err := proc.Output(gitsafe.Command(worktreePath, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "nerdr-index-")
	if denied.Err(err) != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	data, readErr := os.ReadFile(strings.TrimSpace(string(out)))
	_, writeErr := tmp.Write(data)
	if err := tmp.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return "", writeErr
	}
	if readErr != nil || len(data) == 0 {
		// No index yet: git makes one, but will not read an empty file.
		_ = os.Remove(name)
	}
	index := "GIT_INDEX_FILE=" + filepath.Clean(name)
	if err := AddAll(context.Background(), worktreePath, index); err != nil {
		return "", err
	}
	write := gitsafe.Command(worktreePath, "write-tree")
	write.Env = append(write.Env, index)
	tree, err := proc.Output(write)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tree)), nil
}

// TreeOf is the id of the tree of commit ref, or "" when there is none.
func TreeOf(worktreePath, ref string) string {
	out, err := proc.Output(gitsafe.Command(worktreePath, "rev-parse", "--verify", "-q", ref+"^{tree}"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// AddAll is `git add -A` of the whole worktree, with env added to git's
// (GIT_INDEX_FILE, for one), but for the submodules: each gitlink in the
// index is left as it is.
//
// To learn whether a gitlink's folder has changes of its own, git add runs
// `git status` inside it, with that folder's own .git and config. An agent
// can make any folder of its worktree a repository of its own, with a
// filter driver for every file in its config, and put it in the index as
// a gitlink; the status would run the filter, as the user. git add has no
// option to skip that, nor heeds diff.ignoreSubmodules, so the gitlinks are
// kept out with exclude pathspecs. They are read from the same index file
// the add then works on, so env should name a copy of the index nerdr made
// (as WorkTree and StageAll do): one the agent could change in between
// could gain a gitlink after it was read. Listing the index runs nothing
// in the gitlinks' folders.
//
// A submodule moved to another commit is therefore not staged; nor is one
// removed from the work tree. A folder with a .git of its own that is not
// yet in the index is added as a gitlink, which runs nothing in it either.
func AddAll(ctx context.Context, worktreePath string, env ...string) error {
	ls := gitsafe.CommandContext(ctx, worktreePath, "ls-files", "--stage", "--full-name", "-z")
	ls.Env = append(ls.Env, env...)
	var stderr bytes.Buffer
	ls.Stderr = &stderr
	out, err := proc.Output(ls)
	if err != nil {
		return gitError("ls-files", &stderr, err)
	}
	var spec bytes.Buffer
	spec.WriteString(":/\x00")
	for _, entry := range strings.Split(string(out), "\x00") {
		// "<mode> <object> <stage>\t<path>"
		meta, path, ok := strings.Cut(entry, "\t")
		if ok && strings.HasPrefix(meta, "160000 ") {
			spec.WriteString(":(exclude,top,literal)" + path + "\x00")
		}
	}
	// From stdin rather than as arguments: a repository may have more
	// submodules than a command line holds.
	add := gitsafe.CommandContext(ctx, worktreePath, "add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul")
	add.Env = append(add.Env, env...)
	add.Stdin = &spec
	stderr.Reset()
	add.Stderr = &stderr
	if err := proc.Run(add); err != nil {
		return gitError("add", &stderr, err)
	}
	return nil
}

// gitError is err of git's subcommand op, with the last line git wrote to
// stderr: why it failed, which "exit status 128" does not say.
func gitError(op string, stderr *bytes.Buffer, err error) error {
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return errors.New("git " + op + ": " + last)
	}
	return errors.New("git " + op + ": " + err.Error())
}

// StageAll stages every change in the worktree, new files too, in its own
// index, as `git add -A` would, by way of AddAll. The add is made on a
// copy of the index in a folder of nerdr's, so the gitlinks left out are
// those of the index added to, and the copy then replaces the worktree's
// index the way git replaces it: written to index.lock, which fails if
// another git holds it, and renamed over.
func StageAll(ctx context.Context, worktreePath string) error {
	out, err := proc.Output(gitsafe.CommandContext(ctx, worktreePath, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	if err != nil {
		return err
	}
	real := strings.TrimSpace(string(out))
	tmp, err := os.MkdirTemp("", "nerdr-index-")
	if denied.Err(err) != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	copied := filepath.Join(tmp, "index")
	if data, err := os.ReadFile(real); err == nil && len(data) > 0 {
		if err := os.WriteFile(copied, data, 0o600); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := AddAll(ctx, worktreePath, "GIT_INDEX_FILE="+copied); err != nil {
		return err
	}
	data, err := os.ReadFile(copied)
	if err != nil {
		return err
	}
	lock := real + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("the index is locked by another git (" + lock + ")")
		}
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(lock, real)
	}
	if err != nil {
		os.Remove(lock)
	}
	return err
}
