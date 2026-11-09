package review

// context.go holds what belongs to the selected worktree beyond its files —
// its code's structure, its PR, the watch core keeps on it — much of it
// loaded in the background.

import (
	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"

	tea "github.com/charmbracelet/bubbletea"
)

// switchWorktree moves the panel to the worktree selected, if it is
// another, and reports whether it was. The base, the notes and the reading
// order are left empty, for the caller to read in the background.
func (m *Model) switchWorktree() bool {
	wt := ""
	if m.wtIdx >= 0 && m.wtIdx < len(m.worktrees) {
		wt = m.worktrees[m.wtIdx].Path
	}
	if wt == m.curWT {
		return false
	}
	m.curWT = wt
	m.walkWanted = "" // a walkthrough asked for elsewhere does not open here
	m.watching = false
	m.projectVer, m.imports = 0, nil
	m.projectLoad = false
	m.ctxLoad = ""
	m.err = nil
	m.stamps = nil
	m.fileStats = nil
	m.recent = map[string]int{}
	m.prConvCursor, m.prConvViewTop = 0, 0
	m.resetFilesPane()
	m.expanded = map[string]bool{}
	m.diffMode = diffModeHunk
	m.peek = nil
	m.jumpHistory = nil
	if wt == "" {
		m.projectVer, m.imports = 0, nil
		m.baseRef = ""
		m.pr, m.prOK, m.prMsg = github.PR{}, false, ""
		m.whyIndex = why.Index{}
		m.orderIndex = order.Index{}
		m.setWalk(walkthrough.Walkthrough{}, false, false)
		return true
	}
	// The project walk and the PR lookup (a network round trip) run in
	// the background — see loadContextCmd — so moving between worktrees
	// doesn't freeze the screen while they finish.
	m.projectVer, m.imports = 0, nil
	m.pr, m.prOK, m.prMsg = github.PR{}, false, "looking up the PR…"
	m.ctxLoad = wt
	m.baseRef = ""
	m.whyIndex = why.Index{}
	m.orderIndex = order.Index{}
	m.setWalk(walkthrough.Walkthrough{}, false, false)
	return true
}

// worktreeContextMsg carries what loadContextCmd found for worktree wt.
type worktreeContextMsg struct {
	wt         string
	projectVer int
	pr         github.PR
	prOK       bool
	prMsg      string
	noGh       bool // gh is not installed
}

// loadContextCmd walks the worktree's code and asks GitHub for its PR: the
// parts of a worktree's context too slow to do while the key is held.
func loadContextCmd(src Source, wt string) tea.Cmd {
	return func() tea.Msg {
		msg := worktreeContextMsg{wt: wt, projectVer: src.LoadProject(wt)}
		msg.pr, msg.prOK, msg.prMsg = src.CurrentPR(wt)
		msg.noGh = !src.GhAvailable()
		return msg
	}
}

// projectMsg is worktree wt's code read again by loadProjectCmd, as of
// version.
type projectMsg struct {
	wt      string
	version int
}

// loadProjectCmd has worktree wt's code read again: a walk of the whole
// tree and every source file, too slow for a large one to do while keys
// and redraws wait.
func loadProjectCmd(src Source, wt string) tea.Cmd {
	return func() tea.Msg {
		return projectMsg{wt: wt, version: src.LoadProject(wt)}
	}
}

// importsMsg is the import graph among worktree wt's changed files, read
// from its code as of version.
type importsMsg struct {
	wt      string
	version int
	imports map[string][]string
}

// readImports reads the import graph among the changed files, which the
// tree groups them by.
func (m *Model) readImports() tea.Cmd {
	if m.projectVer == 0 || len(m.files) == 0 {
		return nil
	}
	src, wt, version, paths := m.src, m.curWT, m.projectVer, filePaths(m.files)
	return func() tea.Msg {
		return importsMsg{wt: wt, version: version, imports: src.Imports(wt, paths)}
	}
}

// watchedPollTicks is how many ticks pass between polls of the files while
// a watch is in place: a safety net, not how changes are found.
const watchedPollTicks = 20

// WatchMsg asks nerdr to watch worktree Wt for changes, the one the panel
// shows, in place of the one before; "" for none. nerdr tells the panel
// whether the watch is in place (Watching), and each change (Changed).
type WatchMsg struct{ Wt string }

// Watching tells the panel whether the watch on worktree wt is in place:
// with it, the files are refreshed when it reports a change, and polled
// only now and then.
func (m Model) Watching(wt string, ok bool) Model {
	if wt == m.curWT {
		m.watching = ok
	}
	return m
}

// Changed is the watch on worktree wt reporting a change.
func (m Model) Changed(wt string) (Model, tea.Cmd) {
	if wt != m.curWT {
		return m, nil // a worktree left behind
	}
	return m, tea.Batch(m.refreshIfShown(), m.lookForWalk())
}

// changedGroups clusters the changed files by their imports of each
// other, so the file tree can review connected changes together instead
// of alphabetically.
func (m *Model) changedGroups(files []git.FileStatus) []analysis.Group {
	if m.imports == nil || len(files) == 0 {
		return nil
	}
	return analysis.GroupEdges(filePaths(files), m.imports)
}
