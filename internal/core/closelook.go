package core

import (
	"slices"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func init() { registerRequests(LookBeforeClose{}) }

// LookBeforeClose looks at what closing tab T would lose, for the close
// dialog: told as a CloseInfoMsg.
type LookBeforeClose struct{ T TabRef }

// CloseInfoMsg is what git and GitHub say of tab T's worktrees before it
// is closed: the repositories with uncommitted changes (Dirty) and with
// commits on no remote (Unpushed); each worktree's head (Heads) and, when
// its pull request is merged with that head, MergedHeads; both by the
// worktree's path.
type CloseInfoMsg struct {
	T                  TabRef
	Dirty, Unpushed    []string
	MergedHeads, Heads map[string]string
}

func (q LookBeforeClose) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	wts := slices.Clone(t.Worktrees)
	return Later(func() Event {
		heads := make(map[string]string)
		for _, w := range wts {
			if h := workspace.Head(w); h != "" {
				heads[w.Path] = h
			}
		}
		return CloseInfoMsg{T: q.T, Dirty: workspace.Dirty(wts), Unpushed: workspace.Unpushed(wts),
			MergedHeads: mergedBranchHeads(wts), Heads: heads}
	})
}

// A merged PR's head stays identifiable after squash/rebase merges, when
// Git ancestry alone cannot prove that the local branch's work is safe.
func mergedBranchHeads(wts []workspace.Worktree) map[string]string {
	heads := make(map[string]string)
	for _, w := range wts {
		if w.Branch == "" || git.CurrentBranch(w.Path) != w.Branch {
			continue
		}
		pr, ok := github.BranchStatus(w.Path)
		if ok && pr.State == "MERGED" && pr.HeadOID != "" && workspace.Head(w) == pr.HeadOID {
			heads[w.Path] = pr.HeadOID
		}
	}
	return heads
}
