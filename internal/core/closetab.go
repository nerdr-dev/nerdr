package core

import (
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func init() { registerRequests(CloseTab{}) }

// CloseTab closes tab T, stopping every program in it; with Remove its
// worktrees are deleted too, by force when Force says to, and with
// BranchHeads (the head of each worktree's branch the user was shown, by
// path) its branches, unless one moved since.
type CloseTab struct {
	T           TabRef
	Remove      bool
	Force       bool
	BranchHeads map[string]string
}

func (q CloseTab) Do(m *Core) Cmd {
	return m.CloseTab(m.ByRef(q.T), q.Remove, q.Force, q.BranchHeads)
}

// TabClosedMsg is how closing a tab went, in the background: Err when
// it did not go as asked.
type TabClosedMsg struct{ Err, Note string }

// CloseTab closes t; see the request CloseTab.
func (m *Core) CloseTab(t *Tab, remove, force bool, branchHeads map[string]string) Cmd {
	return m.closeTab(t, remove, force, branchHeads, branchHeads != nil)
}

// closeTab is CloseTab, the worktrees kept unless each still has the head
// heads gives it, by path, once the programs have stopped, when heads is
// not nil; their branches deleted too with deleteBranch.
func (m *Core) closeTab(t *Tab, remove, force bool, heads map[string]string, deleteBranch bool) Cmd {
	i := m.TabIndex(t)
	if i < 0 || m.ReposHold(t) {
		return nil
	}
	m.Tabs = append(m.Tabs[:i], m.Tabs[i+1:]...)
	if remove {
		delete(m.GroupNames, t.Dir)
	}
	m.ForgetTabPRNews(t.Name)
	m.SaveSession()
	m.FindOverlaps() // the other tabs no longer share files with this one
	// What the agents' hooks kept in the temporary folder goes with them,
	// once the programs in the tab's panes have stopped, below. Not the
	// reviewers': EndReview stops each and forgets it then (stopAgent).
	// One put to sleep a moment ago may still be ending: see stopAgent.
	var hookKeys []string
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if a.HookID != "" {
			hookKeys = append(hookKeys, a.HookID)
		}
	}
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if a.Review != nil {
			m.EndReview(a, "stopped", false)
		}
	}
	var progs []*term.Pane
	for _, p := range t.Panes() {
		if tp := p.Term(); tp != nil {
			progs = append(progs, tp)
		}
	}
	dir, wts, name := t.Dir, t.Worktrees, t.Name
	return func() Event {
		// Every program in the tab's panes stopped before its worktree
		// goes.
		for _, p := range progs {
			p.Close()
		}
		for _, k := range hookKeys {
			forgetAgent(k)
		}
		if !remove {
			return TabClosedMsg{Note: "closed " + name + " — the worktree is kept"}
		}
		if heads != nil {
			// The agent may have committed while the dialog was open, or
			// since the worktrees were looked at. It is stopped now: check
			// every worktree again before removing any.
			for _, w := range wts {
				if h := heads[w.Path]; h == "" || workspace.Head(w) != h {
					if deleteBranch {
						return TabClosedMsg{Err: "closed " + name + "; kept the worktree and branch: the branch moved after you were asked"}
					}
					return TabClosedMsg{Err: "closed " + name + "; kept the worktree: " + filepath.Base(w.Path) + " has a commit made after it was looked at"}
				}
			}
		}
		group := ""
		if len(wts) > 1 {
			group = dir
		}
		if err := workspace.Remove(group, wts, force); err != nil {
			return TabClosedMsg{Err: "could not delete the worktree: " + err.Error()}
		}
		if deleteBranch {
			var errs []string
			for _, w := range wts {
				if err := workspace.DeleteBranch(w, heads[w.Path]); err != nil {
					errs = append(errs, err.Error())
				}
			}
			if len(errs) > 0 {
				return TabClosedMsg{Err: "deleted the worktree, not the branch: " + strings.Join(errs, "; ")}
			}
			return TabClosedMsg{Note: "closed " + name + ", deleted its worktree" + pluralS(len(wts)) + " and branch"}
		}
		return TabClosedMsg{Note: "closed " + name + " and deleted its worktree" + pluralS(len(wts))}
	}
}
