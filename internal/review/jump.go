package review

// jump.go holds following a reference from the diff to where it leads, the
// file opened when that is outside the change, and the way back.

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// peekTarget names a file being viewed outside the normal changed-file tree.
type peekTarget struct {
	file string
}

// jumpState is a saved diff-pane position, pushed before jumping to a
// reference so "u" can return to exactly where the user was.
type jumpState struct {
	cursor      int
	peek        *peekTarget
	diffMode    diffMode
	diffCursor  int
	diffViewTop int
}

// jumpToSelected jumps to the reference target of the diff line currently
// under the cursor (the top of the visible viewport), if any. It reports
// whether a jump happened.
func (m *Model) jumpToSelected() bool {
	idx := clamp(m.diffCursor, 0, max(0, len(m.diffLines)-1))
	if idx < 0 || idx >= len(m.diffLines) {
		return false
	}
	dl := m.diffLines[idx]
	if dl.kind != 'a' || dl.refFile == "" {
		return false
	}
	m.pushJumpState()
	m.jumpTo(dl.refFile, dl.refLine)
	return true
}

// openWhyDetail opens the full rationale of the ✎ line under the diff
// cursor in the relations panel — beside an agent, where there is none,
// under the line in the diff, or closes it there again. Reports whether
// the cursor was on one.
func (m *Model) openWhyDetail() bool {
	idx := clamp(m.diffCursor, 0, max(0, len(m.diffLines)-1))
	if idx < 0 || idx >= len(m.diffLines) {
		return false
	}
	dl := m.diffLines[idx]
	if dl.kind != 'w' || dl.whyDetail == "" {
		return false
	}
	if m.embedded {
		m.toggleWhyInline(idx)
		return true
	}
	m.whyDetail = dl.whyDetail
	m.relationsTab = relationsTabWhy
	m.relCursor, m.relViewTop = 0, 0
	m.buildRelRows()
	// Focus follows the text: the rationale is usually longer than the
	// panel, and leaving focus on the diff would strand it — ↑/↓ would keep
	// moving the diff cursor, and → would just re-open what is already open.
	m.focus = focusRelations
	return true
}

// toggleWhyInline opens the rationale of the ✎ line at idx under it in the
// diff, or closes it when it is open.
func (m *Model) toggleWhyInline(idx int) {
	end := idx + 1
	for end < len(m.diffLines) && m.diffLines[end].whyMore {
		end++
	}
	if end > idx+1 {
		m.diffLines = append(m.diffLines[:idx+1], m.diffLines[end:]...)
		return
	}
	w := max(m.diffInnerWidth()-6, 20)
	var more []diffLine
	for _, para := range strings.Split(m.diffLines[idx].whyDetail, "\n") {
		if strings.TrimSpace(para) == "" {
			more = append(more, diffLine{kind: 'w', whyMore: true})
			continue
		}
		for _, l := range wrapText(para, w, 0) {
			more = append(more, diffLine{kind: 'w', whyMore: true, text: "    " + l})
		}
	}
	m.diffLines = append(m.diffLines[:idx+1], append(more, m.diffLines[idx+1:]...)...)
}

// jumpTo navigates to file at line: selecting it in the changed-file tree
// (switching to the full-file view so the target line is guaranteed
// visible) if it's one of the changed files, otherwise opening a
// read-only "peek" of its current worktree content.
func (m *Model) jumpTo(file string, line int) {
	if idx, ok := m.rowForFile(file); ok {
		m.peek = nil
		m.cursor = idx
		m.diffMode = diffModeNewFull
		m.loadDiff()
		m.diffViewTop = 0 // fresh content: compute the viewport from scratch, not the old file's scroll state
		m.setDiffCursor(m.diffRowForLine(line))
		return
	}
	m.peek = &peekTarget{file: file}
	m.loadDiff()
	// Rebuild so the tree carries the peek row, and put the file-pane
	// cursor on it: otherwise the tree keeps marking the file jumped from
	// while the diff pane shows a different one.
	m.rebuildTree(file)
	m.diffViewTop = 0
	m.setDiffCursor(m.diffRowForLine(line))
}

// clearPeek leaves the peeked file, dropping its row from the tree so the
// cursor can't sit on a row that no longer exists.
func (m *Model) clearPeek() {
	if m.peek == nil {
		return
	}
	m.peek = nil
	m.rebuildTree("")
}

// diffRowForLine returns the index in m.diffLines whose tracked "new" line
// number matches line — not simply line-1, since the currently loaded diff
// (a synthetic full-file diff, in diffModeNewFull) has header/hunk rows
// ahead of the actual content, which a raw index would land inside of.
// Falls back to the closest earlier line if there's no exact match.
func (m *Model) diffRowForLine(line int) int {
	best := 0
	for i, dl := range m.diffLines {
		if dl.newLine == line {
			return i
		}
		if dl.newLine > 0 && dl.newLine < line {
			best = i
		}
	}
	return best
}

// currentNewLine is the line number in the current file that the diff
// cursor sits on, or 0 when there is none to name — the cursor is on a
// header, an annotation, or the file pane has focus and no line has been
// picked out yet. Rows without a line of their own (an ✎ rationale, a
// reference list) belong to the line below them, so it searches forward.
func (m *Model) currentNewLine() int {
	for i := m.diffCursor; i >= 0 && i < len(m.diffLines); i++ {
		if n := m.diffLines[i].newLine; n > 0 {
			return n
		}
	}
	return 0
}

// rowForFile returns the flat-tree row index of path, expanding any
// collapsed ancestor directories/groups so it becomes visible.
func (m *Model) rowForFile(path string) (int, bool) {
	found := false
	for _, f := range m.files {
		if f.Path == path {
			found = true
			break
		}
	}
	if !found {
		return 0, false
	}
	m.expandAncestors(path)
	for i, r := range m.flatRows() {
		if !r.node.isDir && r.node.path == path {
			return i, true
		}
	}
	return 0, false
}

// expandAncestors marks every directory/group node on the path to target as
// expanded, so it becomes reachable in the flattened tree.
func (m *Model) expandAncestors(target string) {
	var walk func(nodes []*treeNode) bool
	walk = func(nodes []*treeNode) bool {
		for _, n := range nodes {
			if n.isDir {
				if walk(n.children) {
					m.expanded[n.path] = true
					return true
				}
			} else if n.path == target {
				return true
			}
		}
		return false
	}
	walk(m.tree)
}

// pushJumpState saves the current diff-pane position so jumpBack can
// restore it.
func (m *Model) pushJumpState() {
	m.jumpHistory = append(m.jumpHistory, jumpState{
		cursor:      m.cursor,
		peek:        m.peek,
		diffMode:    m.diffMode,
		diffCursor:  m.diffCursor,
		diffViewTop: m.diffViewTop,
	})
}

// jumpBack restores the diff-pane position saved before the last jump.
func (m *Model) jumpBack() {
	if len(m.jumpHistory) == 0 {
		m.setToast("no earlier position")
		return
	}
	st := m.jumpHistory[len(m.jumpHistory)-1]
	m.jumpHistory = m.jumpHistory[:len(m.jumpHistory)-1]
	m.restoreJump(st)
}

// leaveJumps undoes every jump at once, back to where the first one was
// made. Leaving the review layout does this: a file opened from a
// reference or a note, still listed on coming back with nothing on screen
// saying how it got there, reads as part of the change.
func (m *Model) leaveJumps() {
	if len(m.jumpHistory) == 0 {
		return
	}
	st := m.jumpHistory[0]
	m.jumpHistory = nil
	m.restoreJump(st)
}

func (m *Model) restoreJump(st jumpState) {
	// Rebuild before restoring the cursor: the saved index refers to a tree
	// without the peek row, so it has to be gone again before the index
	// means what it did when it was saved.
	m.peek = st.peek
	m.rebuildTree("")
	m.cursor = st.cursor
	m.diffMode = st.diffMode
	m.loadDiff()
	m.diffViewTop = st.diffViewTop
	m.setDiffCursor(st.diffCursor)
}

// loadPeekDiff renders m.peek's file as plain syntax-highlighted code (no
// diff markers, since it isn't necessarily changed) for the diff pane.
func (m *Model) loadPeekDiff() tea.Cmd {
	wt := m.worktrees[m.wtIdx].Path
	src, err := git.ReadWorktreeFile(wt, m.peek.file)
	if err != nil {
		m.err = err
		m.diff = ""
		m.diffLines = nil
		return nil
	}
	m.diff = src
	m.diffLines = annotateWhyLines(plainSourceLines(m.peek.file, src), m.peek.file, m.whyIndex)
	m.reloadRelations()
	m.setDiffCursor(m.diffCursor)
	return nil
}
