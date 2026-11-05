package core

import (
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/state"
)

const (
	// QueueQuiet is how long the agent's screen must have been still
	// before a message is typed in: an idle prompt that is still being
	// drawn may not take input yet.
	QueueQuiet = 800 * time.Millisecond
	// QueueGiveUp is how long after sending a message nerdr waits to see
	// the agent start on it before sending the next one anyway.
	QueueGiveUp = 30 * time.Second
	// AnsweredQuiet is how long an agent that has drawn something since the
	// message was sent must then have been still to count as having
	// answered it: one quick enough is never seen working.
	AnsweredQuiet = 3 * time.Second
	// EnterAfter is the pause between typing a message and pressing
	// enter, so the agent has taken the paste in as text first.
	EnterAfter = 300 * time.Millisecond
	// StallAfter is how long after enter an agent that has neither started
	// working, asked something, nor drawn anything counts as not having
	// taken the message (herdr's agent_prompt_stalled).
	StallAfter = 5 * time.Second
)

// SendEnter is the moment to press enter after a message typed in to agent ID,
// while it runs the program with generation Gen.
type SendEnter struct {
	ID  string
	Gen uint64
}

// NextAuto is the index of t's next message to be typed in by itself, or
// -1 when there is none.
func (a *Agent) NextAuto() int {
	for i, msg := range a.Messages {
		if msg.Auto {
			return i
		}
	}
	return -1
}

// AutoCount is how many of t's messages are typed in by themselves.
func (a *Agent) AutoCount() int {
	n := 0
	for _, msg := range a.Messages {
		if msg.Auto {
			n++
		}
	}
	return n
}

// DropMessage takes message i out of t's list.
func (a *Agent) DropMessage(i int) {
	a.Messages = append(a.Messages[:i:i], a.Messages[i+1:]...)
}

// ReadyForMessage reports whether the agent is ready to have its next auto
// message typed in; holds says something else holds the queue up.
func (a *Agent) ReadyForMessage(holds bool) bool {
	if a.NextAuto() < 0 || a.Pane == nil {
		return false
	}
	if a.Light != StIdle && a.Light != StDone {
		return false
	}
	if a.Raw != detect.Idle || a.Pane.Quiet() < QueueQuiet {
		return false
	}
	if a.AwaitWork || a.QueuePaused || holds {
		return false
	}
	exited, _, _ := a.Pane.Exited()
	return !exited
}

// TypeMessage types msg in, and returns the Cmd for pressing enter after it.
func (a *Agent) TypeMessage(msg string, now time.Time) Cmd {
	a.Pane.Input(Paste(msg))
	a.AwaitWork, a.SentAt, a.EnterAt = true, now, time.Time{}
	return After(EnterAfter, SendEnter{ID: a.ID, Gen: a.Gen})
}

// PressEnter presses enter after a message typed in to the program with
// generation gen; one since replaced gets nothing.
func (a *Agent) PressEnter(gen uint64, now time.Time) {
	if a.Pane != nil && gen == a.Gen {
		a.Pane.Input([]byte("\r"))
		a.EnterAt = now
	}
}

// CheckSent follows a message sent from the queue until the agent is seen
// taking it up: working, or asking something (Classify clears AwaitWork
// then). One that answers too fast to be seen working has drawn something
// and gone still again. One that has done nothing at all has stalled, and
// CheckSent reports that once.
func (a *Agent) CheckSent(now time.Time) (stalled bool) {
	if !a.AwaitWork || a.EnterAt.IsZero() || a.Pane == nil {
		return false
	}
	since := now.Sub(a.EnterAt)
	if since < StallAfter {
		return false
	}
	drew := a.Pane.LastOutput().After(a.EnterAt)
	switch {
	case drew && a.Pane.Quiet() >= AnsweredQuiet:
		a.AwaitWork, a.Begun = false, true // answered
	case !drew:
		a.AwaitWork = false
		a.QueuePaused = true
		return true
	case since > QueueGiveUp:
		a.AwaitWork, a.Begun = false, true // still drawing, never idle: carry on
	}
	return false
}

// Paste is text typed into an agent as a bracketed paste. The text may
// quote what others wrote (a pull request's comments): a control character
// in it would reach the agent as a key, and an end-of-paste sequence would
// end the paste early and have the rest typed in as keys, enter included.
// So only tabs and new lines are kept of the control characters, and a
// carriage return, which is enter, becomes a new line.
func Paste(text string) []byte {
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

// SendEnter presses enter after a queued message, to the agent it was typed
// in to.
func (m *Core) SendEnter(ev SendEnter) {
	for _, t := range m.AllAgents() {
		if t.ID == ev.ID && m.Live(t) {
			t.PressEnter(ev.Gen, time.Now())
			return
		}
	}
}

// CheckSent follows a message sent from the queue, and says so when the
// agent has not taken it up.
func (m *Core) CheckSent(t *Tab, now time.Time) {
	if !t.CheckSent(now) {
		return
	}
	if t.AutoCount() > 0 {
		m.Toast(t.Name + ": the agent did not take the task up — have a look; the rest of its queue waits")
	} else {
		m.Toast(t.Name + ": the agent did not take the task up — have a look")
	}
}

// NewLineHint is the hint for a new line in a newTextArea. macOS's
// Terminal sends nothing for option+enter unless option is set to act as
// meta, so ctrl+j, which works everywhere, is the one named there.
var NewLineHint = "alt+enter new line"

// Checkbox is a box, ticked or not.
func Checkbox(on bool) string {
	if on {
		return "[✓]"
	}
	return "[ ]"
}

// deliverQueue types the tab's next auto message in if the agent is ready
// for it.
func (m *Core) DeliverQueue(t *Tab, now time.Time) Cmd {
	if t.Pane != nil && !t.InSandbox {
		m.HoldSandboxed(t)
	}
	if !t.ReadyForMessage(t.ReviewHolds()) {
		return nil
	}
	i := t.NextAuto()
	msg := t.Messages[i].Text
	t.DropMessage(i)
	if t.Review == nil && codereview.IsFixPrompt(msg) {
		t.FindingsSent = true // see findingsDone
	}
	m.SetGoal(t, msg) // when it is the first the agent is asked
	m.SetLast(t, msg)
	m.Client.SaveSession()
	enter := t.TypeMessage(msg, now)
	t.NoteSaid(msg)
	if t.Owner() != m.Cur() {
		t.Owner().Away.Sent++
	}
	return enter
}

// enqueue adds a message to tabs' lists, to be typed in by itself.
func (m *Core) Enqueue(tabs []*Tab, msg string) {
	for _, t := range tabs {
		m.AddMessage(t, msg, true)
	}
}

// addMessage adds a message to the end of t's list.
func (m *Core) AddMessage(t *Tab, msg string, auto bool) {
	m.AddMessageFrom(t, msg, auto, false)
}

// addMessageFrom is addMessage, the message marked as an agent in a
// sandbox's when sandboxed is set.
func (m *Core) AddMessageFrom(t *Tab, msg string, auto, sandboxed bool) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	t.Messages = append(t.Messages, state.Message{Text: msg, Auto: auto, Sandboxed: sandboxed})
	m.Client.SaveSession()
}

// FindMessage is the index of the first of t's messages equal to msg, or
// -1 when it has gone.
func (t *Tab) FindMessage(msg state.Message) int { return IndexOf(t.Messages, msg) }

// IndexOf is the index of the first message in list equal to msg, or -1.
func IndexOf(list []state.Message, msg state.Message) int {
	return slices.Index(list, msg)
}
