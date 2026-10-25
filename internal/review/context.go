package review

// context.go holds what belongs to the selected worktree beyond its files —
// its code's structure, its PR, the watch on it — much of it loaded in
// the background.

import (
	"nerdr/internal/analysis"
	"nerdr/internal/git"
	"nerdr/internal/github"
	"nerdr/internal/order"
	"nerdr/internal/store"
	"nerdr/internal/walkthrough"
	"nerdr/internal/watch"
	"nerdr/internal/why"

	tea "github.com/charmbracelet/bubbletea"
)

// ensureWorktreeContext reloads project/agent/base state when the selected
// worktree has actually changed, and reports whether it did — callers use
// that to force a refresh even when the new worktree's file list happens
// to look the same as the old one (e.g. both clean), which otherwise would
// leave stale context (wrong project, wrong diff content) showing.
func (m *Model) ensureWorktreeContext() bool {
	if !m.switchWorktree() {
		return false
	}
	if wt := m.curWT; wt != "" {
		m.baseRef = git.DetectBase(wt)
		m.whyIndex = loadNotes(wt)
		m.orderIndex = order.Load(wt, m.worktrees[m.wtIdx].Branch)
		_, m.walk = walkthrough.Load(wt, m.worktrees[m.wtIdx].Branch)
	}
	return true
}

// switchWorktree is ensureWorktreeContext without the parts that run git:
// the base, the notes and the reading order are left empty, for the
// caller to fill in, at once or in the background.
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
	m.stopWatch()
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
		m.whyIndex, m.whyDetail = why.Index{}, ""
		m.orderIndex, m.walk = order.Index{}, false
		return true
	}
	// The project walk and the PR lookup (a network round trip) run in
	// the background — see loadContextCmd — so moving between worktrees
	// doesn't freeze the screen while they finish.
	m.project = nil
	m.pr, m.prOK, m.prMsg = github.PR{}, false, "looking up the PR…"
	m.ctxLoad = wt
	m.baseRef = ""
	m.whyIndex, m.whyDetail = why.Index{}, ""
	m.orderIndex, m.walk = order.Index{}, false
	return true
}

// worktreeContextMsg carries what loadContextCmd found for worktree wt.
type worktreeContextMsg struct {
	wt      string
	project *analysis.Project
	pr      github.PR
	prOK    bool
	prMsg   string
	watcher *watch.Watcher // nil when the watch could not be set up
}

// loadContextCmd walks the worktree's code, sets up the watch on it and
// asks GitHub for its PR: the parts of a worktree's context too slow to do
// while the key is held.
func loadContextCmd(wt string) tea.Cmd {
	return func() tea.Msg {
		msg := worktreeContextMsg{wt: wt, project: analysis.LoadProject(wt)}
		msg.project.Index()
		if dir := store.CommonDir(wt); dir != "" {
			if w, err := watch.New(wt, dir); err == nil {
				msg.watcher = w
			}
		}
		msg.pr, msg.prOK, msg.prMsg = github.CurrentPR(wt)
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

// WatchChanges is the worktree watched for changes, and how many it has
// reported in all, for others to tell when something there changed: a
// file edited, which git's own files do not show. wt is "" with no watch.
func (m Model) WatchChanges() (wt string, n int) {
	if m.watcher == nil {
		return "", m.watchN
	}
	return m.curWT, m.watchN
}

// fsChangedMsg is the watch on worktree wt reporting a change.
type fsChangedMsg struct{ wt string }

// waitForChange waits for w's next report. It ends without a message once
// w is closed, which is what ends the wait for a worktree left behind.
func waitForChange(w *watch.Watcher, wt string) tea.Cmd {
	return func() tea.Msg {
		if !w.Wait() {
			return nil
		}
		return fsChangedMsg{wt: wt}
	}
}

// stopWatch ends the watch on the worktree being left.
func (m *Model) stopWatch() {
	if m.watcher != nil {
		m.watcher.Close()
		m.watcher = nil
	}
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
	defer m.buildRelRows()
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
