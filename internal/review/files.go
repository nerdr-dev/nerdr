package review

// files.go holds the worktree and file lists: loading and refreshing them,
// filtering them, and marking the files just touched.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) loadWorktrees() tea.Cmd {
	if m.repoIdx < 0 || m.repoIdx >= len(m.repos) {
		m.worktrees = nil
		m.curRepo = ""
		return nil
	}
	repoPath := m.repos[m.repoIdx].Path
	wts, err := git.ListWorktrees(repoPath)
	if err != nil {
		m.err = err
		return nil
	}
	if m.embedded {
		wts = m.filterPinned(wts)
	}
	if repoPath != m.curRepo {
		// A genuinely different repo: default to its primary worktree
		// rather than reusing whatever index happened to be selected in
		// the previous repo, which may point at an unrelated worktree.
		m.curRepo = repoPath
		m.wtIdx = 0
	}
	m.worktrees = wts
	m.wtIdx = clamp(m.wtIdx, 0, len(wts)-1)
	return nil
}

func (m *Model) loadFiles() tea.Cmd {
	wtChanged := m.ensureWorktreeContext()
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	files, err := m.changedFiles()
	if err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	if m.setFiles(files, wtChanged) {
		m.loadDiff()
	}
	m.refreshing = false
	return nil
}

func (m *Model) refreshFiles() tea.Cmd {
	if m.focus == focusRepos {
		// No worktree picked yet, so there are no files to refresh. The
		// repo list has its own, slower cadence — see autoRefresh.
		return nil
	}
	if m.ensureWorktreeContext() {
		// Another worktree: loaded at once, as moving to it asked.
		m.filesToken++
		m.filesBusy, m.filesAgain = false, false
		return m.loadFiles()
	}
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	if m.filesBusy {
		m.filesAgain = true // one at a time; the next when it is done
		return nil
	}
	m.filesBusy, m.refreshing = true, true
	m.filesToken++
	c, token, wt := m.comparison(), m.filesToken, m.curWT
	return func() tea.Msg {
		files, err := c.Files()
		msg := filesMsg{wt: wt, token: token, files: files, err: err}
		if err == nil {
			msg.stats = c.NumStat()
		}
		return msg
	}
}

// filesMsg is a refresh of the file list, done in the background: git is
// not run on the screen's time.
type filesMsg struct {
	wt    string
	token int
	files []git.FileStatus
	stats map[string]git.LineCount
	err   error
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

	notesBefore := m.whyIndex.Read()
	m.whyIndex = loadNotes(m.worktrees[m.wtIdx].Path)
	notesChanged := m.whyIndex.Read() != notesBefore

	// The reading order is written by the agent when asked, so it arrives
	// while you are looking at the change it describes.
	orderBefore := m.orderIndex.Read()
	m.orderIndex = order.Load(m.worktrees[m.wtIdx].Path, m.worktrees[m.wtIdx].Branch)
	orderChanged := m.orderIndex.Read() != orderBefore

	m.fileStats = msg.stats
	reloadDiff := m.setFiles(msg.files, false)
	m.setWalk(walkthrough.Load(m.worktrees[m.wtIdx].Path, m.worktrees[m.wtIdx].Branch))
	cmds := []tea.Cmd{m.walkArrived()}
	// The project is read again in the background, and the tree and diff
	// rebuilt when it arrives changed; one read at a time, as a busy agent
	// reports changes faster than a large tree is read.
	if m.project != nil && !m.projectLoad {
		m.projectLoad = true
		cmds = append(cmds, loadProjectCmd(m.curWT))
	}
	if orderChanged && m.showOrder {
		path, _ := m.currentFile()
		m.rebuildTree(path)
	}
	// New notes can land on hunks whose files didn't otherwise change.
	if reloadDiff || notesChanged {
		cmds = append(cmds, m.loadDiffLater())
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
	m.reloadRelations()
}

// setFiles updates the changed-file list, rebuilding the tree and analysis
// state when file paths/status changed OR the worktree they belong
// to did (checked before listing files — two worktrees can have the same,
// often empty, file list, which must still force a refresh rather than
// leaving the previous worktree's project/diff content showing).
//
// It reports whether the diff shown is to be loaded again, which the
// caller does, at once or in the background.
func (m *Model) setFiles(files []git.FileStatus, wtChanged bool) (reloadDiff bool) {
	old := m.files
	filesChanged := !sameFiles(old, files)
	prev, _ := m.currentFile()
	m.files = files

	// An agent editing a file that is already changed leaves the list as
	// it was, so the files' write stamps are what show it.
	stamps := m.fileStamps(files)
	edited := map[string]bool{}
	if m.recent == nil {
		m.recent = map[string]int{}
	}
	if wtChanged {
		// Recent-touch markers compare old vs new files to spot live edits
		// within the same worktree; across a worktree switch that
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

	if filesChanged || wtChanged {
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

// fileStamps records when and to what size each changed file was last
// written. A deleted file has none, and a change to that shows up in its
// status instead.
func (m *Model) fileStamps(files []git.FileStatus) map[string]string {
	out := make(map[string]string, len(files))
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return out
	}
	wt := m.worktrees[m.wtIdx].Path
	for _, f := range files {
		if fi, err := os.Stat(filepath.Join(wt, filepath.FromSlash(f.Path))); err == nil {
			out[f.Path] = fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return out
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

// cycleWorktree switches to the previous/next worktree in the current repo.
func (m *Model) cycleWorktree(delta int) {
	if len(m.worktrees) == 0 {
		return
	}
	m.peek = nil
	m.jumpHistory = nil
	m.wtIdx = (m.wtIdx + delta + len(m.worktrees)) % len(m.worktrees)
	m.loadFiles()
	m.loadDiff()
	wt := m.worktrees[m.wtIdx]
	m.setToast(fmt.Sprintf("%s — %d files", wt.Branch, len(m.files)))
}
