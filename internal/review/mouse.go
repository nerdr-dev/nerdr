package review

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// rect is a pane's place on screen, in terminal cells.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return r.w > 0 && r.h > 0 && x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// contentRow maps a screen row to an index into the pane's *visible* rows,
// or -1 when the click landed on the border, the heading or the hint row.
// Every pane is drawn the same way — border, heading, content, hint, border
// — so this arithmetic holds for all of them; headerRows covers the one
// that also draws a column header.
func (r rect) contentRow(y, headerRows int) int {
	i := y - r.y - 2 - headerRows
	if i < 0 || i >= r.h-3-hintRows-headerRows {
		return -1
	}
	return i
}

// paneLayout is where every pane sits for the current mode and size. It
// exists so the geometry is worked out once: View draws from it and the
// mouse hit-tests against it, which is the only way a click can be trusted
// to land on the row it appears to be on.
type paneLayout struct {
	menu      rect
	repos     rect
	worktrees rect
	files     rect
	diff      rect
	relations rect
	prConv    rect
	// zoom is the single pane filling the window; which one it is follows
	// m.focus, the same way zoomedView renders it.
	zoom     rect
	overview rect
}

// menuRect is where the menu's items are in width W: inside the end
// columns that nerdr frames it with when embedded (see menuBar).
func (m Model) menuRect(W int) rect {
	if m.embedded && W > 2 {
		return rect{1, 0, W - 2, m.menuRows()}
	}
	return rect{0, 0, W, m.menuRows()}
}

// layout computes the panes for width W and height H.
func (m Model) layout(W, H int) paneLayout {
	var l paneLayout
	if m.nerdrDraws() {
		l.menu = m.menuRect(W)
		return l
	}
	if m.overview {
		l.overview = rect{0, 0, W, H}
		return l
	}
	l.menu = m.menuRect(W)

	if m.mode == modePRs {
		l.prConv = rect{0, m.menuRows(), W, H - m.statusRows() - m.menuRows()}
		return l
	}
	if m.zoomed {
		l.zoom = rect{0, m.menuRows(), W, H - m.statusRows() - m.menuRows()}
		return l
	}
	if m.embedded {
		return m.embeddedLayout(W, H)
	}

	leftW, midW, rightW := paneWidths(W)
	topH := m.topPaneHeight(H)
	reposH := min(9, topH/2)

	l.repos = rect{0, m.menuRows(), leftW, reposH}
	l.worktrees = rect{0, m.menuRows() + reposH, leftW, topH - reposH}
	l.files = rect{leftW, m.menuRows(), midW, topH}
	l.diff = rect{leftW + midW, m.menuRows(), rightW, topH}
	l.relations = rect{0, m.menuRows() + topH, W, m.relationsPaneHeight(H)}
	return l
}

// paneWidths splits the window between the three columns of the review
// layout. The two trailing columns the diff pane gives up keep the border
// off the last cell, where some terminals wrap.
func paneWidths(W int) (leftW, midW, rightW int) {
	const gap = 1
	leftW, midW = 22, 36
	if W < 100 {
		leftW, midW = 18, 28
	}
	rightW = W - leftW - midW - 2*gap
	if rightW < 20 {
		rightW = 20
	}
	return leftW, midW, rightW
}

// listStart is the first visible item of a pane whose viewport follows its
// cursor (renderPane). Shared with the renderer so a click maps to the row
// that was actually drawn.
func listStart(idx, innerH, n int) int {
	if idx < 0 || n == 0 || idx < innerH {
		return 0
	}
	return idx - innerH + 1
}

// wheelRows is how far one notch of the wheel moves, in rows.
const wheelRows = 3

// handleMouse routes a mouse event to the pane under the pointer: the wheel
// scrolls it without taking focus, a click gives it focus and puts its
// cursor on the row clicked. Nothing is activated by a click — enter still
// does that, so a stray click cannot open a browser or start an agent.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.filtering {
		return nil
	}
	W, H := m.width, m.height
	if W == 0 {
		W = 120
	}
	if H == 0 {
		H = 32
	}
	l := m.layout(W, H)

	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		return m.wheel(l, msg.X, msg.Y, -wheelRows)
	case msg.Button == tea.MouseButtonWheelDown:
		return m.wheel(l, msg.X, msg.Y, wheelRows)
	case msg.Action == tea.MouseActionRelease:
		m.dragging = scrollNone
		return m.endSelect()
	case msg.Action == tea.MouseActionMotion && m.dragging != scrollNone:
		return m.dragScrollbar(m.dragging, m.dragRect, m.dragHeader, msg.Y)
	case msg.Action == tea.MouseActionMotion && m.selecting:
		m.extendSelect(msg.Y)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		m.dragging = scrollNone
		m.diffSel, m.selecting = lineSpan{}, false
		if p, r, header := m.onScrollbar(l, msg.X, msg.Y); p != scrollNone {
			m.dragging, m.dragRect, m.dragHeader = p, r, header
			return m.dragScrollbar(p, r, header, msg.Y)
		}
		cmd := m.click(l, msg.X, msg.Y)
		m.startSelect(l, msg.X, msg.Y)
		return cmd
	}
	return nil
}

func (m *Model) wheel(l paneLayout, x, y, delta int) tea.Cmd {
	switch {
	case l.overview.contains(x, y):
		m.ovCursor = clamp(m.ovCursor+delta, 0, max(0, len(m.ovItems)-1))

	case l.prConv.contains(x, y):
		m.scrollPRPane(delta)

	case l.zoom.contains(x, y):
		return m.scrollFocused(delta)

	case l.repos.contains(x, y):
		m.repoIdx = clamp(m.repoIdx+delta, 0, max(0, len(m.repos)-1))
		return m.onRepoMove()
	case l.worktrees.contains(x, y):
		m.wtIdx = clamp(m.wtIdx+delta, 0, max(0, len(m.worktrees)-1))
		return m.onWorktreeMove()
	case l.files.contains(x, y):
		m.moveCursor(delta)
		return m.loadDiff()
	case l.diff.contains(x, y):
		m.scrollDiffTo(m.diffViewTop + delta)
	case l.relations.contains(x, y):
		m.scrollRelTo(m.relViewTop + delta)
	}
	return nil
}

// scrollFocused scrolls whichever pane is zoomed, since only one is shown.
func (m *Model) scrollFocused(delta int) tea.Cmd {
	switch m.focus {
	case focusRepos:
		m.repoIdx = clamp(m.repoIdx+delta, 0, max(0, len(m.repos)-1))
		return m.onRepoMove()
	case focusWorktrees:
		m.wtIdx = clamp(m.wtIdx+delta, 0, max(0, len(m.worktrees)-1))
		return m.onWorktreeMove()
	case focusFiles:
		m.moveCursor(delta)
		return m.loadDiff()
	case focusRelations:
		m.scrollRelTo(m.relViewTop + delta)
	default:
		m.scrollDiffTo(m.diffViewTop + delta)
	}
	return nil
}

func (m *Model) click(l paneLayout, x, y int) tea.Cmd {
	switch {
	case l.menu.contains(x, y):
		// The menu's items, as menuBar draws them.
		at := l.menu.x
		modes := m.menuModes()
		for i, label := range m.menuShown(max(l.menu.w, 1)) {
			w := lipgloss.Width(label)
			if x >= at && x < at+w {
				return m.setMode(modes[i])
			}
			at += w
		}

	case l.overview.contains(x, y):
		if i := l.overview.contentRow(y, 0); i >= 0 {
			m.ovCursor = clamp(i, 0, max(0, len(m.ovItems)-1))
		}

	case l.prConv.contains(x, y):
		if y == l.prConv.y+1 && m.embedded {
			// The heading: the line of F2's views.
			if v, ok := m.prViewAt(x - l.prConv.x - 1); ok {
				return m.SetPRView(v)
			}
			return nil
		}
		if i := l.prConv.contentRow(y, 0); i >= 0 {
			row := m.prConvViewTop + i
			if rows, _ := m.prContent(); row < len(rows) && rows[row].button {
				return openInBrowser(rows[row].openURL)
			}
			m.selectConvRow(row)
		}

	case l.repos.contains(x, y):
		m.focus = focusRepos
		if i := l.repos.contentRow(y, 0); i >= 0 {
			innerH := l.repos.h - 3 - hintRows
			idx := listStart(m.repoIdx, innerH, len(m.repos)) + i
			if idx < len(m.repos) && idx != m.repoIdx {
				m.repoIdx = idx
				return m.onRepoMove()
			}
		}
	case l.worktrees.contains(x, y):
		m.focus = focusWorktrees
		if i := l.worktrees.contentRow(y, 0); i >= 0 {
			innerH := l.worktrees.h - 3 - hintRows
			idx := listStart(m.wtIdx, innerH, len(m.worktrees)) + i
			if idx < len(m.worktrees) && idx != m.wtIdx {
				m.wtIdx = idx
				return m.onWorktreeMove()
			}
		}
	case l.files.contains(x, y):
		// The hint row is drawn only while the list has the keyboard, and
		// each key on it is a button.
		if m.hasFocus(focusFiles) && y == l.files.y+l.files.h-2 {
			return m.clickFilesHint(x-l.files.x-1, l.files.w-2)
		}
		m.focus = focusFiles
		if y == l.files.y+1 && m.hasRepoBar() {
			if pin, ok := m.repoBarAt(l.files.w-2, x-l.files.x-1); ok {
				m.onRepoBar = true
				return m.ShowPin(pin)
			}
			return nil
		}
		m.onRepoBar = false
		if i := l.files.contentRow(y, m.filesHeader()); i >= 0 {
			rows := m.flatRows()
			if len(rows) == 0 {
				if _, button := m.emptyFiles(); button >= 0 && i == button {
					return m.toggleBase()
				}
				return nil
			}
			innerH := l.files.h - 3 - hintRows - m.filesHeader()
			idx := listStart(m.cursor, innerH, len(rows)) + i
			if idx < len(rows) {
				m.cursor = idx
				// Clicking outside the opened section leaves it, which drops
				// its rows: find the clicked row again in the tree without them.
				if m.peek != nil && !inPeekSection(rows, idx) {
					clicked := rows[idx].node
					m.clearPeek()
					for i, r := range m.flatRows() {
						if r.node.path == clicked.path && r.node.isDir == clicked.isDir {
							m.cursor = i
							break
						}
					}
				}
				return m.loadDiff()
			}
		}
	case l.diff.contains(x, y):
		m.focus = focusDiff
		if i := l.diff.contentRow(y, m.diffHeader()); i >= 0 {
			if line := m.diffView().lineAt(m.diffViewTop, i); line >= 0 {
				m.setDiffCursor(line)
			}
		}
	case l.relations.contains(x, y):
		m.focus = focusRelations
		if i := l.relations.contentRow(y, 0); i >= 0 {
			m.setRelCursor(m.relViewTop + i)
		}
	}
	return nil
}

// selectConvRow puts the conversation cursor on the block containing row,
// so clicking anywhere inside a comment selects that comment.
func (m *Model) selectConvRow(row int) {
	_, entries := m.prContent()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].row <= row {
			m.prConvCursor, m.prOnRepos = i, false
			return
		}
	}
}

// inPeekSection reports whether row idx of rows falls in the section that
// holds the file opened from a reference, which the tree lists first.
func inPeekSection(rows []flatRow, idx int) bool {
	if len(rows) == 0 || rows[0].node.path != "#"+sectionPeek {
		return false
	}
	for i := 1; i <= idx && i < len(rows); i++ {
		if rows[i].depth == 0 {
			return false
		}
	}
	return true
}

// clickFilesHint presses the key on the file list's hint row at column x,
// in a row w wide: a key cut off by the pane's edge isn't one.
func (m *Model) clickFilesHint(x, w int) tea.Cmd {
	at := 0
	for _, h := range m.filesHints() {
		end := at + lipgloss.Width(h)
		if end > w {
			break
		}
		if x >= at && x < end {
			switch h[0] {
			case '/':
				m.startFilter()
			case 't':
				return m.cycleFileList()
			case 'e':
				return m.explainFile()
			case 'E':
				return m.askExplain()
			case 'g':
				return m.openWalkthrough()
			case 'G':
				return m.askWalk()
			case 'L':
				return m.toggleSide()
			}
			return nil
		}
		at = end + lipgloss.Width(hintSep)
	}
	return nil
}
