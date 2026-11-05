package app

import (
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A tab has a list of messages for its agent, shown in the review panel's
// F3 screen (messages.go). An auto one is typed in for it the next time it
// is idle: that is how a tab gets its first task from the wizard, how you
// tell an agent what to do after what it is doing now without
// interrupting it, and how one message goes to several agents. The rest
// wait for you to put them in, when you have seen what the agent did.
//
// Sending follows herdr's `agent prompt`: nothing is typed into an agent
// that is asking something (blocked); the text goes in as a paste, in the
// agent's own paste mode, with enter after it; and if the agent then shows
// no sign of taking it up within stallAfter, the submission counts as
// stalled — it may be sitting unsent in the prompt — and the tab's queue
// stops until the user has looked, rather than typing the next message on
// top of it.

// messageDialog writes a message for the active tab's agent, or with
// several set, for any of the tabs.
type messageDialog struct {
	input textarea.Model
	// broadcast lists every tab to pick from; picked is by tab.
	broadcast bool
	picked    map[core.TabRef]bool
	cursor    int
	// inList is set while the keys go to the tab list, not the text.
	inList bool
	err    string
	// note says where a prepared message came from; see askAgent.
	note string
	// auto is whether the message is typed in by itself when the agent is
	// idle, or kept in F3 until it is put in; not for several tabs, where
	// it always is. editing is the message being changed, if one is.
	auto    bool
	editing *state.Message
	// from is the agent whose message editing is, nil for one of the
	// tab's inactive ones.
	from *core.Tab
	// agent is who a message not for several tabs is for: the tab's own
	// agent, or another in one of its panes; all is set when it is for
	// each of them. tab is the active tab.
	agent *core.Tab
	all   bool
	tab   *core.Tab
}

func init() {
	if runtime.GOOS == "darwin" {
		core.NewLineHint = "ctrl+j new line"
	}
}

func newTextArea(placeholder string, w int) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.CharLimit = 0
	// Its default of 99 lines cuts what is pasted or set beyond them away,
	// saying nothing: a long message changed with e would lose its end.
	ta.MaxHeight = 0
	ta.SetWidth(w)
	ta.SetHeight(5)
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ta.Focus()
	return ta
}

func (m *Model) openMessage(broadcast bool) tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	d := &messageDialog{
		input:     newTextArea("what the agent should do next…", min(max(m.width-16, 30), 90)),
		broadcast: broadcast,
		picked:    map[core.TabRef]bool{},
		auto:      m.autoMessage,
		agent:     m.CurAgent(),
		tab:       t,
	}
	if broadcast {
		// The active group to start with: a message for all of them is
		// the usual case ("rebase on main").
		for _, i := range m.GroupTabs(m.curGroup()) {
			d.picked[m.Ref(m.Tabs[i])] = true
		}
	} else {
		d.picked[m.Ref(t)] = true
	}
	m.dialog = d
	return nil
}

// choices are the agents a message not for several tabs can be for; the
// dialog asks only when there are more than one.
func (d *messageDialog) choices() []*core.Tab {
	if d.broadcast {
		return nil
	}
	return d.tab.TaskAgents()
}

// pickNext moves who the message is for on by one: through the agents in
// turn, then all of them.
func (d *messageDialog) pickNext() {
	cs := d.choices()
	at := len(cs)
	if !d.all {
		at = slices.Index(cs, d.agent)
	}
	if at++; at == len(cs) {
		d.all = true
		return
	}
	d.all, d.agent = false, cs[at%len(cs)]
}

// pickNum makes the message for the agent numbered n, saying whether
// there is one.
func (d *messageDialog) pickNum(n int) bool {
	for _, a := range d.choices() {
		if a.Num == n {
			d.all, d.agent = false, a
			return true
		}
	}
	return false
}

func (d *messageDialog) targets(m *Model) []*core.Tab {
	if !d.broadcast && d.all {
		var out []*core.Tab
		for _, a := range d.choices() {
			if m.Live(a) {
				out = append(out, a)
			}
		}
		return out
	}
	if !d.broadcast && d.agent != nil {
		if !m.Live(d.agent) {
			return nil
		}
		return []*core.Tab{d.agent}
	}
	var out []*core.Tab
	for _, t := range m.Tabs {
		if d.picked[m.Ref(t)] {
			out = append(out, t)
		}
	}
	return out
}

func (d *messageDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "tab", "shift+tab":
		if !d.broadcast {
			d.auto = !d.auto
			return nil
		}
		if d.broadcast {
			d.inList = !d.inList
			if d.inList {
				d.input.Blur()
				return nil
			}
			return d.input.Focus()
		}
	case "ctrl+t":
		if len(d.choices()) > 1 {
			d.pickNext()
			return nil
		}
	case "alt+a":
		if len(d.choices()) > 1 {
			d.all = true
			return nil
		}
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		if n, _ := strconv.Atoi(k[len("alt+"):]); len(d.choices()) > 1 && d.pickNum(n) {
			return nil
		}
	case "enter":
		if d.inList {
			break
		}
		ts := d.targets(m)
		if len(ts) == 0 {
			d.err = "pick at least one tab (tab, then space)"
			return nil
		}
		if strings.TrimSpace(d.input.Value()) == "" {
			d.err = "the task is empty"
			return nil
		}
		m.dialog = nil
		if d.broadcast {
			m.Enqueue(ts, d.input.Value())
			m.toast("queued for " + core.Plural(len(ts), "tab") + ": sent to each when it is idle")
			if d.picked[m.Ref(m.Cur())] {
				return m.openMessages()
			}
			return nil
		}
		m.autoMessage = d.auto
		if d.editing != nil {
			from := &d.tab.Inactive
			if d.from != nil {
				from = &d.from.Messages
			}
			if i := core.IndexOf(*from, *d.editing); i >= 0 {
				if len(ts) == 1 && ts[0] == d.from {
					(*from)[i] = state.Message{Text: strings.TrimSpace(d.input.Value()), Auto: d.auto}
					m.SaveSession()
					return m.openMessages()
				}
				// For another agent now: it goes to the end of its list.
				*from = core.DropFrom(*from, i)
			} // sent while it was being changed: kept as a new one
		}
		for _, t := range ts {
			m.AddMessage(t, d.input.Value(), d.auto)
		}
		who := m.AgentName(ts[0])
		if d.all && len(ts) > 1 {
			who = "all " + strconv.Itoa(len(ts)) + " agents of " + d.tab.Name
		}
		if d.auto {
			m.toast("queued for " + who + ": sent when it is idle")
		} else {
			m.toast("kept for " + who + " in " + m.screenKey(3) + ": put it in when you want")
		}
		// F3 shows it among the tab's tasks, so what became of it is seen.
		return m.openMessages()
	}
	if d.inList {
		switch k {
		case "up", "k":
			d.cursor = max(d.cursor-1, 0)
		case "down", "j":
			d.cursor = min(d.cursor+1, max(len(m.Tabs)-1, 0))
		case " ", "x", "enter":
			if d.cursor < len(m.Tabs) {
				id := m.Ref(m.Tabs[d.cursor])
				d.picked[id] = !d.picked[id]
			}
		case "a":
			all := len(d.targets(m)) < len(m.Tabs)
			for _, t := range m.Tabs {
				d.picked[m.Ref(t)] = all
			}
		}
		return nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	d.fit(m)
	return cmd
}

// fit makes the text as tall as what is written in it, from five rows up
// to half the screen, so a long message can be read as it is changed.
func (d *messageDialog) fit(m *Model) {
	w := max(d.input.Width(), 1)
	rows := 1 // the cursor's, past the end
	for _, l := range strings.Split(d.input.Value(), "\n") {
		rows += strings.Count(ansi.Wrap(l, w, ""), "\n") + 1
	}
	d.input.SetHeight(min(max(rows, 5), max(m.height/2, 5)))
}

func (d *messageDialog) view(m *Model, w, h int) string {
	var lines []string
	title := "Task"
	if d.broadcast {
		title = "Task to several tabs"
	} else if t := d.agent; t != nil {
		if d.note != "" {
			title = "Task from the review panel"
			lines = append(lines, d.note, "")
		}
		if cs := d.choices(); len(cs) > 1 {
			lines = append(lines, "For "+t.Owner().Name+", to: "+d.pickRow(m, cs))
		} else {
			lines = append(lines, "For "+t.Owner().Name+" ("+m.AgentLabel(t.Owner(), t)+").")
		}
		if d.editing != nil && d.from == nil {
			lines = append(lines, dim("It was for agent "+strconv.Itoa(d.editing.Gone)+", whose pane was closed."))
		}
		if n := len(d.tab.TaskRefs()); n > 0 && d.editing == nil {
			lines = append(lines, dim("Already in "+m.screenKey(3)+": "+core.Plural(n, "task")))
		}
		lines = append(lines, "")
	}
	lines = append(lines, m.onClick(d.input.View(), func(m *Model) tea.Cmd {
		if !d.inList {
			return nil
		}
		d.inList = false
		return d.input.Focus()
	}))
	if d.broadcast {
		lines = append(lines, "", "To:")
		sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
		for i, t := range m.Tabs {
			line := core.Checkbox(d.picked[m.Ref(t)]) + " " + light(t.Lit()) + " " + t.Name + dim("  "+t.GroupName())
			id := m.Ref(t)
			lines = append(lines, m.onClick(cursorLine(d.inList && i == d.cursor, line, sel), func(*Model) tea.Cmd {
				d.cursor = i
				d.picked[id] = !d.picked[id]
				return nil
			}))
		}
	}
	if !d.broadcast {
		lines = append(lines, "", m.onClick(core.Checkbox(d.auto)+" send by itself the next time the agent is idle", func(*Model) tea.Cmd {
			d.auto = !d.auto
			return nil
		}))
		if !d.auto {
			lines = append(lines, dim("    else it waits in "+m.screenKey(3)+" until you put it in"))
		}
	}
	lines = append(lines, "")
	if d.broadcast {
		lines = append(lines, m.keys("enter", "queue", "", core.NewLineHint, "tab", "text ⇄ tabs", " ", "pick", "a", "all", "esc", "cancel"))
	} else {
		keys := []string{"enter", core.OnOff(d.editing != nil, "save", "add"), "", core.NewLineHint, "tab", "by itself on/off"}
		if len(d.choices()) > 1 {
			keys = append(keys, "ctrl+t", "to whom")
		}
		lines = append(lines, m.keys(append(keys, "esc", "cancel")...))
	}
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	return box(title, lines, w)
}

// pickRow is the agents the message can be for, and all of them, the one
// it is for marked; a click picks one.
func (d *messageDialog) pickRow(m *Model, cs []*core.Tab) string {
	on := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	item := func(label string, picked bool, pick func()) string {
		s := dim(" " + label + " ")
		if picked {
			s = on.Render("[" + label + "]")
		}
		return m.onClick(s, func(*Model) tea.Cmd {
			pick()
			return nil
		})
	}
	var parts []string
	for _, a := range cs {
		parts = append(parts, item(m.AgentLabel(d.tab, a), !d.all && a == d.agent, func() { d.all, d.agent = false, a }))
	}
	parts = append(parts, item("all", d.all, func() { d.all = true }))
	return strings.Join(parts, " ")
}
