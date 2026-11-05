package core

import "path/filepath"

// ReposOff says why t's repositories cannot be changed now, "" when they
// can.
func (m *Core) ReposOff(t *Tab) string {
	if t != nil {
		t = t.Owner()
	}
	switch {
	case t == nil:
		return "no tab open"
	case len(t.Worktrees) == 0:
		return "the tab is in a folder, not in worktrees nerdr made"
	case t.ReposBusy:
		return "the tab's repositories are being changed"
	case t.Worktrees[0].Branch == "":
		return "the tab's worktree has no branch checked out to put the new ones on"
	}
	for _, a := range t.AgentsIn() {
		if a.Review != nil {
			return "not while a code review runs in the tab"
		}
	}
	return ""
}

// ReposHold reports, and says so, whether t's tab is having its
// repositories changed: what would start in its worktrees, close them or
// stop its agents waits until that is done.
func (m *Core) ReposHold(t *Tab) bool {
	if t == nil || !t.Owner().ReposBusy {
		return false
	}
	m.Toast(t.Owner().Name + ": wait until its repositories are changed")
	return true
}

func YIes(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func SamePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
