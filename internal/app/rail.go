package app

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The key panel is a narrow column at the right of the body, there all
// the time with key_panel on: the prefix's keys, each with what it does,
// in the groups of the hint line. A group's keys open below its own while
// the prefix waits for one of them (prefix a, say), the rest dimmed; with
// the prefix pressed the column lights up. Off, or where the window has
// no room for it, the hint line over the bottom rows says the same after
// the prefix (prefixHint). The F-keys open the review panel's screens at
// the top of it.

// keyPanelW is the columns of the key panel, its frame included.
const keyPanelW = 30

// panelRailW is the columns of the key panel, its frame included.
func (m *Model) panelRailW() int { return keyPanelW }

// keyPanel reports whether the key panel is asked for (key_panel).
func (m *Model) keyPanel() bool { return core.IsOn(m.cfg().KeyPanel) }

// railShown reports whether the key panel is on screen: when asked for,
// and the window has room for it beside the agent.
func (m *Model) railShown() bool {
	return m.keyPanel() && m.bodyW() >= 3*m.panelRailW()
}

// railCols is the columns the key panel takes from the body, the one
// between it and the rest included.
func (m *Model) railCols() int {
	if !m.railShown() {
		return 0
	}
	return m.panelRailW() + 1
}

// keyRow is a row of the key panel, the hint line's entry it is for (nil
// for a heading), and whether it belongs to the group open.
type keyRow struct {
	text  string
	open  bool // the group open, or one of its keys
	entry *core.CommandEntry
}

// keyRows are the key panel's rows, inner columns wide.
func (m *Model) keyRows(inner int) []keyRow {
	keySt := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	txtSt := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	offSt := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	heading := lipgloss.NewStyle().Foreground(accent).Bold(true)
	inGroup := m.prefix && m.keyGroup != ""
	item := func(indent, key, label string, off, dim bool, more string) string {
		ks, ts := keySt, txtSt
		if off || dim {
			ks, ts = offSt, offSt
		}
		key = padTo(key, 3) // the labels in line, past a short key
		room := inner - len(indent) - ansi.StringWidth(key) - 1 - ansi.StringWidth(more)
		return ks.Render(indent+key) + ts.Render(" "+ansi.Truncate(label, max(room, 1), "…")) + keySt.Render(more)
	}
	var rows []keyRow
	head := func(title string, open bool) {
		st := heading
		if inGroup && !open {
			st = offSt.Bold(true)
		}
		rows = append(rows, keyRow{text: st.Render(ansi.Truncate(" "+title, inner, "…")), open: open})
	}

	// The F-keys first: the screens of the review panel.
	head("Panel", false)
	shown := -1
	if m.panelShown() {
		shown = m.review.ScreenIndex()
	}
	keys, _ := m.screenMenuKeys()
	for i, name := range review.ScreenNames() {
		key := "F" + strconv.Itoa(i+1)
		if i < len(keys) {
			key = keys[i]
		}
		more := ""
		if i == 1 && m.review.News() {
			more = "◆"
		}
		text := item(" ", key, name, false, inGroup, "")
		if more != "" {
			text = ansi.Truncate(text, inner-1, "") + warn(more)
		}
		if i == shown {
			text = lipgloss.NewStyle().Background(tabActiveBg).Foreground(lipgloss.Color("231")).Bold(true).
				Render(padTo(ansi.Strip(text), inner))
		}
		rows = append(rows, keyRow{text: text, entry: &core.CommandEntry{Key: key, Label: name, Run: "f" + strconv.Itoa(i+1)}})
	}

	groups := core.CommandGroups()
	if m.sideW() > 0 {
		groups = sideArrows(groups)
	}
	sub := core.KeyGroups()
	for _, g := range groups {
		head(g.Title, false)
		for _, e := range rareLast(g.Entries) {
			e := e
			_, isGroup := sub[e.Run]
			open := isGroup && inGroup && m.keyGroup == e.Run
			more := ""
			if isGroup {
				more = core.OnOff(open, " ▾", " ▸")
			}
			key := m.shownKeys(e.Key)
			rows = append(rows, keyRow{
				text:  item(" ", key, e.Label, m.commandOff(e.Run) != "", e.Rare || (inGroup && !open), more),
				open:  open,
				entry: &e,
			})
			if !open {
				continue
			}
			for _, se := range rareLast(sub[e.Run].Entries) {
				se := se
				rows = append(rows, keyRow{
					text:  item("   ", se.Key, se.Label, m.commandOff(se.Run) != "", se.Rare, ""),
					open:  true,
					entry: &se,
				})
			}
		}
	}
	return rows
}

// rareLast is entries with the rare ones after the rest, each kept in its
// order.
func rareLast(entries []core.CommandEntry) []core.CommandEntry {
	out := make([]core.CommandEntry, 0, len(entries))
	for _, rare := range []bool{false, true} {
		for _, e := range entries {
			if e.Rare == rare {
				out = append(out, e)
			}
		}
	}
	return out
}

// railLines draws the key panel, h rows of panelRailW columns.
func (m *Model) railLines(h int) []string {
	rw := m.panelRailW()
	inner := rw - 2
	bs := lipgloss.NewStyle().Foreground(frameColor)
	if m.prefix {
		bs = lipgloss.NewStyle().Foreground(accent)
	}
	row := func(s string) string {
		return bs.Render("│") + padTo(s, inner) + bs.Render("│")
	}
	title := " " + m.cfg().Prefix + " "
	titleSt := bs
	if m.prefix {
		titleSt = lipgloss.NewStyle().Background(accent).Foreground(lipgloss.Color("232")).Bold(true)
	}
	title = ansi.Truncate(title, max(inner-1, 0), "")
	top := bs.Render("╭─") + titleSt.Render(title) + bs.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(title), 0))+"╮")

	rows := m.keyRows(inner)
	room := max(h-2, 0)
	m.keyTop = min(max(m.keyTop, 0), max(len(rows)-room, 0))
	if m.prefix && m.keyGroup != "" {
		// The group open in sight, from its own key down as far as it fits.
		first, last := -1, -1
		for i, r := range rows {
			if r.open {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first >= 0 {
			if last >= m.keyTop+room {
				m.keyTop = last - room + 1
			}
			m.keyTop = min(m.keyTop, first)
		}
	}
	out := []string{top}
	for _, r := range rows[m.keyTop:min(len(rows), m.keyTop+room)] {
		text := r.text
		if e := r.entry; e != nil {
			tip := e.Label
			if e.Desc != "" {
				tip += " · " + e.Desc
			}
			if off := m.commandOff(e.Run); off != "" {
				tip += " · " + off
			}
			text = m.tip(m.zone(text, func(m *Model, ev tea.MouseMsg) tea.Cmd {
				if m.keyWheel(ev) {
					return nil
				}
				if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
					return m.keyClick(*e)
				}
				return nil
			}), tip)
		}
		out = append(out, row(text))
	}
	for len(out) < h-1 {
		out = append(out, row(""))
	}
	bottom := strings.Repeat("─", inner)
	switch above, below := m.keyTop > 0, m.keyTop+room < len(rows); {
	case above && below:
		bottom = "─ ↑↓ " + strings.Repeat("─", max(inner-5, 0))
	case below:
		bottom = "─ ↓ " + strings.Repeat("─", max(inner-4, 0))
	case above:
		bottom = "─ ↑ " + strings.Repeat("─", max(inner-4, 0))
	}
	out = append(out[:min(len(out), h-1)], bs.Render("╰"+ansi.Truncate(bottom, inner, "")+"╯"))
	return out
}

// scrollKeys moves the key panel's rows by n, down for n > 0.
func (m *Model) scrollKeys(n int) { m.keyTop = max(m.keyTop+n, 0) }

// keyWheel scrolls the key panel for a turn of the wheel over it, and
// reports whether ev was one.
func (m *Model) keyWheel(ev tea.MouseMsg) bool {
	switch ev.Button {
	case tea.MouseButtonWheelUp:
		m.scrollKeys(-3)
	case tea.MouseButtonWheelDown:
		m.scrollKeys(3)
	default:
		return false
	}
	return true
}

// keyClick is a click on entry e of the key panel or the hint line: it
// runs the command as its keys would, or opens its group.
func (m *Model) keyClick(e core.CommandEntry) tea.Cmd {
	if strings.HasPrefix(e.Run, "f") && panelKey(e.Run) {
		m.prefix, m.keyGroup = false, ""
		return m.screenPressed(keyMsg(e.Run))
	}
	if strings.Contains(e.Run, " ") || panelKey(e.Run) {
		m.prefix, m.keyGroup = false, ""
		return m.runCommand(e.Run)
	}
	m.prefix, m.keyGroup, m.nav = true, "", false
	return m.handleKey(keyMsg(m.swapKey(e.Run))) // as pressed
}
