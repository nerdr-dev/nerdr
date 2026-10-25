package review

// change.go holds what the selected worktree's change is measured against,
// and loading it into the diff pane in each of its views.

import (
	"github.com/nerdr-dev/nerdr/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// comparison is what the selected worktree's change is measured against,
// by the base-diff mode. Everything that lists, diffs or counts the change
// goes through it, so they cannot disagree.
func (m *Model) comparison() git.Comparison {
	c := git.Comparison{Worktree: m.worktrees[m.wtIdx].Path}
	if m.showBase && m.baseRef != "" {
		c.Base = m.baseRef
	}
	return c
}

// changedFiles returns the changed files, respecting the base-diff mode.
func (m *Model) changedFiles() ([]git.FileStatus, error) {
	c := m.comparison()
	files, err := c.Files()
	if err == nil {
		m.fileStats = c.NumStat()
	}
	return files, err
}

// changeDiff is the unified diff of path's change, whatever the diff pane
// is set to show.
func (m *Model) changeDiff(path string) (string, error) {
	return m.comparison().Diff(path, m.untracked(path), m.originalPath(path))
}

func (m *Model) originalPath(path string) string {
	for _, f := range m.files {
		if f.Path == path && f.OldPath != "" {
			return f.OldPath
		}
	}
	return path
}

// fileDiff returns the diff pane content for path, respecting both the
// base-diff mode and the old/new-full view mode. Untracked files have no
// prior version to diff against, so they always render as plain code
// regardless of diffMode.
func (m *Model) fileDiff(path string) (string, error) {
	if m.untracked(path) {
		return m.changeDiff(path)
	}
	switch m.diffMode {
	case diffModeOld:
		return m.comparison().OldFile(m.originalPath(path))
	case diffModeNewFull:
		raw, err := m.changeDiff(path)
		if err != nil {
			return "", err
		}
		return git.FullNewFileWithMarkers(m.worktrees[m.wtIdx].Path, path, raw)
	default:
		return m.changeDiff(path)
	}
}

// cycleDiffMode is v: the diff, then the whole file with the changes
// marked, then the old code, and round to the diff again.
func (m *Model) cycleDiffMode() tea.Cmd {
	switch m.diffMode {
	case diffModeHunk:
		m.diffMode = diffModeNewFull
		m.setToast("the whole file, changes marked · v the old code")
	case diffModeNewFull:
		m.diffMode = diffModeOld
		m.setToast("the old code · v the diff")
	default:
		m.diffMode = diffModeHunk
		m.setToast("the diff · v the whole file")
	}
	return m.loadDiff()
}

// toggleBase switches between working-tree changes and diff against the base
// branch.
func (m *Model) toggleBase() tea.Cmd {
	if m.baseRef == "" {
		m.setToast("no base branch (main/master) found")
		return nil
	}
	m.showBase = !m.showBase
	// Loaded at once, as moving to another worktree is: a refresh begun
	// before is of the other comparison, and is overtaken.
	m.filesToken++
	m.filesBusy, m.filesAgain, m.refreshing = false, false, false
	m.loadFiles()
	// setFiles derives the ● markers from the difference between the old
	// and new file lists, which here is a different comparison rather than
	// anything anyone touched — the branch's committed work can be months
	// old. Clear them instead of flagging the whole branch as live edits.
	m.recent = map[string]int{}
	m.loadDiff()
	if m.showBase {
		m.setToast("showing changes against " + m.baseRef)
	} else {
		m.setToast("showing worktree changes")
	}
	return nil
}

func (m *Model) loadDiff() tea.Cmd {
	m.diffToken++ // a load in the background is overtaken
	m.diffSel = lineSpan{}
	if m.peek != nil {
		return m.loadPeekDiff()
	}
	path, ok := m.currentFile()
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) || !ok {
		m.diff = ""
		m.diffLines = nil
		m.reloadRelations()
		return nil
	}
	diff, err := m.fileDiff(path)
	if err != nil {
		m.err = err
		m.diff = ""
		m.diffLines = nil
		return nil
	}
	m.diff = diff
	// reloadRelations must run first: annotateDiffLines below depends on the
	// freshly computed m.symbols/m.symbolRefs for the selected file.
	m.reloadRelations()
	m.diffLines = m.buildDisplayLines(path, diff)
	// Re-validate the cursor/viewport against the (possibly very different)
	// newly loaded content. jumpTo/jumpBack override this afterwards when
	// they need a specific position; plain navigation just needs the
	// existing cursor clamped into range.
	m.setDiffCursor(m.diffCursor)
	return nil
}

// loadDiffLater is loadDiff for a refresh, in the background: the diff of
// the file shown is read and its source fetched off the screen's time, and
// put in place when they come (onDiff). Navigation loads at once instead.
func (m *Model) loadDiffLater() tea.Cmd {
	path, ok := m.currentFile()
	if m.peek != nil || !ok || m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return m.loadDiff()
	}
	m.diffToken++
	snap, token, wt := *m, m.diffToken, m.curWT
	return func() tea.Msg {
		msg := diffMsg{wt: wt, token: token, path: path}
		msg.diff, msg.err = snap.fileDiff(path)
		msg.src, msg.srcOK = snap.highlightSourceFor(path)
		return msg
	}
}

// diffMsg is the diff of path, and its source, read in the background.
type diffMsg struct {
	wt, path  string
	token     int
	diff, src string
	srcOK     bool
	err       error
}

// onDiff puts a diff read in the background in place, if it is still the
// file shown.
func (m *Model) onDiff(msg diffMsg) {
	path, ok := m.currentFile()
	if msg.token != m.diffToken || msg.wt != m.curWT || !ok || path != msg.path || m.peek != nil {
		return
	}
	if msg.err != nil {
		m.err = msg.err
		m.diff, m.diffLines = "", nil
		return
	}
	m.diff = msg.diff
	m.reloadRelations()
	m.diffLines = m.displayLines(path, msg.diff, msg.src, msg.srcOK)
	m.diffSel = lineSpan{}
	m.setDiffCursor(m.diffCursor)
}

// buildDisplayLines turns the raw diff text for path into the final lines
// shown in the diff pane: word-highlighted, syntax-highlighted on unmarked
// context lines, and annotated with symbol references. Computed once per
// load rather than per render frame, since chroma tokenisation of a full
// file is too expensive to redo on every keystroke/scroll/tick.
func (m *Model) buildDisplayLines(path, diff string) []diffLine {
	src, ok := m.highlightSourceFor(path)
	return m.displayLines(path, diff, src, ok)
}

// displayLines is buildDisplayLines with the source already read.
func (m *Model) displayLines(path, diff, src string, srcOK bool) []diffLine {
	lines := buildDiffLines(diff)
	if srcOK {
		lines = highlightContextLines(lines, path, src)
	}
	lines = annotateDiffLines(lines, m.symbols, m.symbolRefs)
	return annotateWhyLines(lines, path, m.whyIndex)
}

// highlightSourceFor returns the source text whose line numbers align with
// the diff currently shown for path, for syntax-highlighting its unmarked
// context lines: the pre-change content (from HEAD/base) in the old-code
// view, otherwise the worktree's current content.
func (m *Model) highlightSourceFor(path string) (string, bool) {
	if m.diffMode == diffModeOld {
		src, err := m.comparison().OldSource(m.originalPath(path))
		return src, err == nil
	}
	src, err := git.ReadWorktreeFile(m.worktrees[m.wtIdx].Path, path)
	return src, err == nil
}
