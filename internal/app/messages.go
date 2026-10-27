package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's F3 screen: the tab's messages for its agent (see
// queue.go). What comes to mind while the agent works is written down
// here, without being sent: once it has finished, and you have answered
// what it said, you put a message in with enter, to change it or send it
// as it is. A message ticked to go by itself is typed in the next time the
// agent is idle, in order, as the queue always was.

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
	if n == 0 {
		return ""
	}
	var parts []string
	if kept := n - auto; kept > 0 {
		parts = append(parts, plural(kept, "task")+" waiting for you to put in")
	}
	if auto > 0 {
		parts = append(parts, plural(auto, "task")+" sent by itself when the agent is idle")
	}
	return strings.Join(parts, " · ") + " · " + m.screenKey(3)
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
	m.msgCursor = min(max(m.msgCursor, noMessage), len(t.Messages)-1)
	i := m.msgCursor
	has := i >= 0 && i < len(t.Messages)
	switch k {
	case "up", "k":
		m.msgCursor = max(i-1, noMessage)
	case "down", "j":
		m.msgCursor = min(i+1, len(t.Messages)-1)
	case "K", "shift+up":
		if has && i > 0 {
			t.Messages[i-1], t.Messages[i] = t.Messages[i], t.Messages[i-1]
			m.msgCursor--
			m.saveSession()
		}
	case "J", "shift+down":
		if has && i+1 < len(t.Messages) {
			t.Messages[i+1], t.Messages[i] = t.Messages[i], t.Messages[i+1]
			m.msgCursor++
			m.saveSession()
		}
	case " ", "x":
		if has {
			t.Messages[i].Auto = !t.Messages[i].Auto
			m.saveSession()
		}
	case "enter":
		if has {
			return m.putIn(t, i)
		}
		if len(t.Messages) > 0 {
			m.toast("no task picked: ↓ picks one, esc goes to the agent")
		}
	case "e":
		if has {
			return m.editMessage(t, i)
		}
	case "y", "c":
		if has {
			m.copyText(t.Messages[i].Text)
		}
	case "d", "delete", "backspace":
		if has {
			t.dropMessage(i)
			m.msgCursor = min(i, len(t.Messages)-1)
			m.saveSession()
		}
	}
	return nil
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

// editMessage opens message i of t to be changed.
func (m *Model) editMessage(t *tab, i int) tea.Cmd {
	cmd := m.openMessage(false)
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		return cmd
	}
	msg := t.Messages[i]
	d.editing = &msg
	d.auto = msg.Auto
	d.input.SetValue(msg.Text)
	d.fit(m)
	return cmd
}

// messagesBody draws t's messages, w×h, for the panel's frame.
func (m *Model) messagesBody(t *tab, w, h int) []string {
	textW := max(w-8, 10) // cursor, box and margins
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	lines := []string{m.f3Views(), ""}
	if len(t.Messages) == 0 {
		lines = append(lines, "", " "+dim("No tasks yet."), "",
			" "+dim("Line up tasks for the agent while it works."),
			" "+dim("Send them yourself, or let nerdr send them when the agent is idle."))
	}
	m.msgCursor = min(max(m.msgCursor, noMessage), len(t.Messages)-1)
	if len(t.Messages) > 0 {
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
	cursorAt, cursorRows := 0, 0
	for i, msg := range t.Messages {
		rows := wrapRows(msg.Text, textW, messageRows)
		if i == m.msgCursor {
			cursorAt = len(lines)
			rows = wrapLines(msg.Text, textW, room)
			cursorRows = len(rows)
		}
		on := i == m.msgCursor && m.focus == focusReview
		mark := "  "
		if on {
			mark = sel.Render("❯ ")
		}
		box := m.onClick(checkbox(msg.Auto), func(m *Model) tea.Cmd {
			m.msgCursor = i
			return m.messagesKey(keyMsg(" "))
		})
		for j, r := range rows {
			if on {
				r = sel.Render(r)
			}
			text := m.onClick(r, func(m *Model) tea.Cmd {
				m.msgCursor = i
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
