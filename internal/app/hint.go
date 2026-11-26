package app

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The prefix hint is a box in the middle of the screen with every key
// that can follow the prefix, the most used first, in one level: a key
// that opens a group of its own is marked ›, and pressed, the box shows
// the group's keys instead, with esc or backspace back. It shows once the
// prefix has waited hintDelay for its next key, so a key known by heart,
// pressed at once, never brings it up; prefix_hint off, it never shows.

// hintDelay is how long the prefix waits for its next key before the hint
// shows.
const hintDelay = 400 * time.Millisecond

// hintDueMsg comes hintDelay after the prefix, to draw the hint.
type hintDueMsg struct{}

// hintWanted reports whether the hint is on (prefix_hint).
func (m *Model) hintWanted() bool {
	b := m.cfg().PrefixHint
	return b == nil || *b
}

// hintDue is the tick that draws the hint once the prefix has waited.
func (m *Model) hintDue() tea.Cmd {
	if !m.hintWanted() {
		return nil
	}
	return tea.Tick(hintDelay, func(time.Time) tea.Msg { return hintDueMsg{} })
}

// hintShown reports whether the hint is on screen. Not while the arrows
// walk the sidebar: it would hide the tabs walked, and the bar says what
// the arrows do; nor with the key panel, which shows the keys itself.
func (m *Model) hintShown() bool {
	return m.prefix && m.hintWanted() && time.Since(m.prefixAt) >= hintDelay &&
		!(m.nav && m.sideW() > 0) && !m.railShown()
}

// walkHintShown reports whether the small box saying the prefix is still
// on shows: while the arrows walk the sidebar, in place of the hint.
func (m *Model) walkHintShown() bool {
	return m.prefix && m.nav && m.sideW() > 0 && m.keyGroup == "" && m.dialog == nil &&
		m.hintWanted() && !m.railShown()
}

// walkHint is the box in the corner while the arrows walk the sidebar: the
// prefix is still on, what the arrows do, how to stay in the tab, and the
// prefix again for all the keys.
func (m *Model) walkHint() string {
	tabs, repos := "tabs", "repositories"
	if m.onGroups {
		tabs, repos = repos, tabs
	}
	kw := max(7, ansi.StringWidth(m.cfg().Prefix)+1)
	row := func(k, what string) string { return keyHint(padTo(k, kw)) + dim(what) }
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(accent).Render(m.cfg().Prefix + " is still on"),
		row("↑ ↓", tabs),
		row("← →", repos),
		row("enter", "stay in this tab"),
		row(m.cfg().Prefix, "all the keys"),
		dim("another key: its command,"),
		dim("for the tab walked to"),
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).
		Padding(0, 1).Render(strings.Join(lines, "\n"))
}

// hintOrder is the prefix's keys (their Run) the most used first. The rest
// come after them in the order of the menu, the rare ones last.
var hintOrder = []string{
	"c", "x", "n",
	"left", "right", "down", "up",
	"w", "m", "e", "a", "t", "g",
	"tab", "f", "1",
	"r", "p", "M", "k", "h", "l", "o", "/", "u", "?", "s",
}

// hintEntries are the keys the hint lists now: the prefix's, or those of
// the group it went into, and the title over them.
func (m *Model) hintEntries() (title string, entries []core.CommandEntry, inGroup bool) {
	if g, ok := core.KeyGroups()[m.keyGroup]; ok {
		return m.cfg().Prefix + " " + m.shownKeys(m.keyGroup) + "  " + g.Title, rareLast(g.Entries), true
	}
	groups := core.CommandGroups()
	if m.sideW() > 0 {
		groups = sideArrows(groups)
	}
	for _, g := range groups {
		entries = append(entries, g.Entries...)
	}
	rank := func(e core.CommandEntry) int {
		if e.Rare {
			return len(hintOrder) + 1
		}
		if i := slices.Index(hintOrder, e.Run); i >= 0 {
			return i
		}
		return len(hintOrder)
	}
	slices.SortStableFunc(entries, func(a, b core.CommandEntry) int { return rank(a) - rank(b) })
	return "Keys after " + m.cfg().Prefix, entries, false
}

// prefixHint draws the hint's box to fit w×h: its keys in columns read
// down each; past what fits, in pages turned with PgUp and PgDn.
func (m *Model) prefixHint(w, h int) string {
	keySt := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	txtSt := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	rareSt := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	offSt := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	title, entries, inGroup := m.hintEntries()

	keys := make([]string, len(entries))
	keyW, cellW := 0, 0
	for i, e := range entries {
		keys[i] = e.Key
		if !inGroup && !panelKey(e.Run) {
			keys[i] = m.shownKeys(e.Key)
		}
		keyW = max(keyW, ansi.StringWidth(keys[i]))
	}
	more := func(e core.CommandEntry) string {
		if !inGroup && core.IsGroup(e.Run) {
			return " ›"
		}
		return ""
	}
	for _, e := range entries {
		cellW = max(cellW, keyW+1+ansi.StringWidth(e.Label)+ansi.StringWidth(more(e)))
	}

	// As few columns as keep it to tallRows rows, up to three. Only to
	// keep it to one page are the columns narrowed to fit one more, their
	// long labels cut short.
	const gap, tallRows = 3, 14
	inner := max(w-4, 8) // the frame and a column of room on each side
	room := max(1, h-4)  // the frame, the title and the line below
	tall := func(cols int) int { return (len(entries) + cols - 1) / cols }
	fit := max(1, min(3, (inner+gap)/(cellW+gap)))
	squeezed := max(fit, min(3, (inner+gap)/(min(cellW, keyW+1+26)+gap)))
	cols := 1
	for cols < fit && tall(cols) > min(room, tallRows) {
		cols++
	}
	for cols < squeezed && tall(cols) > room {
		cols++
	}
	cellW = min(cellW, (inner-(cols-1)*gap)/cols)

	// The keys below them: back, close, and the pages there are. They go
	// on as many lines as the width needs, each a line less for the keys.
	paging := func(room int) (rows, pages int) {
		rows = tall(cols)
		if rows > room {
			return room, (len(entries) + room*cols - 1) / (room * cols)
		}
		return rows, 1
	}
	footer := func(pages int) []string {
		var foot []string
		if inGroup {
			foot = append(foot, m.onClick(keyHint("esc")+dim(" back"), func(m *Model) tea.Cmd {
				m.prefix, m.keyGroup, m.groupWalk, m.prefixPage = true, "", false, 0
				return nil
			}))
		}
		foot = append(foot, keyHint(m.cfg().Prefix)+dim(" close"))
		if pages > 1 {
			page := min(m.prefixPage, pages-1) + 1
			foot = append(foot, keyHint("PgUp PgDn")+dim(" more ("+strconv.Itoa(page)+"/"+strconv.Itoa(pages)+")"))
		}
		var out []string
		cur := ""
		for _, part := range foot {
			if cur != "" && ansi.StringWidth(cur)+3+ansi.StringWidth(part) > inner {
				out = append(out, cur)
				cur = ""
			}
			if cur != "" {
				cur += dim(" · ")
			}
			cur += part
		}
		return append(out, cur)
	}
	rows, pages := paging(room)
	foot := footer(pages)
	if len(foot) > 1 {
		rows, pages = paging(max(1, room-len(foot)+1))
		foot = footer(pages)
	}
	m.prefixPage = min(m.prefixPage, pages-1)
	page := entries[m.prefixPage*rows*cols:]
	pageKeys := keys[m.prefixPage*rows*cols:]

	cell := func(i int) string {
		if i >= len(page) {
			return strings.Repeat(" ", cellW)
		}
		e := page[i]
		ks, ts := keySt, txtSt
		switch {
		case m.commandOff(e.Run) != "":
			ks, ts = offSt, offSt
		case e.Rare:
			ks, ts = rareSt, rareSt
		}
		mark := more(e) // kept when the label is cut short
		label := ansi.Truncate(e.Label, max(cellW-keyW-1-ansi.StringWidth(mark), 1), "…") + mark
		s := ks.Render(padTo(pageKeys[i], keyW)) + ts.Render(" "+padTo(label, cellW-keyW-1))
		return m.onClick(s, func(m *Model) tea.Cmd { return m.keyClick(e) })
	}
	lineW := cols*cellW + (cols-1)*gap
	for _, l := range foot {
		lineW = max(lineW, min(ansi.StringWidth(l), inner))
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(accent).Render(padTo(ansi.Truncate(title, lineW, "…"), lineW))}
	for r := range rows {
		parts := make([]string, cols)
		for c := range cols {
			parts[c] = cell(c*rows + r)
		}
		lines = append(lines, padTo(strings.Join(parts, strings.Repeat(" ", gap)), lineW))
	}
	for _, l := range foot {
		lines = append(lines, padTo(l, lineW))
	}

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).
		Padding(0, 1).Render(strings.Join(lines, "\n"))
}
