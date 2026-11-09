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
	m.project = nil
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
		m.project = nil
		m.baseRef = ""
		m.pr, m.prOK, m.prMsg = github.PR{}, false, ""
		m.whyIndex = why.Index{}
		m.orderIndex = order.Index{}
		m.setWalk(walkthrough.Walkthrough{}, false)
		return true
	}
	// The project walk and the PR lookup (a network round trip) run in
	// the background — see loadContextCmd — so moving between worktrees
	// doesn't freeze the screen while they finish.
	m.project = nil
	m.pr, m.prOK, m.prMsg = github.PR{}, false, "looking up the PR…"
	m.ctxLoad = wt
	m.baseRef = ""
	m.whyIndex = why.Index{}
	m.orderIndex = order.Index{}
	m.setWalk(walkthrough.Walkthrough{}, false)
	return true
}

// worktreeContextMsg carries what loadContextCmd found for worktree wt.
type worktreeContextMsg struct {
	wt      string
	project *analysis.Project
	pr      github.PR
	prOK    bool
	prMsg   string
	noGh    bool // gh is not installed
}

// loadContextCmd walks the worktree's code and asks GitHub for its PR: the
// parts of a worktree's context too slow to do while the key is held.
func loadContextCmd(src Source, wt string) tea.Cmd {
	return func() tea.Msg {
		msg := worktreeContextMsg{wt: wt, project: analysis.LoadProject(wt)}
		msg.project.Index()
		msg.pr, msg.prOK, msg.prMsg = src.CurrentPR(wt)
		msg.noGh = !src.GhAvailable()
		return msg
	}
}

// projectMsg carries worktree wt's project, read again by loadProjectCmd.
type projectMsg struct {
	wt      string
	project *analysis.Project
}

// loadProjectCmd reads worktree wt's code again: a walk of the whole tree
// and every source file, too slow for a large one to do while keys and
// redraws wait.
func loadProjectCmd(wt string) tea.Cmd {
	return func() tea.Msg {
		p := analysis.LoadProject(wt)
		p.Index()
		return projectMsg{wt: wt, project: p}
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

// changedGroups clusters the changed files by
// dependency relationship via the loaded project's import graph, so the
// file tree can review connected changes together instead of alphabetically.
func (m *Model) changedGroups(files []git.FileStatus) []analysis.Group {
	if m.project == nil {
		return nil
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	return m.project.GroupChanged(paths)
}

// reloadRelations recomputes the symbols of the currently selected file,
// or of m.peek's file when peeking at a file outside the tree, along with
// where each is referenced — what the diff pane annotates changed lines
// with, and what those annotations jump to.
func (m *Model) reloadRelations() {
	m.symbols = nil
	m.symbolRefs = nil
	if m.project == nil {
		return
	}
	var rel string
	var ok bool
	if m.peek != nil {
		rel, ok = m.peek.file, true
	} else {
		rel, ok = m.currentFile()
	}
	if !ok {
		return
	}
	m.symbols = m.project.Symbols(rel)
	m.symbolRefs = make([][]analysis.Ref, len(m.symbols))
	for i, s := range m.symbols {
		m.symbolRefs[i] = m.project.References(s.Name, rel)
	}
}
