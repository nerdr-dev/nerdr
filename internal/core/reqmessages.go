package core

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// The requests about the messages waiting for the agents: F3's tasks.

func init() {
	registerRequests(MoveMessage{}, ToggleAuto{}, DropMessage{}, PutIn{}, TypeIn{}, EditMessage{}, AddMessages{})
}

// MsgRef names a message waiting for an agent: in agent A's list, or, with
// Inactive, in tab A's of agents whose panes were closed. It holds the
// message as the client saw it, so that one changed or moved since is not
// taken for it, and where it saw it, I, so that of two the same it is the
// one the user picked.
type MsgRef struct {
	A        TabRef
	Inactive bool
	Msg      state.Message
	I        int
}

// MsgRefOf is the MsgRef of task r of tab o.
func (m *Core) MsgRefOf(o *Tab, r TaskRef) MsgRef {
	list := *r.List(o)
	ref := MsgRef{A: m.Ref(r.A), Inactive: r.A == nil}
	if ref.Inactive {
		ref.A = m.Ref(o)
	}
	ref.I = r.I
	if r.I >= 0 && r.I < len(list) {
		ref.Msg = list[r.I]
	}
	return ref
}

// find is the agent r names, its list and where the message is in it;
// a nil list when the agent or the message is not there now.
func (r MsgRef) find(m *Core) (*Tab, *[]state.Message, int) {
	a := m.ByRef(r.A)
	if a == nil {
		return nil, nil, -1
	}
	list := &a.Messages
	if r.Inactive {
		list = &a.Inactive
	}
	i := r.I
	if i < 0 || i >= len(*list) || (*list)[i] != r.Msg {
		// Moved since the client saw it.
		i = IndexOf(*list, r.Msg)
	}
	if i < 0 {
		return a, nil, -1
	}
	return a, list, i
}

// MoveMessage moves a message up (By -1) or down (By 1) its list.
type MoveMessage struct {
	M  MsgRef
	By int
}

func (q MoveMessage) Do(m *Core) Cmd {
	_, list, i := q.M.find(m)
	if list == nil {
		return nil
	}
	j := i + q.By
	if j < 0 || j >= len(*list) {
		return nil
	}
	l := *list
	l[i], l[j] = l[j], l[i]
	m.SaveSession()
	return nil
}

// ToggleAuto has a message sent by itself when the agent is idle, or kept
// for the user to put in. One from an agent in a sandbox is no longer
// marked so once the user has it sent to an agent outside one.
type ToggleAuto struct{ M MsgRef }

func (q ToggleAuto) Do(m *Core) Cmd {
	a, list, i := q.M.find(m)
	if list == nil || q.M.Inactive {
		return nil
	}
	l := *list
	old := l[i]
	l[i].Auto = !l[i].Auto
	if l[i].Auto && !a.InSandbox {
		l[i].Sandboxed = false
	}
	if run := a.Review; run != nil && run.FixMsg == old {
		run.FixMsg = l[i]
	}
	m.SaveSession()
	return nil
}

// DropMessage deletes a message.
type DropMessage struct{ M MsgRef }

func (q DropMessage) Do(m *Core) Cmd {
	_, list, i := q.M.find(m)
	if list == nil {
		return nil
	}
	*list = DropFrom(*list, i)
	m.SaveSession()
	return nil
}

// TypedInMsg is text just typed in to agent A at the client's request, as
// a paste: the client gives it the keyboard, and presses enter after it
// when Enter says to.
type TypedInMsg struct {
	A     TabRef
	Enter bool
}

// PutIn types a waiting message in to its agent, without enter, and takes
// it from the list.
type PutIn struct{ M MsgRef }

func (q PutIn) Do(m *Core) Cmd {
	a, list, i := q.M.find(m)
	if list == nil || q.M.Inactive || !m.TypeIn(a, (*list)[i].Text) {
		return nil
	}
	a.DropMessage(i)
	m.SaveSession()
	return m.Tell(TypedInMsg{A: q.M.A})
}

// TypeIn types Text in to agent A, as a paste; the client presses enter
// after it when Enter says to.
type TypeIn struct {
	A     TabRef
	Text  string
	Enter bool
}

func (q TypeIn) Do(m *Core) Cmd {
	a := m.ByRef(q.A)
	if a == nil || !m.TypeIn(a, q.Text) {
		return nil
	}
	return m.Tell(TypedInMsg{A: q.A, Enter: q.Enter})
}

// EditMessage changes a waiting message to Text, for the agents To: in
// its place when it stays with its agent alone, else taken from its list
// and added at the end of each of theirs. One gone meanwhile, sent, is
// added anew.
type EditMessage struct {
	M    MsgRef
	To   []TabRef
	Text string
	Auto bool
}

func (q EditMessage) Do(m *Core) Cmd {
	if _, list, i := q.M.find(m); list != nil {
		if !q.M.Inactive && len(q.To) == 1 && q.To[0] == q.M.A {
			(*list)[i] = state.Message{Text: strings.TrimSpace(q.Text), Auto: q.Auto}
			m.SaveSession()
			return nil
		}
		*list = DropFrom(*list, i)
	}
	return AddMessages{To: q.To, Text: q.Text, Auto: q.Auto}.Do(m)
}

// AddMessages adds Text to the messages of each agent To: sent by itself
// when the agent is idle, with Auto, or kept for the user to put in.
type AddMessages struct {
	To   []TabRef
	Text string
	Auto bool
}

func (q AddMessages) Do(m *Core) Cmd {
	for _, r := range q.To {
		if a := m.ByRef(r); a != nil {
			m.AddMessage(a, q.Text, q.Auto)
		}
	}
	return nil
}
