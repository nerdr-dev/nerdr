package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// sentDialog is the prefix and a h, or a click on last: in the panel:
// what you sent the agent, the latest first, one row
// each, and the one picked with ↑ ↓ under them; enter shows it alone, in
// full, to scroll through. c copies it, and its text can be selected
// with the mouse. It lists what
// nerdr kept of the tab's prompts, and Claude's own log of them once that
// is read. Any other key closes it.
type sentDialog struct {
	t    *tab
	list []state.Prompt // the oldest first, as the tab keeps them
	pick int            // counted from the latest
	// reading is set while Claude's log is read for the list.
	reading bool
	// full is set while the prompt picked is shown alone, from row off.
	full bool
	off  int
	// sel is the text of the prompt picked, as drawn.
	sel textSel
}

// sentMsg is the list of t's prompts read from its agent's log.
type sentMsg struct {
	t    *tab
	list []agents.Prompt
}

// sentShownFull is the most rows the prompt picked takes.
const sentShownFull = 10

func (m *Model) openSent() tea.Cmd { return m.openSentOf(m.curAgent()) }

// openSentOf opens the list of what was sent to agent t.
func (m *Model) openSentOf(t *tab) tea.Cmd {
	if t == nil || !m.live(t) {
		return nil
	}
	d := &sentDialog{t: t, list: t.Prompts}
	if len(d.list) == 0 && strings.TrimSpace(t.Last) != "" {
		// From before nerdr kept the list.
		d.list = []state.Prompt{{Text: t.Last}}
	}
	m.dialog = d
	if t.Agent != agents.Claude || t.SessionID == "" {
		return nil
	}
	d.reading = true
	kind, id := t.Agent, t.SessionID
	return func() tea.Msg { return sentMsg{t: t, list: agents.PromptsOf(kind, id, promptsKept)} }
}

// onSent takes the list read from the log into the dialog, if it is
// still open on that tab. The log has every prompt, typed in nerdr or not.
func (m *Model) onSent(msg sentMsg) {
	d, ok := m.dialog.(*sentDialog)
	if !ok || d.t != msg.t {
		return
	}
	d.reading = false
	if len(msg.list) == 0 {
		return
	}
	d.list = nil
	for _, p := range msg.list {
		// The agent can write its log: see package clean.
		if text := strings.TrimSpace(clean.Text(p.Text)); text != "" {
			d.list = append(d.list, state.Prompt{Text: text, At: p.At})
		}
	}
	d.pick = min(d.pick, max(len(d.list)-1, 0))
}

func (d *sentDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if d.full {
		switch msg.String() {
		case "up", "k":
			d.off--
		case "down", "j":
			d.off++
		case "pgup":
			d.off -= 10
		case "pgdown", " ":
			d.off += 10
		case "home", "g":
			d.off = 0
		case "end", "G":
			d.off = 1 << 20
		case "enter", "esc", "q":
			d.full = false
		case "c", "y":
			m.copyText(d.at(d.pick).Text)
		}
		d.off = max(d.off, 0) // and at most the end, as drawn
		return nil
	}
	switch msg.String() {
	case "enter":
		if len(d.list) > 0 {
			d.full, d.off = true, 0
		}
	case "up", "k":
		d.move(-1)
	case "down", "j":
		d.move(1)
	case "pgup":
		d.move(-10)
	case "pgdown":
		d.move(10)
	case "home", "g":
		d.pick = 0
	case "end", "G":
		d.move(len(d.list))
	case "c", "y":
		if len(d.list) > 0 {
			m.copyText(d.at(d.pick).Text)
		}
	default:
		m.dialog = nil
	}
	return nil
}

func (d *sentDialog) move(by int) {
	d.pick = max(0, min(len(d.list)-1, d.pick+by))
}

// at is the prompt i rows from the latest.
func (d *sentDialog) at(i int) state.Prompt { return d.list[len(d.list)-1-i] }

func (d *sentDialog) view(m *Model, w, h int) string {
	inner := boxInner(w)
	if d.full && len(d.list) > 0 {
		return d.fullView(m, inner, w, h)
	}
	var lines []string
	if len(d.list) == 0 {
		lines = append(lines, dim("Nothing sent yet."))
	}
	// The rows around the one picked, as many as leave room for it in full.
	rows := max(min(len(d.list), h-sentShownFull-10), 3)
	first := max(min(d.pick-rows/2, len(d.list)-rows), 0)
	for i := first; i < min(first+rows, len(d.list)); i++ {
		p := d.at(i)
		text := truncName(strings.Join(strings.Fields(p.Text), " "), max(inner-16, 10))
		row := dim(padTo(sentTime(p.At), 12)) + " " + text
		if i == d.pick {
			lines = append(lines, "❯ "+lipgloss.NewStyle().Bold(true).Render(row))
			continue
		}
		i := i
		lines = append(lines, "  "+m.onClick(row, func(m *Model) tea.Cmd { d.pick = i; return nil }))
	}
	if len(d.list) > 0 {
		lines = append(lines, "", dim(strings.Repeat("─", min(inner, 60))))
		d.sel.setText(d.at(d.pick).Text, inner)
		d.sel.scroll = nil
		lines = append(lines, m.selRows(&d.sel, 0, sentShownFull)...)
		if n := len(d.sel.rows); n > sentShownFull {
			lines = append(lines, dim(fmt.Sprintf("… %d rows more: enter shows it all", n-sentShownFull)))
		}
	}
	lines = append(lines, "")
	if d.reading {
		lines = append(lines, dim("Reading Claude's log for the rest…"), "")
	}
	lines = append(lines, m.button("↑↓", "pick")+"  "+m.button("enter", "in full")+"  "+
		m.button("c", "copy")+"  "+m.button("esc", "close"))
	return box("Sent to "+m.agentName(d.t), lines, w)
}

// fullView is the prompt picked alone, as many of its rows as fit from off.
func (d *sentDialog) fullView(m *Model, inner, w, h int) string {
	p := d.at(d.pick)
	d.sel.setText(p.Text, inner)
	n := len(d.sel.rows)
	rows := max(h-8, 3)
	d.off = min(d.off, max(n-rows, 0))
	d.sel.scroll = func(by int) { d.off = max(min(d.off+by, n-rows), 0) }
	lines := m.selRows(&d.sel, d.off, d.off+rows)
	where := "all of it"
	if n > rows {
		where = fmt.Sprintf("rows %d–%d of %d", d.off+1, d.off+len(lines), n)
	}
	lines = append(lines, "", dim(sentTime(p.At)+" · "+where), "",
		m.button("↑↓", "scroll")+"  "+m.button("c", "copy")+"  "+m.button("enter", "back to the list"))
	return box("Sent to "+m.agentName(d.t), lines, w)
}

// sentTime is when a prompt was sent, as short as is clear: the time
// alone today.
func sentTime(at time.Time) string {
	switch {
	case at.IsZero():
		return ""
	case at.Local().Format("2006-01-02") == time.Now().Format("2006-01-02"):
		return at.Local().Format("15:04")
	}
	return at.Local().Format("2 Jan 15:04")
}
