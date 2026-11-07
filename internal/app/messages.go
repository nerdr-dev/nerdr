package app

import (
	"strconv"

	"github.com/nerdr-dev/nerdr/internal/core"
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

// panelMessages reports whether the review panel shows the messages.
func (m *Model) panelMessages() bool { return m.reviewOpen && m.review.Messages() }

// openMessages shows the messages in the review panel, with the keyboard.
func (m *Model) openMessages() tea.Cmd {
	m.reviewOpen = true
	m.saved.on = false
	cmd := m.reviewKey(tea.KeyMsg{Type: tea.KeyF3})
	m.setFocus(core.FocusReview)
	return tea.Batch(cmd, m.resize())
}

// messagesKey handles a key while the messages have the keyboard.
func (m *Model) messagesKey(msg tea.KeyMsg) tea.Cmd {
	t := m.CurAgent()
	k := msg.String()
	switch k {
	case "f1", "f2", "f3", "f4", "f5", "m":
		// m steps through the panel's screens, as in the others.
		return m.reviewKey(msg)
	case "esc", "q":
		m.setFocus(core.FocusAgent)
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
	o := t.Owner()
	refs := o.TaskRefs()
	m.msgCursor = min(max(m.msgCursor, core.NoMessage), len(refs)-1)
	i := m.msgCursor
	has := i >= 0 && i < len(refs)
	var r core.TaskRef
	var list *[]state.Message
	if has {
		r = refs[i]
		list = r.List(o)
	}
	switch k {
	case "up", "k":
		m.msgCursor = max(i-1, core.NoMessage)
	case "down", "j":
		m.msgCursor = min(i+1, len(refs)-1)
	case "K", "shift+up":
		// Within the agent's own list: which agent it is for is e's.
		if has && i > 0 && refs[i-1].A == r.A {
			m.msgCursor--
			return m.do(core.MoveMessage{M: m.MsgRefOf(o, r), By: -1})
		}
	case "J", "shift+down":
		if has && i+1 < len(refs) && refs[i+1].A == r.A {
			m.msgCursor++
			return m.do(core.MoveMessage{M: m.MsgRefOf(o, r), By: 1})
		}
	case " ", "x":
		switch {
		case has && r.A == nil:
			m.InactiveToast(o.Inactive[r.I])
		case has:
			return m.do(core.ToggleAuto{M: m.MsgRefOf(o, r)})
		}
	case "enter":
		switch {
		case has && r.A == nil:
			m.InactiveToast(o.Inactive[r.I])
		case has:
			return m.putIn(r.A, r.I)
		case len(refs) > 0:
			m.toast("no task picked: ↓ picks one, esc goes to the agent")
		}
	case "e":
		if has {
			return m.editMessage(o, r)
		}
	case "y", "c":
		if has {
			m.copyText((*list)[r.I].Text)
		}
	case "d", "delete", "backspace":
		if has {
			m.msgCursor = min(i, len(refs)-2)
			return m.do(core.DropMessage{M: m.MsgRefOf(o, r)})
		}
	}
	return nil
}

// putIn types message i into t's agent, as a paste and without enter, and
// gives the agent the keyboard: to change it, or send it as it is.
func (m *Model) putIn(t *core.Tab, i int) tea.Cmd {
	return m.do(core.PutIn{M: core.MsgRef{A: m.Ref(t), Msg: t.Messages[i], I: i}})
}

// editMessage opens task r of tab o to be changed, and given to another
// agent if need be.
func (m *Model) editMessage(o *core.Tab, r core.TaskRef) tea.Cmd {
	cmd := m.openMessage(false)
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		return cmd
	}
	msg := (*r.List(o))[r.I]
	d.editing, d.editAt, d.from = &msg, r.I, r.A
	if r.A != nil {
		d.agent = r.A
	} // an inactive one goes to the agent with the keyboard, unless changed
	d.auto = msg.Auto
	d.input.SetValue(msg.Text)
	d.fit(m)
	return cmd
}

// messagesBody draws the tasks of tab o, w×h, for the panel's frame: under
// the label of the agent each is for when the tab has several.
func (m *Model) messagesBody(o *core.Tab, w, h int) []string {
	textW := max(w-8, 10) // cursor, box and margins
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	lines := []string{m.f3Views(), ""}
	refs := o.TaskRefs()
	if len(refs) == 0 {
		lines = append(lines, "", " "+dim("No tasks yet."), "",
			" "+dim("Line up tasks for the agent while it works."),
			" "+dim("Send them yourself, or let nerdr send them when the agent is idle."))
	}
	m.msgCursor = min(max(m.msgCursor, core.NoMessage), len(refs)-1)
	if len(refs) > 0 {
		mark := "  "
		if m.msgCursor == core.NoMessage && m.focus == core.FocusReview {
			mark = sel.Render("❯ ")
		}
		lines = append(lines, m.onClick(mark, func(m *Model) tea.Cmd {
			m.msgCursor = core.NoMessage
			m.setFocus(core.FocusReview)
			return nil
		}))
	}
	foot := []string{"",
		" " + dim(core.Checkbox(true)+" sent by itself, in order, when the agent is idle"),
		" " + m.msgKeys("enter", "put in", " ", "by itself", "n", "new", "e", "edit", "y", "copy", "d", "delete", "J/K", "move", "esc", "agent"),
	}
	room := max(h-len(foot), 1)
	headed := len(o.ExtraAgents()) > 0 || len(o.Inactive) > 0
	cursorAt, cursorRows := 0, 0
	for n, r := range refs {
		var msg state.Message
		if r.A == nil {
			msg = o.Inactive[r.I]
		} else {
			msg = r.A.Messages[r.I]
		}
		if headed && (n == 0 || refs[n-1].A != r.A) {
			if n > 0 {
				lines = append(lines, "")
			}
			if r.A == nil {
				lines = append(lines, " "+warn("inactive")+dim(" · their agent's pane was closed · e gives one to another"))
			} else {
				lines = append(lines, " "+light(r.A.Light)+" "+lipgloss.NewStyle().Bold(true).Render(m.AgentLabel(o, r.A)))
			}
		}
		rows := wrapRows(msg.Text, textW, core.MessageRows)
		if n == m.msgCursor {
			cursorAt = len(lines)
			rows = wrapLines(msg.Text, textW, room)
			cursorRows = len(rows)
		}
		on := n == m.msgCursor && m.focus == core.FocusReview
		mark := "  "
		if on {
			mark = sel.Render("❯ ")
		}
		box := m.onClick(core.Checkbox(msg.Auto), func(m *Model) tea.Cmd {
			m.msgCursor = n
			return m.messagesKey(keyMsg(" "))
		})
		if r.A == nil {
			box = dim("→" + strconv.Itoa(msg.Gone) + " ")
		}
		for j, row := range rows {
			switch {
			case on:
				row = sel.Render(row)
			case r.A == nil:
				row = dim(row)
			}
			text := m.onClick(row, func(m *Model) tea.Cmd {
				m.msgCursor = n
				m.setFocus(core.FocusReview)
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
