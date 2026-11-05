package core

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// messageRows is the most rows one message takes in the list, but for the
// one under the cursor: that is shown whole, as far as the panel has room.
const MessageRows = 2

// noMessage is msgCursor on the empty line above the messages, where F3
// starts: enter there puts nothing in, so an enter meant for the agent,
// pressed while F3 has the keyboard, does not send a message by mistake.
const NoMessage = -1

// messagesLine is the panel's line about t's messages, "" when there are
// none.
func (m *Core) MessagesLine(t *Tab) string {
	n, auto := len(t.Messages), t.AutoCount()
	var parts []string
	if kept := n - auto; kept > 0 {
		parts = append(parts, Plural(kept, "task")+" waiting for you to put in")
	}
	if auto > 0 {
		parts = append(parts, Plural(auto, "task")+" sent by itself when the agent is idle")
	}
	if gone := len(t.Inactive); gone > 0 {
		parts = append(parts, Plural(gone, "task")+" for a closed agent")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + " · " + m.Client.ScreenKey(3)
}

// dropFrom is list without message i.
func DropFrom(list []state.Message, i int) []state.Message {
	return append(list[:i:i], list[i+1:]...)
}

// inactiveToast says why an inactive message is not put in or sent.
func (m *Core) InactiveToast(msg state.Message) {
	m.Toast("for agent " + strconv.Itoa(msg.Gone) + ", whose pane was closed: e gives it to another agent")
}

// typeIn types text into t's agent as a paste, without enter, unless it
// is not running or is asking something, which it says.
func (m *Core) TypeIn(t *Tab, text string) bool {
	if t.Pane == nil || t.Light == StExited {
		m.Toast(t.Name + ": the agent is not running")
		return false
	}
	if t.Light == StBlocked {
		// What is typed now answers its question.
		m.Toast(t.Name + ": the agent is asking something — answer it first")
		return false
	}
	t.NoteSaid(text)
	b := Paste(text)
	t.Pane.Input(b)
	if t.Typed != nil {
		t.Typed.Feed(b) // for the panel, once it is sent
	}
	return true
}

// TaskRef is one of the tasks F3 lists: message i of agent a, or with a
// nil, the tab's inactive message i.
type TaskRef struct {
	A *Tab
	I int
}

// TaskAgents are the agents tab t's tasks can be for: its own, then the
// others in its panes; not its reviewers.
func (t *Tab) TaskAgents() []*Tab { return append([]*Tab{t}, t.ExtraAgents()...) }

// TaskRefs are tab t's tasks as F3 lists them: each agent's in turn, then
// the inactive ones.
func (t *Tab) TaskRefs() []TaskRef {
	var out []TaskRef
	for _, a := range t.TaskAgents() {
		for i := range a.Messages {
			out = append(out, TaskRef{A: a, I: i})
		}
	}
	for i := range t.Inactive {
		out = append(out, TaskRef{I: i})
	}
	return out
}

// List is the list of tab o that r is in.
func (r TaskRef) List(o *Tab) *[]state.Message {
	if r.A == nil {
		return &o.Inactive
	}
	return &r.A.Messages
}

// KeepTasks moves the messages of agent a, whose pane is closing, to tab
// t's inactive ones, and says how many there were.
func (t *Tab) KeepTasks(a *Tab) int {
	n := len(a.Messages)
	for _, msg := range a.Messages {
		msg.Gone = a.Num
		t.Inactive = append(t.Inactive, msg)
	}
	a.Messages = nil
	return n
}
