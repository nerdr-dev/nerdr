// Package reviewsource is what nerdr's review panel reads of the
// repositories it shows (Source): read where they are (Local), or asked of
// core over the client's connection (Asker), as the panel may run on
// another machine than they do.
package reviewsource

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
)

// Source is all the review panel reads of the repositories it shows: git,
// gh and the worktrees' files. The panel may run on another machine than
// they do, so it reads them through a Source and nothing else, which
// review's TestReviewReadsThroughSource holds it to. Its methods may be
// called from any goroutine.
type Source interface {
	// ListWorktrees lists repo's worktrees.
	ListWorktrees(repo string) ([]git.Worktree, error)
	// DetectBase is the branch worktree wt's branch left, or "".
	DetectBase(wt string) string

	// Files lists c's changed files, and on success the lines added and
	// removed in each.
	Files(c git.Comparison) ([]git.FileStatus, map[string]git.LineCount, error)
	// Diff is the unified diff of file's change in c; original is the
	// path it had before a rename, or file.
	Diff(c git.Comparison, file string, untracked bool, original string) (string, error)
	// OldFile is file as c's change found it, formatted for the diff pane.
	OldFile(c git.Comparison, file string) (string, error)
	// OldSource is file's content as c's change found it.
	OldSource(c git.Comparison, file string) (string, error)
	// FullNewFile is file in worktree wt whole, with diff's changes marked.
	FullNewFile(wt, file, diff string) (string, error)
	// ReadFile is file's content in worktree wt.
	ReadFile(wt, file string) (string, error)
	// Summary counts worktree wt's uncommitted changes: the files, and the
	// lines added and removed.
	Summary(wt string) (files, add, del int)
	// Stamps is a stamp of each of paths in worktree wt that changes when
	// the file does; a file that cannot be read has none.
	Stamps(wt string, paths []string) map[string]string

	// GhAvailable reports whether the gh CLI is installed.
	GhAvailable() bool
	// CurrentPR is the pull request for worktree wt's branch; see
	// github.CurrentPR.
	CurrentPR(wt string) (pr github.PR, ok bool, msg string)
	// FailedRunLogs are the logs of the failed jobs behind checks; see
	// github.FailedRunLogs.
	FailedRunLogs(wt string, checks []github.Check, limit int) (string, error)
}

// Local is the Source that reads the repositories on this machine.
type Local struct{}

func (Local) ListWorktrees(repo string) ([]git.Worktree, error) { return git.ListWorktrees(repo) }

func (Local) DetectBase(wt string) string { return git.DetectBase(wt) }

func (Local) Files(c git.Comparison) ([]git.FileStatus, map[string]git.LineCount, error) {
	files, err := c.Files()
	if err != nil {
		return files, nil, err
	}
	return files, c.NumStat(), nil
}

func (Local) Diff(c git.Comparison, file string, untracked bool, original string) (string, error) {
	return c.Diff(file, untracked, original)
}

func (Local) OldFile(c git.Comparison, file string) (string, error) { return c.OldFile(file) }

func (Local) OldSource(c git.Comparison, file string) (string, error) { return c.OldSource(file) }

func (Local) FullNewFile(wt, file, diff string) (string, error) {
	return git.FullNewFileWithMarkers(wt, file, diff)
}

func (Local) ReadFile(wt, file string) (string, error) { return git.ReadWorktreeFile(wt, file) }

func (Local) Summary(wt string) (files, add, del int) {
	if fs, err := git.ChangedFiles(wt); err == nil {
		files = len(fs)
	}
	_, add, del = git.ShortStat(wt)
	return files, add, del
}

func (Local) Stamps(wt string, paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		if fi, err := os.Stat(filepath.Join(wt, filepath.FromSlash(p))); err == nil {
			out[p] = fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return out
}

func (Local) GhAvailable() bool { return github.Available() }

func (Local) CurrentPR(wt string) (github.PR, bool, string) { return github.CurrentPR(wt) }

func (Local) FailedRunLogs(wt string, checks []github.Check, limit int) (string, error) {
	return github.FailedRunLogs(wt, checks, limit)
}
