package review

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A pane whose content is longer than it has rows for shows where the
// view is on its right-hand border: a thumb on the border line. The wheel
// scrolls the view; a click on the border line jumps there, and dragging
// the thumb scrolls with it.
//
// The thumb is ┃, the border's │ made heavy, not a full block: terminals
// draw box-drawing lines themselves, the cell's whole height, where a
// block is the font's glyph, which on macOS falls short of the line
// height and leaves the thumb in pieces.

var (
	thumbActive   = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	thumbInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// thumbGlyph is what the thumb is drawn with.
const thumbGlyph = "┃"

// scrollbar draws the thumb over the right-hand border of pane, a rendered
// pane whose content rows start at row first and number innerH, showing
// rows top… of total. It leaves a pane whose content fits as it is.
func scrollbar(pane string, total, top, innerH, first int, active bool) string {
	if total <= innerH || innerH < 1 {
		return pane
	}
	pos, size := thumb(total, top, innerH)
	st := thumbInactive
	if active {
		st = thumbActive
	}
	lines := strings.Split(pane, "\n")
	for i := pos; i < pos+size; i++ {
		y := first + i
		if y >= len(lines) {
			break
		}
		w := lipgloss.Width(lines[y])
		lines[y] = ansi.Truncate(lines[y], w-1, "") + st.Render(thumbGlyph)
	}
	return strings.Join(lines, "\n")
}

// thumb is where the thumb is on a track of innerH rows, and how long.
func thumb(total, top, innerH int) (pos, size int) {
	size = clamp(innerH*innerH/total, 1, innerH)
	maxTop := total - innerH
	pos = (clamp(top, 0, maxTop)*(innerH-size) + maxTop/2) / maxTop
	return clamp(pos, 0, innerH-size), size
}

// trackTop is the top of the view that row i of the track stands for: the
// first row is the start, the last the end.
func trackTop(total, innerH, i int) int {
	maxTop := total - innerH
	if maxTop <= 0 || innerH <= 1 {
		return 0
	}
	return clamp((i*maxTop+(innerH-1)/2)/(innerH-1), 0, maxTop)
}

// scrollPane is a pane the scrollbar can be dragged in.
type scrollPane int

const (
	scrollNone scrollPane = iota
	scrollDiff
	scrollRelations
	scrollPR
	scrollFiles
	scrollRepos
	scrollWorktrees
	scrollOverview
)

// onScrollbar reports which pane's border line (x, y) is on, within its
// content rows, and the pane's place.
func (m Model) onScrollbar(l paneLayout, x, y int) (scrollPane, rect, int) {
	zoomed := scrollNone
	switch m.focus {
	case focusDiff:
		zoomed = scrollDiff
	case focusRelations:
		zoomed = scrollRelations
	case focusFiles:
		zoomed = scrollFiles
	case focusRepos:
		zoomed = scrollRepos
	case focusWorktrees:
		zoomed = scrollWorktrees
	}
	for _, c := range []struct {
		p      scrollPane
		r      rect
		header int
	}{
		{scrollDiff, l.diff, 0},
		{scrollRelations, l.relations, 0},
		{scrollPR, l.prConv, 0},
		{scrollFiles, l.files, m.filesHeader()},
		{scrollRepos, l.repos, 0},
		{scrollWorktrees, l.worktrees, 0},
		{scrollOverview, l.overview, 0},
		{zoomed, l.zoom, 0},
	} {
		if c.p == scrollNone || !c.r.contains(x, y) || x != c.r.x+c.r.w-1 {
			continue
		}
		if c.r.contentRow(y, c.header) >= 0 {
			return c.p, c.r, c.header
		}
	}
	return scrollNone, rect{}, 0
}

// dragScrollbar scrolls pane p so the view is where row y of its track,
// in r, stands for.
func (m *Model) dragScrollbar(p scrollPane, r rect, header, y int) tea.Cmd {
	innerH := max(r.h-3-hintRows-header, 1)
	i := clamp(y-r.y-2-header, 0, innerH-1)
	// at is the item row i stands for, for the panes whose view follows
	// their cursor.
	at := func(n int) int {
		if innerH <= 1 || n <= 1 {
			return 0
		}
		return clamp((i*(n-1)+(innerH-1)/2)/(innerH-1), 0, n-1)
	}
	switch p {
	case scrollDiff:
		v := m.diffView()
		m.scrollDiffTo(trackTop(v.rowOf(len(m.diffLines)), v.innerH, i))
	case scrollRelations:
		m.scrollRelTo(trackTop(len(m.relRows), m.relationsInnerHeight(), i))
	case scrollPR:
		rows, _ := m.prContent()
		m.scrollPRPane(trackTop(len(rows), m.prConvInnerHeight(), i) - m.prConvViewTop)
	case scrollFiles:
		// Moving off the opened section leaves it, as the keys do; the
		// track then stands for the tree without its rows.
		peeked := m.peek != nil
		m.clearPeek()
		if n := len(m.flatRows()); n > 0 && (peeked || at(n) != m.cursor) {
			m.cursor = at(n)
			return m.loadDiff()
		}
	case scrollRepos:
		if n := len(m.repos); n > 0 && at(n) != m.repoIdx {
			m.repoIdx = at(n)
			return m.onRepoMove()
		}
	case scrollWorktrees:
		if n := len(m.worktrees); n > 0 && at(n) != m.wtIdx {
			m.wtIdx = at(n)
			return m.onWorktreeMove()
		}
	case scrollOverview:
		m.ovCursor = at(len(m.ovItems))
	}
	return nil
}

// scrollDiffTo puts the diff view's top at top, the cursor staying on the
// same row while that is in view and else moving to the nearest one that
// is: the wheel scrolls the view, not the cursor through it.
func (m *Model) scrollDiffTo(top int) {
	v := m.diffView()
	m.diffViewTop = clamp(top, 0, v.maxTop())
	m.diffCursor = clamp(m.diffCursor, max(0, v.lineAt(0, m.diffViewTop)), max(0, v.last(m.diffViewTop)))
	m.diffCursor = clamp(m.diffCursor, 0, max(0, len(m.diffLines)-1))
}

// scrollRelTo is scrollDiffTo for the relations panel.
func (m *Model) scrollRelTo(top int) {
	innerH := m.relationsInnerHeight()
	m.relViewTop = clamp(top, 0, max(0, len(m.relRows)-innerH))
	m.relCursor = clamp(m.relCursor, m.relViewTop, min(m.relViewTop+innerH, len(m.relRows))-1)
	m.relCursor = max(m.relCursor, 0)
}
