package app

import (
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/detect"
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

const (
	// queueQuiet is how long the agent's screen must have been still
	// before a message is typed in: an idle prompt that is still being
	// drawn may not take input yet.
	queueQuiet = 800 * time.Millisecond
	// queueGiveUp is how long after sending a message nerdr waits to see
	// the agent start on it before sending the next one anyway.
	queueGiveUp = 30 * time.Second
	// answeredQuiet is how long an agent that has drawn something since the
	// message was sent must then have been still to count as having
	// answered it: one quick enough is never seen working.
	answeredQuiet = 3 * time.Second
	// enterAfter is the pause between typing a message and pressing
	// enter, so the agent has taken the paste in as text first.
	enterAfter = 300 * time.Millisecond
	// stallAfter is how long after enter an agent that has neither started
	// working, asked something, nor drawn anything counts as not having
	// taken the message (herdr's agent_prompt_stalled).
	stallAfter = 5 * time.Second
)

type sendEnterMsg struct{ t *tab }

// nextAuto is the index of t's next message to be typed in by itself, or
// -1 when there is none.
func (t *tab) nextAuto() int {
	for i, msg := range t.Messages {
		if msg.Auto {
			return i
		}
	}
	return -1
}

// autoCount is how many of t's messages are typed in by themselves.
func (t *tab) autoCount() int {
	n := 0
	for _, msg := range t.Messages {
		if msg.Auto {
			n++
		}
	}
	return n
}

// dropMessage takes message i out of t's list.
func (t *tab) dropMessage(i int) {
	t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
}

// deliverQueue types the tab's next auto message in if the agent is ready
// for it.
func (m *Model) deliverQueue(t *tab, now time.Time) tea.Cmd {
	if t.pane != nil && !t.sandboxed {
		m.holdSandboxed(t)
	}
	if t.nextAuto() < 0 || t.pane == nil {
		return nil
	}
	if t.status != stIdle && t.status != stDone {
		return nil
	}
	if t.raw != detect.Idle || t.pane.Quiet() < queueQuiet {
		return nil
	}
	if t.awaitWork || t.queuePaused || t.reviewHolds() {
		return nil
	}
	if exited, _, _ := t.pane.Exited(); exited {
		return nil
	}
	i := t.nextAuto()
	msg := t.Messages[i].Text
	t.dropMessage(i)
	if t.review == nil && codereview.IsFixPrompt(msg) {
		t.FindingsSent = true // see findingsDone
	}
	m.setGoal(t, msg) // when it is the first the agent is asked
	m.setLast(t, msg)
	m.saveSession()
	t.pane.Input(paste(msg))
	t.noteSaid(msg)
	t.awaitWork, t.sentAt, t.enterAt = true, now, time.Time{}
	if t.owner() != m.cur() {
		t.owner().away.sent++
	}
	return tea.Tick(enterAfter, func(time.Time) tea.Msg { return sendEnterMsg{t: t} })
}

// paste is text typed into an agent as a bracketed paste. The text may
// quote what others wrote (a pull request's comments): a control character
// in it would reach the agent as a key, and an end-of-paste sequence would
// end the paste early and have the rest typed in as keys, enter included.
// So only tabs and new lines are kept of the control characters, and a
// carriage return, which is enter, becomes a new line.
func paste(text string) []byte {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case r == '\n' || r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		}
		return r
	}, text)
	return []byte("\x1b[200~" + clean + "\x1b[201~")
}

// sendEnter presses enter after a queued message.
func (m *Model) sendEnter(t *tab) {
	if m.live(t) && t.pane != nil {
		t.pane.Input([]byte("\r"))
		t.enterAt = time.Now()
	}
}

// checkSent follows a message sent from the queue until the agent is seen
// taking it up: working, or asking something (classify clears awaitWork
// then). One that answers too fast to be seen working has drawn something
// and gone still again. One that has done nothing at all has stalled.
func (m *Model) checkSent(t *tab, now time.Time) {
	if !t.awaitWork || t.enterAt.IsZero() || t.pane == nil {
		return
	}
	since := now.Sub(t.enterAt)
	if since < stallAfter {
		return
	}
	drew := t.pane.LastOutput().After(t.enterAt)
	switch {
	case drew && t.pane.Quiet() >= answeredQuiet:
		t.awaitWork, t.begun = false, true // answered
	case !drew:
		t.awaitWork = false
		t.queuePaused = true
		if t.autoCount() > 0 {
			m.toast(t.Name + ": the agent did not take the task up — have a look; the rest of its queue waits")
		} else {
			m.toast(t.Name + ": the agent did not take the task up — have a look")
		}
	case since > queueGiveUp:
		t.awaitWork, t.begun = false, true // still drawing, never idle: carry on
	}
}

// enqueue adds a message to tabs' lists, to be typed in by itself.
func (m *Model) enqueue(tabs []*tab, msg string) {
	for _, t := range tabs {
		m.addMessage(t, msg, true)
	}
}

// addMessage adds a message to the end of t's list.
func (m *Model) addMessage(t *tab, msg string, auto bool) {
	m.addMessageFrom(t, msg, auto, false)
}

// addMessageFrom is addMessage, the message marked as an agent in a
// sandbox's when sandboxed is set.
func (m *Model) addMessageFrom(t *tab, msg string, auto, sandboxed bool) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	t.Messages = append(t.Messages, state.Message{Text: msg, Auto: auto, Sandboxed: sandboxed})
	m.saveSession()
}

// messageDialog writes a message for the active tab's agent, or with
// several set, for any of the tabs.
type messageDialog struct {
	input textarea.Model
	// broadcast lists every tab to pick from; picked is by tab id.
	broadcast bool
	picked    map[string]bool
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
	from *tab
	// agent is who a message not for several tabs is for: the tab's own
	// agent, or another in one of its panes; all is set when it is for
	// each of them. tab is the active tab.
	agent *tab
	all   bool
	tab   *tab
}

// newLineHint is the hint for a new line in a newTextArea. macOS's
// Terminal sends nothing for option+enter unless option is set to act as
// meta, so ctrl+j, which works everywhere, is the one named there.
var newLineHint = "alt+enter new line"

func init() {
	if runtime.GOOS == "darwin" {
		newLineHint = "ctrl+j new line"
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
	t := m.cur()
	if t == nil {
		return nil
	}
	d := &messageDialog{
		input:     newTextArea("what the agent should do next…", min(max(m.width-16, 30), 90)),
		broadcast: broadcast,
		picked:    map[string]bool{},
		auto:      m.autoMessage,
		agent:     m.curAgent(),
		tab:       t,
	}
	if broadcast {
		// The active group to start with: a message for all of them is
		// the usual case ("rebase on main").
		for _, i := range m.groupTabs(m.curGroup()) {
			d.picked[m.tabs[i].ID] = true
		}
	} else {
		d.picked[t.ID] = true
	}
	m.dialog = d
	return nil
}

// choices are the agents a message not for several tabs can be for; the
// dialog asks only when there are more than one.
func (d *messageDialog) choices() []*tab {
	if d.broadcast {
		return nil
	}
	return d.tab.taskAgents()
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

func (d *messageDialog) targets(m *Model) []*tab {
	if !d.broadcast && d.all {
		var out []*tab
		for _, a := range d.choices() {
			if m.live(a) {
				out = append(out, a)
			}
		}
		return out
	}
	if !d.broadcast && d.agent != nil {
		if !m.live(d.agent) {
			return nil
		}
		return []*tab{d.agent}
	}
	var out []*tab
	for _, t := range m.tabs {
		if d.picked[t.ID] {
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
			m.enqueue(ts, d.input.Value())
			m.toast("queued for " + plural(len(ts), "tab") + ": sent to each when it is idle")
			if d.picked[m.cur().ID] {
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
			if i := indexOf(*from, *d.editing); i >= 0 {
				if len(ts) == 1 && ts[0] == d.from {
					(*from)[i] = state.Message{Text: strings.TrimSpace(d.input.Value()), Auto: d.auto}
					m.saveSession()
					return m.openMessages()
				}
				// For another agent now: it goes to the end of its list.
				*from = dropFrom(*from, i)
			} // sent while it was being changed: kept as a new one
		}
		for _, t := range ts {
			m.addMessage(t, d.input.Value(), d.auto)
		}
		who := m.agentName(ts[0])
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
			d.cursor = min(d.cursor+1, max(len(m.tabs)-1, 0))
		case " ", "x", "enter":
			if d.cursor < len(m.tabs) {
				id := m.tabs[d.cursor].ID
				d.picked[id] = !d.picked[id]
			}
		case "a":
			all := len(d.targets(m)) < len(m.tabs)
			for _, t := range m.tabs {
				d.picked[t.ID] = all
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
			lines = append(lines, "For "+t.owner().Name+", to: "+d.pickRow(m, cs))
		} else {
			lines = append(lines, "For "+t.owner().Name+" ("+m.agentLabel(t.owner(), t)+").")
		}
		if d.editing != nil && d.from == nil {
			lines = append(lines, dim("It was for agent "+strconv.Itoa(d.editing.Gone)+", whose pane was closed."))
		}
		if n := len(d.tab.taskRefs()); n > 0 && d.editing == nil {
			lines = append(lines, dim("Already in "+m.screenKey(3)+": "+plural(n, "task")))
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
		for i, t := range m.tabs {
			line := checkbox(d.picked[t.ID]) + " " + light(t.lit()) + " " + t.Name + dim("  "+t.group())
			id := t.ID
			lines = append(lines, m.onClick(cursorLine(d.inList && i == d.cursor, line, sel), func(*Model) tea.Cmd {
				d.cursor = i
				d.picked[id] = !d.picked[id]
				return nil
			}))
		}
	}
	if !d.broadcast {
		lines = append(lines, "", m.onClick(checkbox(d.auto)+" send by itself the next time the agent is idle", func(*Model) tea.Cmd {
			d.auto = !d.auto
			return nil
		}))
		if !d.auto {
			lines = append(lines, dim("    else it waits in "+m.screenKey(3)+" until you put it in"))
		}
	}
	lines = append(lines, "")
	if d.broadcast {
		lines = append(lines, m.keys("enter", "queue", "", newLineHint, "tab", "text ⇄ tabs", " ", "pick", "a", "all", "esc", "cancel"))
	} else {
		keys := []string{"enter", onOff(d.editing != nil, "save", "add"), "", newLineHint, "tab", "by itself on/off"}
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
func (d *messageDialog) pickRow(m *Model, cs []*tab) string {
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
		parts = append(parts, item(m.agentLabel(d.tab, a), !d.all && a == d.agent, func() { d.all, d.agent = false, a }))
	}
	parts = append(parts, item("all", d.all, func() { d.all = true }))
	return strings.Join(parts, " ")
}

// findMessage is the index of the first of t's messages equal to msg, or
// -1 when it has gone.
func (t *tab) findMessage(msg state.Message) int { return indexOf(t.Messages, msg) }

// indexOf is the index of the first message in list equal to msg, or -1.
func indexOf(list []state.Message, msg state.Message) int {
	return slices.Index(list, msg)
}

// checkbox is a box, ticked or not.
func checkbox(on bool) string {
	if on {
		return "[✓]"
	}
	return "[ ]"
}
