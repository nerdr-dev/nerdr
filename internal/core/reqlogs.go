package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// What a client reads of the agents' own logs, read here, where the
// agents run: what was sent to one, and codex's limits and contexts. A
// client names each read by a Job of its own, and the answer by it.

func init() { registerRequests(ReadPrompts{}, ReadUsage{}) }

// ReadPrompts reads what the user asked in conversation Session of a Kind
// agent, the last PromptsKept: told as a PromptsMsg.
type ReadPrompts struct {
	Job     string
	Kind    agents.Kind
	Session string
}

// PromptsMsg is what ReadPrompts of Job read.
type PromptsMsg struct {
	Job  string
	List []agents.Prompt
}

func (q ReadPrompts) Do(m *Core) Cmd {
	return Later(func() Event {
		return PromptsMsg{Job: q.Job, List: agents.PromptsOf(q.Kind, q.Session, PromptsKept)}
	})
}

// ReadUsage reads codex's limits from its logs, and how full each of
// Sessions' context is: told as a UsageMsg.
type ReadUsage struct {
	Job      string
	Sessions []AgentSession
}

// AgentSession is agent A's conversation Session.
type AgentSession struct {
	A       TabRef
	Session string
}

// UsageMsg is what ReadUsage of Job read: codex's limits, as of At, nil
// when its logs say none; and the contexts of the conversations read.
type UsageMsg struct {
	Job      string
	Codex    *usage.Report
	At       time.Time
	Contexts []AgentUsage
}

// AgentUsage is how full agent A's context is.
type AgentUsage struct {
	A TabRef
	R usage.Report
}

func (q ReadUsage) Do(m *Core) Cmd {
	return Later(func() Event {
		msg := UsageMsg{Job: q.Job}
		if r, at, ok := usage.Codex(agents.CodexHome()); ok {
			msg.Codex, msg.At = &r, at
		}
		for _, s := range q.Sessions {
			if path := agents.CodexLog(s.Session); path != "" {
				if r, ok := usage.CodexLog(path); ok {
					msg.Contexts = append(msg.Contexts, AgentUsage{A: s.A, R: r})
				}
			}
		}
		return msg
	})
}
