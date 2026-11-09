package review

// files.go holds the worktree and file lists: loading and refreshing them,
// filtering them, and marking the files just touched.

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"

	tea "github.com/charmbracelet/bubbletea"
)

// loadFiles reads the file list again in the background, overtaking a
// refresh under way: the comparison changed (b), so the list that comes is
// a new one, not the old one edited (see filesMsg.anew).
func (m *Model) loadFiles() tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	m.filesToken++
	m.filesBusy, m.filesAgain, m.refreshing = true, false, true
	return m.readFiles(true)
}

// refreshFiles reads the file list again in the background, one read at a
// time.
func (m *Model) refreshFiles() tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	if m.filesBusy {
		m.filesAgain = true // one at a time; the next when it is done
		return nil
	}
	m.filesBusy, m.refreshing = true, true
	m.filesToken++
	return m.readFiles(false)
}

// readFiles is the command that reads the file list, the lines changed in
// each file and when each was written, with what the agent may have
// written of the change meanwhile: the notes, the reading order and the
// walkthrough; and, once the worktree's code has been read, the files'
// imports of each other.
func (m *Model) readFiles(anew bool) tea.Cmd {
	c, token, wt, src := m.comparison(), m.filesToken, m.curWT, m.src
	branch, version := m.worktrees[m.wtIdx].Branch, m.projectVer
	return func() tea.Msg {
		msg := filesMsg{wt: wt, token: token, anew: anew}
		msg.files, msg.stats, msg.err = src.Files(c)
		if msg.err != nil {
			return msg
		}
		paths := filePaths(msg.files)
		msg.stamps = src.Stamps(c.Worktree, paths)
		msg.notes = src.Notes(wt)
		msg.order = src.Order(wt, branch)
		msg.walk, msg.hasWalk, msg.walkOld = src.Walkthrough(wt, branch, paths)
		if version != 0 {
			msg.version, msg.imports = version, src.Imports(wt, paths)
		}
		return msg
	}
}

func filePaths(files []git.FileStatus) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

// filesMsg is a refresh of the file list, done in the background: git is
// not run on the screen's time.
type filesMsg struct {
	wt     string
	token  int
	anew   bool // a list of another comparison; see setFiles
	files  []git.FileStatus
	stats  map[string]git.LineCount
	stamps map[string]string // see Source.Stamps
	err    error

	notes            why.Index
	order            order.Index
	walk             walkthrough.Walkthrough
	hasWalk, walkOld bool
	version          int                 // of the code imports was read from, 0 for none
	imports          map[string][]string // see Model.imports
}

// onFiles takes a refresh of the file list: the files, and the notes and
// reading order the agent may have written meanwhile.
func (m *Model) onFiles(msg filesMsg) tea.Cmd {
	if msg.token != m.filesToken || msg.wt != m.curWT {
		return nil // overtaken, or about a worktree left behind
	}
	m.filesBusy, m.refreshing = false, false
	again := m.filesAgain
	m.filesAgain = false
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	m.err = nil

	notesChanged := msg.notes.Read() != m.whyIndex.Read()
	m.whyIndex = msg.notes

	// The reading order is written by the agent when asked, so it arrives
	// while you are looking at the change it describes.
	orderChanged := msg.order.Read() != m.orderIndex.Read()
	m.orderIndex = msg.order

	if msg.version != 0 && msg.version == m.projectVer {
		m.imports = msg.imports // before the tree is built from them
	}
	m.fileStats = msg.stats
	reloadDiff := m.setFiles(msg.files, msg.stamps, msg.anew)
	m.setWalk(msg.walk, msg.hasWalk, msg.walkOld)
	cmds := []tea.Cmd{m.walkArrived()}
	// The project is read again in the background, and the tree and diff
	// rebuilt when it arrives changed; one read at a time, as a busy agent
	// reports changes faster than a large tree is read.
	if m.projectVer != 0 && !m.projectLoad {
		m.projectLoad = true
		cmds = append(cmds, loadProjectCmd(m.src, m.curWT))
	}
	if orderChanged && m.showOrder {
		path, _ := m.currentFile()
		m.rebuildTree(path)
	}
	// New notes can land on hunks whose files didn't otherwise change.
	if reloadDiff || notesChanged {
		cmds = append(cmds, m.loadDiff())
	}
	if again {
		cmds = append(cmds, m.refreshIfShown())
	}
	return tea.Batch(cmds...)
}

// f1Shown reports whether the file list is on screen: F1, in a panel
// nerdr shows.
func (m *Model) f1Shown() bool { return m.mode == modeReview && !m.hidden }

// refreshIfShown refreshes the file list while it is on screen, and marks
// it stale otherwise, for coming back to it.
func (m *Model) refreshIfShown() tea.Cmd {
	if !m.f1Shown() {
		m.filesStale = true
		return nil
	}
	m.filesStale = false
	return m.refreshFiles()
}

// refreshIfStale refreshes a stale file list once it is on screen again.
func (m *Model) refreshIfStale() tea.Cmd {
	if !m.filesStale || !m.f1Shown() {
		return nil
	}
	m.filesStale = false
	return m.refreshFiles()
}

// resetFilesPane clears the file tree and everything derived from it, so a
// repo change doesn't leave the previous repo's files on screen.
func (m *Model) resetFilesPane() {
	m.files = nil
	m.tree = nil
	m.cursor = 0
	m.peek = nil
	m.diff = ""
	m.diffLines, m.diffSel = nil, lineSpan{}
	m.diffCursor, m.diffViewTop = 0, 0
	m.symbols, m.symbolRefs = nil, nil
}

// setFiles updates the changed-file list, rebuilding the tree and analysis
// state when file paths/status changed OR the list is of another
// worktree or comparison (anew) (checked before listing files — two worktrees can have the same,
// often empty, file list, which must still force a refresh rather than
// leaving the previous worktree's project/diff content showing).
//
// It reports whether the diff shown is to be loaded again, which the
// caller does, at once or in the background.
func (m *Model) setFiles(files []git.FileStatus, stamps map[string]string, anew bool) (reloadDiff bool) {
	old := m.files
	filesChanged := !sameFiles(old, files)
	prev, _ := m.currentFile()
	m.files = files

	// An agent editing a file that is already changed leaves the list as
	// it was, so the files' write stamps are what show it.
	edited := map[string]bool{}
	if m.recent == nil {
		m.recent = map[string]int{}
	}
	if anew {
		// Recent-touch markers compare old vs new files to spot live edits
		// within the same list; across a worktree switch, or to another
		// comparison (b), whose committed work can be months old, that
		// comparison is meaningless, so just clear them instead.
		m.recent = map[string]int{}
	} else {
		if filesChanged {
			m.markRecent(old, files)
		}
		for p, st := range stamps {
			if was, ok := m.stamps[p]; !ok || was != st {
				edited[p] = true
				m.recent[p] = recentTicks
			}
		}
		for p := range m.stamps {
			if _, ok := stamps[p]; !ok {
				edited[p] = true
				m.recent[p] = recentTicks
			}
		}
	}
	m.stamps = stamps

	if filesChanged || anew {
		m.rebuildTree(prev)
		// The diff (not just the relations) so the cached diff pane
		// content — text, syntax highlighting and annotations — stays in
		// sync with live refreshes too, not just explicit navigation.
		return true
	}
	return edited[prev] && m.peek == nil
}

// rebuildTree reconstructs the tree from the current file list (applying the
// active filter and ignore rules), restoring the cursor to prevPath when
// possible and preserving expand state.
func (m *Model) rebuildTree(prevPath string) {
	files := m.files
	if m.filter != "" {
		files = filterFiles(files, m.filter)
	}
	if m.hideIgnored {
		files = hideIgnoredFiles(files)
	}
	if m.folderTree {
		m.tree = buildFolderTree(files)
	} else if m.showOrder && m.orderIndex.Len() > 0 {
		m.tree = buildOrderTree(files, m.orderIndex)
	} else {
		m.tree = buildTree(files, m.changedGroups(files))
	}
	if m.peek != nil {
		m.tree = append([]*treeNode{peekNode(m.peek.file)}, m.tree...)
	}
	if prevPath != "" {
		for i, r := range m.flatRows() {
			if !r.node.isDir && r.node.path == prevPath {
				m.cursor = i
				return
			}
		}
	}
	m.cursor = 0
}

func filterFiles(files []git.FileStatus, sub string) []git.FileStatus {
	sub = strings.ToLower(sub)
	out := make([]git.FileStatus, 0, len(files))
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Path), sub) {
			out = append(out, f)
		}
	}
	return out
}

var ignoredSubstr = []string{
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb",
	".min.js", ".min.css", ".min.js.map",
	"node_modules/", ".next/", "dist/", "build/", "out/",
}

func ignoredPath(path string) bool {
	for _, s := range ignoredSubstr {
		if strings.Contains(path, s) {
			return true
		}
	}
	return false
}

func hideIgnoredFiles(files []git.FileStatus) []git.FileStatus {
	// A new slice, not files[:0]: files is often m.files itself.
	out := make([]git.FileStatus, 0, len(files))
	for _, f := range files {
		if !ignoredPath(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

func (m *Model) startFilter() {
	m.filtering = true
	m.filterInput.SetValue(m.filter)
	m.filterInput.Focus()
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
		prev, _ := m.currentFile()
		m.rebuildTree(prev)
		cmd := m.loadDiff()
		return m, cmd
	case "enter":
		m.filtering = false
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	if m.filter != m.filterInput.Value() {
		prev, _ := m.currentFile()
		m.filter = m.filterInput.Value()
		m.rebuildTree(prev)
		cmd = tea.Batch(cmd, m.loadDiff())
	}
	return m, cmd
}

const recentTicks = 4

// markRecent flags files whose presence or status changed since the previous
// refresh, so they can be highlighted as just touched.
func (m *Model) markRecent(old, new []git.FileStatus) {
	codes := map[string]string{}
	for _, f := range old {
		codes[f.Path] = f.Code
	}
	for _, f := range new {
		if c, ok := codes[f.Path]; !ok || c != f.Code {
			m.recent[f.Path] = recentTicks
		}
	}
}

func sameFiles(a, b []git.FileStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// currentFileStatus returns the git.FileStatus of the file under the
// cursor, if any.
func (m *Model) currentFileStatus() (git.FileStatus, bool) {
	path, ok := m.currentFile()
	if !ok {
		return git.FileStatus{}, false
	}
	for _, f := range m.files {
		if f.Path == path {
			return f, true
		}
	}
	return git.FileStatus{}, false
}

// untracked reports whether path is a new file nobody has added yet.
func (m *Model) untracked(path string) bool {
	for _, f := range m.files {
		if f.Path == path {
			return f.Code == "??"
		}
	}
	return false
}
