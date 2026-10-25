package app

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A bar with more groups or tabs than fit shows a window of them, with
// the one you are on always in it. What does not fit is counted at the
// edge it went over, beside the light of the hidden one that most wants
// a look, so a question in a tab out of view is still seen. Moving along
// the row scrolls the window only as far as it takes to keep the one
// moved to in view.

// barItem is one entry of a bar: drawn, zones and all, and its light.
type barItem struct {
	s  string
	st status
}

// barSep is the columns between the entries of a bar.
const barSep = 1

// barRow is what a bar showed when last drawn, for the scrolling to start
// from next time and for telling whether it has room for another entry.
type barRow struct {
	start  int // the first entry shown
	hidden int // entries not shown
	room   bool
}

// statusRank orders the lights by how much they want a look.
func statusRank(s status) int {
	switch s {
	case stBlocked:
		return 6
	case stDone:
		return 5
	case stWorking:
		return 4
	case stIdle:
		return 3
	case stStarting, stUnknown:
		return 2
	}
	return 1
}

// markerWidth is the columns the count of n hidden entries takes.
func markerWidth(n int) int { return 4 + len(strconv.Itoa(n)) }

// barWindow picks the entries lo to hi (exclusive), of widths ws, shown in
// avail columns: from start on as far as they fit, moved along until
// entry cur is among them, then widened back towards the start while
// there is room.
func barWindow(ws []int, cur, start, avail int) (lo, hi int) {
	n := len(ws)
	width := func(lo, hi int) int {
		w := 0
		for i := lo; i < hi; i++ {
			w += ws[i] + barSep
		}
		w -= barSep
		if lo > 0 {
			w += markerWidth(lo) + barSep
		}
		if hi < n {
			w += markerWidth(n-hi) + barSep
		}
		return w
	}
	if n == 0 || width(0, n) <= avail {
		return 0, n
	}
	cur = min(max(cur, 0), n-1)
	lo = min(max(start, 0), cur)
	for {
		hi = lo + 1
		for hi < n && width(lo, hi+1) <= avail {
			hi++
		}
		if hi > cur {
			break
		}
		lo++
	}
	for lo > 0 && width(lo-1, hi) <= avail {
		lo--
	}
	return lo, hi
}

// scrollBar lays out items in avail columns around entry cur, starting
// from what row showed last, and notes in row what it shows now. what
// names the entries, for the tips; goTo goes to one.
func (m *Model) scrollBar(items []barItem, cur int, row *barRow, avail int, what string, goTo func(m *Model, i int) tea.Cmd) string {
	ws := make([]int, len(items))
	for i, it := range items {
		ws[i] = lipgloss.Width(it.s)
	}
	lo, hi := barWindow(ws, cur, row.start, avail)
	sep := lipgloss.NewStyle().Background(tabBarBg).Render(strings.Repeat(" ", barSep))
	var parts []string
	if lo > 0 {
		parts = append(parts, m.hiddenMarker(items, 0, lo, true, what, goTo))
	}
	for i := lo; i < hi; i++ {
		parts = append(parts, items[i].s)
	}
	if hi < len(items) {
		parts = append(parts, m.hiddenMarker(items, hi, len(items), false, what, goTo))
	}
	out := strings.Join(parts, sep)
	used := lipgloss.Width(out)
	// Room for one more: as wide as the narrowest entry there is.
	narrowest := avail
	for _, w := range ws {
		narrowest = min(narrowest, w)
	}
	*row = barRow{start: lo, hidden: len(items) - (hi - lo), room: lo == 0 && hi == len(items) && used+barSep+narrowest <= avail}
	return out
}

// hiddenMarker counts the entries lo to hi, hidden off the left edge of a
// bar (left) or the right, with the light of the one that most wants a
// look. A click goes to that one; of those as keen, the nearest.
func (m *Model) hiddenMarker(items []barItem, lo, hi int, left bool, what string, goTo func(m *Model, i int) tea.Cmd) string {
	// Walked from the window outwards, so a tie keeps the nearest.
	best, step, end := lo, 1, hi
	if left {
		best, step, end = hi-1, -1, lo-1
	}
	for i := best; i != end; i += step {
		if statusRank(items[i].st) > statusRank(items[best].st) {
			best = i
		}
	}
	st := lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("250"))
	arrow := lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("244"))
	n := hi - lo
	count := lightOn(items[best].st, tabBarBg) + st.Render(strconv.Itoa(n))
	var s, side string
	if left {
		s, side = arrow.Render(" ◂ ")+count+st.Render(" "), "left"
	} else {
		s, side = st.Render(" ")+count+arrow.Render(" ▸ "), "right"
	}
	tip := plural(n, what) + " hidden to the " + side + " · the one that most wants a look: " +
		strings.ToLower(statusTip(items[best].st)) + " · click: go to it"
	return m.tip(m.onClick(s, func(m *Model) tea.Cmd { return goTo(m, best) }), tip)
}
