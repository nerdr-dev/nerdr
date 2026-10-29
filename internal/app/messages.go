package app

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's F3 screen: the tab's messages for its agents (see
// queue.go). What comes to mind while the agent works is written down
// here, without being sent: once it has finished, and you have answered
// what it said, you put a message in with enter, to change it or send it
// as it is. A message ticked to go by itself is typed in the next time the
// agent is idle, in order, as the queue always was.
//
// Each agent of the tab keeps its own messages, and F3 lists them all,
// agent by agent. Those of an agent whose pane was closed stay, inactive,
// until they are given to another agent (e) or deleted.

// messageRows is the most rows one message takes in the list, but for the
// one under the cursor: that is shown whole, as far as the panel has room.
const messageRows = 2

// noMessage is msgCursor on the empty line above the messages, where F3
// starts: enter there puts nothing in, so an enter meant for the agent,
// pressed while F3 has the keyboard, does not send a message by mistake.
const noMessage = -1

// panelMessages reports whether the review panel shows the messages.
func (m *Model) panelMessages() bool { return m.reviewOpen && m.review.Messages() }

// openMessages shows the messages in the review panel, with the keyboard.
func (m *Model) openMessages() tea.Cmd {
	m.reviewOpen = true
	m.saved.on = false
	cmd := m.reviewKey(tea.KeyMsg{Type: tea.KeyF3})
	m.setFocus(focusReview)
	return tea.Batch(cmd, m.resize())
}

// messagesLine is the panel's line about t's messages, "" when there are
// none.
func (m *Model) messagesLine(t *tab) string {
	n, auto := len(t.Messages), t.autoCount()
	var parts []string
	if kept := n - auto; kept > 0 {
		parts = append(parts, plural(kept, "task")+" waiting for you to put in")
	}
	if auto > 0 {
		parts = append(parts, plural(auto, "task")+" sent by itself when the agent is idle")
	}
	if gone := len(t.Inactive); gone > 0 {
		parts = append(parts, plural(gone, "task")+" for a closed agent")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + " · " + m.screenKey(3)
}

// taskRef is one of the tasks F3 lists: message i of agent a, or with a
// nil, the tab's inactive message i.
type taskRef struct {
	a *tab
	i int
}

// taskAgents are the agents tab t's tasks can be for: its own, then the
// others in its panes; not its reviewers.
func (t *tab) taskAgents() []*tab { return append([]*tab{t}, t.extraAgents()...) }

// taskRefs are tab t's tasks as F3 lists them: each agent's in turn, then
// the inactive ones.
func (t *tab) taskRefs() []taskRef {
	var out []taskRef
	for _, a := range t.taskAgents() {
		for i := range a.Messages {
			out = append(out, taskRef{a: a, i: i})
		}
	}
	for i := range t.Inactive {
		out = append(out, taskRef{i: i})
	}
	return out
}

// list is the list of tab o that r is in.
func (r taskRef) list(o *tab) *[]state.Message {
	if r.a == nil {
		return &o.Inactive
	}
	return &r.a.Messages
}

// keepTasks moves the messages of agent a, whose pane is closing, to tab
// t's inactive ones, and says how many there were.
func (t *tab) keepTasks(a *tab) int {
	n := len(a.Messages)
	for _, msg := range a.Messages {
		msg.Gone = a.Num
		t.Inactive = append(t.Inactive, msg)
	}
	a.Messages = nil
	return n
}

// dropFrom is list without message i.
func dropFrom(list []state.Message, i int) []state.Message {
	return append(list[:i:i], list[i+1:]...)
}

// messagesKey handles a key while the messages have the keyboard.
func (m *Model) messagesKey(msg tea.KeyMsg) tea.Cmd {
	t := m.curAgent()
	k := msg.String()
	switch k {
	case "f1", "f2", "f3", "f4", "f5", "m":
		// m steps through the panel's screens, as in the others.
		return m.reviewKey(msg)
	case "esc", "q":
		m.setFocus(focusAgent)
		return m.resize()
	case "right", "l":
		m.showSaved(true)
		return nil
	case "left", "h":
		m.showSaved(false)
		return nil
	}
	if m.saved.on {
		return m.savedKey(msg)
	}
	switch k {
	case "n", "a":
		return m.openMessage(false)
	}
	if t == nil {
		return nil
	}
	o := t.owner()
	refs := o.taskRefs()
	m.msgCursor = min(max(m.msgCursor, noMessage), len(refs)-1)
	i := m.msgCursor
	has := i >= 0 && i < len(refs)
	var r taskRef
	var list *[]state.Message
	if has {
		r = refs[i]
		list = r.list(o)
	}
	switch k {
	case "up", "k":
		m.msgCursor = max(i-1, noMessage)
	case "down", "j":
		m.msgCursor = min(i+1, len(refs)-1)
	case "K", "shift+up":
		// Within the agent's own list: which agent it is for is e's.
		if has && i > 0 && refs[i-1].a == r.a {
			l := *list
			l[r.i-1], l[r.i] = l[r.i], l[r.i-1]
			m.msgCursor--
			m.saveSession()
		}
	case "J", "shift+down":
		if has && i+1 < len(refs) && refs[i+1].a == r.a {
			l := *list
			l[r.i+1], l[r.i] = l[r.i], l[r.i+1]
			m.msgCursor++
			m.saveSession()
		}
	case " ", "x":
		switch {
		case has && r.a == nil:
			m.inactiveToast(o.Inactive[r.i])
		case has:
			a := r.a
			old := a.Messages[r.i]
			a.Messages[r.i].Auto = !a.Messages[r.i].Auto
			if a.Messages[r.i].Auto && !a.sandboxed {
				a.Messages[r.i].Sandboxed = false
			}
			if run := a.review; run != nil && run.fixMsg == old {
				run.fixMsg = a.Messages[r.i]
			}
			m.saveSession()
		}
	case "enter":
		switch {
		case has && r.a == nil:
			m.inactiveToast(o.Inactive[r.i])
		case has:
			return m.putIn(r.a, r.i)
		case len(refs) > 0:
			m.toast("no task picked: ↓ picks one, esc goes to the agent")
		}
	case "e":
		if has {
			return m.editMessage(o, r)
		}
	case "y", "c":
		if has {
			m.copyText((*list)[r.i].Text)
		}
	case "d", "delete", "backspace":
		if has {
			*list = dropFrom(*list, r.i)
			m.msgCursor = min(i, len(refs)-2)
			m.saveSession()
		}
	}
	return nil
}

// inactiveToast says why an inactive message is not put in or sent.
func (m *Model) inactiveToast(msg state.Message) {
	m.toast("for agent " + strconv.Itoa(msg.Gone) + ", whose pane was closed: e gives it to another agent")
}

// putIn types message i into t's agent, as a paste and without enter, and
// gives the agent the keyboard: to change it, or send it as it is.
func (m *Model) putIn(t *tab, i int) tea.Cmd {
	if !m.typeIn(t, t.Messages[i].Text) {
		return nil
	}
	t.dropMessage(i)
	m.saveSession()
	// The keyboard goes to the agent it was put in, whichever pane had it.
	cmd := m.goToAgent(t)
	m.toast("put in: enter sends it")
	return tea.Batch(cmd, m.resize())
}

// typeIn types text into t's agent as a paste, without enter, unless it
// is not running or is asking something, which it says.
func (m *Model) typeIn(t *tab, text string) bool {
	if t.pane == nil || t.status == stExited {
		m.toast(t.Name + ": the agent is not running")
		return false
	}
	if t.status == stBlocked {
		// What is typed now answers its question.
		m.toast(t.Name + ": the agent is asking something — answer it first")
		return false
	}
	t.noteSaid(text)
	b := paste(text)
	t.pane.Input(b)
	if t.typed != nil {
		t.typed.feed(b) // for the panel, once it is sent
	}
	return true
}

// editMessage opens task r of tab o to be changed, and given to another
// agent if need be.
func (m *Model) editMessage(o *tab, r taskRef) tea.Cmd {
	cmd := m.openMessage(false)
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		return cmd
	}
	msg := (*r.list(o))[r.i]
	d.editing, d.from = &msg, r.a
	if r.a != nil {
		d.agent = r.a
	} // an inactive one goes to the agent with the keyboard, unless changed
	d.auto = msg.Auto
	d.input.SetValue(msg.Text)
	d.fit(m)
	return cmd
}

// messagesBody draws the tasks of tab o, w×h, for the panel's frame: under
// the label of the agent each is for when the tab has several.
func (m *Model) messagesBody(o *tab, w, h int) []string {
	textW := max(w-8, 10) // cursor, box and margins
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	lines := []string{m.f3Views(), ""}
	refs := o.taskRefs()
	if len(refs) == 0 {
		lines = append(lines, "", " "+dim("No tasks yet."), "",
			" "+dim("Line up tasks for the agent while it works."),
			" "+dim("Send them yourself, or let nerdr send them when the agent is idle."))
	}
	m.msgCursor = min(max(m.msgCursor, noMessage), len(refs)-1)
	if len(refs) > 0 {
		mark := "  "
		if m.msgCursor == noMessage && m.focus == focusReview {
			mark = sel.Render("❯ ")
		}
		lines = append(lines, m.onClick(mark, func(m *Model) tea.Cmd {
			m.msgCursor = noMessage
			m.setFocus(focusReview)
			return nil
		}))
	}
	foot := []string{"",
		" " + dim(checkbox(true)+" sent by itself, in order, when the agent is idle"),
		" " + m.msgKeys("enter", "put in", " ", "by itself", "n", "new", "e", "edit", "y", "copy", "d", "delete", "J/K", "move", "esc", "agent"),
	}
	room := max(h-len(foot), 1)
	headed := len(o.extraAgents()) > 0 || len(o.Inactive) > 0
	cursorAt, cursorRows := 0, 0
	for n, r := range refs {
		var msg state.Message
		if r.a == nil {
			msg = o.Inactive[r.i]
		} else {
			msg = r.a.Messages[r.i]
		}
		if headed && (n == 0 || refs[n-1].a != r.a) {
			if n > 0 {
				lines = append(lines, "")
			}
			if r.a == nil {
				lines = append(lines, " "+warn("inactive")+dim(" · their agent's pane was closed · e gives one to another"))
			} else {
				lines = append(lines, " "+light(r.a.status)+" "+lipgloss.NewStyle().Bold(true).Render(m.agentLabel(o, r.a)))
			}
		}
		rows := wrapRows(msg.Text, textW, messageRows)
		if n == m.msgCursor {
			cursorAt = len(lines)
			rows = wrapLines(msg.Text, textW, room)
			cursorRows = len(rows)
		}
		on := n == m.msgCursor && m.focus == focusReview
		mark := "  "
		if on {
			mark = sel.Render("❯ ")
		}
		box := m.onClick(checkbox(msg.Auto), func(m *Model) tea.Cmd {
			m.msgCursor = n
			return m.messagesKey(keyMsg(" "))
		})
		if r.a == nil {
			box = dim("→" + strconv.Itoa(msg.Gone) + " ")
		}
		for j, row := range rows {
			switch {
			case on:
				row = sel.Render(row)
			case r.a == nil:
				row = dim(row)
			}
			text := m.onClick(row, func(m *Model) tea.Cmd {
				m.msgCursor = n
				m.setFocus(focusReview)
				return nil
			})
			if j == 0 {
				lines = append(lines, mark+box+" "+text)
			} else {
				lines = append(lines, "      "+text)
			}
		}
	}
	// Keep the message under the cursor in sight above the keys.
	if cursorAt+cursorRows > room {
		lines = lines[min(cursorAt+cursorRows-room, len(lines)):]
	}
	body := fitLines(lines, w, room)
	for i := range foot {
		foot[i] = ansi.Truncate(foot[i], w, "…")
	}
	return append(body, foot...)
}

// msgKeys is keys' row of buttons for the messages screen, whose keys are
// not a dialog's.
func (m *Model) msgKeys(pairs ...string) string {
	var out string
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += dim(" · ")
		}
		k, what := pairs[i], pairs[i+1]
		label := k
		if k == " " {
			label = "space"
		}
		key := k
		if k == "J/K" {
			out += keyHint(label) + " " + dim(what)
			continue
		}
		out += m.onClick(keyHint(label)+" "+dim(what), func(m *Model) tea.Cmd { return m.messagesKey(keyMsg(key)) })
	}
	return out
}
