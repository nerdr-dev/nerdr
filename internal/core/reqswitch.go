package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// The requests that switch an agent to another kind; see agentswitch.go.

func init() { registerRequests(SwitchAgent{}, AskHandoff{}, CancelHandoff{}) }

// SwitchAgent switches agent A to Kind: with Handoff, giving the new one
// the handoff the old one wrote, if it still stands.
type SwitchAgent struct {
	A       TabRef
	Kind    agents.Kind
	Handoff bool
}

func (q SwitchAgent) Do(m *Core) Cmd {
	a := m.ByRef(q.A)
	if a == nil || !m.Live(a) {
		return nil
	}
	text := ""
	if q.Handoff {
		text = a.ReadyHandoff(time.Now())
	}
	return m.SwitchAgent(a, q.Kind, text)
}

// AskHandoff asks agent A for a handoff, then switches it to Kind.
type AskHandoff struct {
	A    TabRef
	Kind agents.Kind
}

func (q AskHandoff) Do(m *Core) Cmd {
	if a := m.ByRef(q.A); a != nil && m.Live(a) {
		m.AskHandoff(a, q.Kind)
	}
	return nil
}

// CancelHandoff gives up the switch of agent A waiting for its handoff.
type CancelHandoff struct{ A TabRef }

func (q CancelHandoff) Do(m *Core) Cmd {
	if a := m.ByRef(q.A); a != nil {
		m.CancelHandoff(a)
	}
	return nil
}
