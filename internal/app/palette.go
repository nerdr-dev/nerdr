package app

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// palette lists every command with its keys and what it does, and finds
// one by typing part of its name or description. It is the help: there is
// no list of keys apart from it. Like the switcher, but over allCommands.
type palette struct {
	input  textinput.Model
	cursor int
	top    int // the first row shown, of rows
}

// paletteEntry is a command as the palette lists it, with its group.
type paletteEntry struct {
	commandEntry
	group string
}

func (m *Model) openPalette() tea.Cmd {
	in := textinput.New()
	in.Prompt = "command: "
	in.Placeholder = "type to filter…"
	in.Focus()
	m.dialog = &palette{input: in}
	return nil
}

// matches are the commands matching what is typed, best first; with
// nothing typed, all of them in the menu's order.
func (p *palette) matches() []paletteEntry {
	q := strings.ToLower(strings.TrimSpace(p.input.Value()))
	type hit struct {
		e     paletteEntry
		score int
	}
	var hits []hit
	for _, g := range allCommands() {
		for _, e := range g.entries {
			if e.run == "?" {
				continue // the palette itself
			}
			score, ok := fuzzy(q, strings.ToLower(e.label+" "+g.title+" "+e.desc))
			if !ok {
				continue
			}
			hits = append(hits, hit{paletteEntry{e, g.title}, score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]paletteEntry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

func (p *palette) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	hits := p.matches()
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "up", "ctrl+p", "ctrl+k":
		p.cursor = max(p.cursor-1, 0)
		return nil
	case "down", "ctrl+n", "ctrl+j", "tab":
		p.cursor = min(p.cursor+1, max(len(hits)-1, 0))
		return nil
	case "enter":
		m.dialog = nil
		if p.cursor < len(hits) {
			return m.runCommand(hits[p.cursor].run)
		}
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.cursor, p.top = 0, 0
	return cmd
}

// paletteRow is a line of the list: a group's heading, or hit n.
type paletteRow struct {
	heading string
	n       int
}

// rows are the lines of the list. With nothing typed, the commands are
// under their groups' headings, as in the menu; what is typed puts them
// best first, where a group no longer holds them together.
func (p *palette) rows(hits []paletteEntry) []paletteRow {
	grouped := strings.TrimSpace(p.input.Value()) == ""
	var rows []paletteRow
	for n, e := range hits {
		if grouped && (n == 0 || hits[n-1].group != e.group) {
			rows = append(rows, paletteRow{heading: e.group})
		}
		rows = append(rows, paletteRow{n: n})
	}
	return rows
}

// scroll moves the rows shown only when the cursor would leave them, as
// the menu does: up at the top scrolls up, down at the bottom scrolls
// down. Going up to a group's first command shows its heading too.
func (p *palette) scroll(rows []paletteRow, room int) {
	at := 0
	for i, r := range rows {
		if r.heading == "" && r.n == p.cursor {
			at = i
			break
		}
	}
	head := at
	if at > 0 && rows[at-1].heading != "" {
		head = at - 1
	}
	if head < p.top {
		p.top = head
	}
	if at >= p.top+room {
		p.top = at - room + 1
	}
	p.top = max(0, min(p.top, len(rows)-room))
}

func (p *palette) view(m *Model, w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	lines := []string{p.input.View(), ""}
	hits := p.matches()
	if len(hits) == 0 {
		lines = append(lines, dim("  no command matches"))
	}
	room := max(h-10, 5)
	rows := p.rows(hits)
	p.scroll(rows, room)
	keysOf := func(e paletteEntry) string {
		if panelKey(e.run) {
			return e.key // no prefix
		}
		return m.pk(e.key)
	}
	// Name and keys in columns, so the descriptions line up after them.
	labelW, keysW := 0, 0
	for _, e := range hits {
		labelW = max(labelW, ansi.StringWidth(e.label))
		keysW = max(keysW, ansi.StringWidth(keysOf(e)))
	}
	lineW := max(w-12, 30)
	more := func(yes bool) {
		if len(rows) <= room {
			return // all of it shown: no room kept for the marks
		}
		if yes {
			lines = append(lines, dim("  …"))
		} else {
			lines = append(lines, "")
		}
	}
	more(p.top > 0)
	for _, r := range rows[p.top:min(len(rows), p.top+room)] {
		if r.heading != "" {
			lines = append(lines, dim(" "+r.heading))
			continue
		}
		n := r.n
		e := hits[n]
		line := padTo(e.label, labelW) + "  " + keyHint(padTo(keysOf(e), keysW)) + "  " + dim(e.desc)
		if why := m.commandOff(e.run); why != "" {
			line = dim(padTo(e.label, labelW)+"  "+padTo(keysOf(e), keysW)+"  ") + warn(why)
		}
		line = ansi.Truncate(line, lineW, "…")
		lines = append(lines, m.onClick(cursorLine(n == p.cursor, line, sel), func(m *Model) tea.Cmd {
			p.cursor = n
			return p.key(m, keyMsg("enter"))
		}))
	}
	more(p.top+room < len(rows))
	lines = append(lines, "", m.keys("", "type to filter", "", "↑↓ move", "enter", "run", "esc", "cancel"))
	return box("All commands", lines, w)
}
