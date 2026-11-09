// Package reviewsource is what nerdr's review panel reads of the
// repositories it shows (Source): read where they are (Local), or asked of
// core over the client's connection (Asker), as the panel may run on
// another machine than they do.
package reviewsource

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"
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

	// Notes are worktree wt's why notes, with the findings of its latest
	// code review added (see internal/codereview).
	Notes(wt string) why.Index
	// Order is the reading order of branch in worktree wt.
	Order(wt, branch string) order.Index
	// Walkthrough is the walkthrough of branch in worktree wt, if there is
	// one (ok), and whether one of files, the change's, was edited after
	// it was written (older).
	Walkthrough(wt, branch string, files []string) (w walkthrough.Walkthrough, ok, older bool)
	// WriteWalkthrough draws the page of branch's walkthrough in worktree
	// wt, from the change as it is now, and gives where it was written;
	// ok is false when there is no walkthrough.
	WriteWalkthrough(wt, branch string) (path string, ok bool, err error)

	// LoadProject reads worktree wt's code anew, for Imports and
	// Relations, and gives its version: another once the code has changed.
	// It walks the whole tree and every source file: the slow part.
	LoadProject(wt string) (version int)
	// Imports is the import graph among paths in wt's code, as
	// LoadProject last read it (see analysis.Project.Imports); nil before.
	Imports(wt string, paths []string) map[string][]string
	// Relations are the symbols file in wt declares, and where each is
	// used in the others, from wt's code as LoadProject last read it.
	Relations(wt, file string) ([]analysis.Symbol, [][]analysis.Ref)
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

func (Local) Notes(wt string) why.Index {
	recs, read := codereview.Latest(wt)
	var extra []why.Note
	for _, rec := range recs {
		extra = append(extra, codereview.Notes(rec)...)
	}
	return why.Load(wt).With(extra, read)
}

func (Local) Order(wt, branch string) order.Index { return order.Load(wt, branch) }

func (Local) Walkthrough(wt, branch string, files []string) (walkthrough.Walkthrough, bool, bool) {
	w, ok := walkthrough.Load(wt, branch)
	return w, ok, ok && walkthrough.Older(wt, w, files)
}

func (Local) WriteWalkthrough(wt, branch string) (string, bool, error) {
	w, ok := walkthrough.Load(wt, branch)
	if !ok {
		return "", false, nil
	}
	path, err := walkthrough.Write(wt, w)
	return path, true, err
}

// projects are the worktrees' code as LoadProject last read it, the
// worktrees read last kept; see LoadProject.
var projects struct {
	sync.Mutex
	byWt map[string]*loaded
	used []string // the worktrees, the one read last at the end
	next int      // the version the next new read gets
}

// projectsKept is how many worktrees' code is kept: each holds the words
// of every source file.
const projectsKept = 4

// loaded is a worktree's code as read; mu is held while it is used, as a
// project fills in what it reads as it goes.
type loaded struct {
	mu      sync.Mutex
	p       *analysis.Project
	version int
}

func (Local) LoadProject(wt string) int {
	p := analysis.LoadProject(wt)
	p.Index()
	projects.Lock()
	defer projects.Unlock()
	if projects.byWt == nil {
		projects.byWt = map[string]*loaded{}
	}
	projects.used = append(slices.DeleteFunc(projects.used, func(w string) bool { return w == wt }), wt)
	for len(projects.used) > projectsKept {
		delete(projects.byWt, projects.used[0])
		projects.used = projects.used[1:]
	}
	if was := projects.byWt[wt]; was != nil && was.p.Same(p) {
		return was.version
	}
	projects.next++
	projects.byWt[wt] = &loaded{p: p, version: projects.next}
	return projects.next
}

// project is wt's code as LoadProject last read it, held for use (its mu
// to be let go), or nil.
func project(wt string) *loaded {
	projects.Lock()
	l := projects.byWt[wt]
	projects.Unlock()
	if l != nil {
		l.mu.Lock()
	}
	return l
}

func (Local) Imports(wt string, paths []string) map[string][]string {
	l := project(wt)
	if l == nil {
		return nil
	}
	defer l.mu.Unlock()
	if len(paths) == 0 {
		return nil
	}
	return l.p.Imports(paths)
}

func (Local) Relations(wt, file string) ([]analysis.Symbol, [][]analysis.Ref) {
	l := project(wt)
	if l == nil {
		return nil, nil
	}
	defer l.mu.Unlock()
	p := l.p
	syms := p.Symbols(file)
	refs := make([][]analysis.Ref, len(syms))
	for i, s := range syms {
		refs[i] = p.References(s.Name, file)
	}
	return syms, refs
}
