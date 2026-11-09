package core

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// The agents whose commands are on the PATH, looked for on the client's
// PATH (BaseEnv), as the agents core starts get it, and kept in core's
// state for the client to read: it may be on another machine. They are
// looked for as core starts (FindAgents), and then away from the loop
// every AgentsEvery, at once when a client connects or the agents'
// config changes.

// AgentsEvery is how often the agents are looked for again: one installed
// meanwhile is then offered.
const AgentsEvery = 30 * time.Second

// agentsFoundMsg is the agents found on the PATH.
type agentsFoundMsg struct {
	kinds []agents.Kind
	at    time.Time
}

// Available is the agents whose commands are on the PATH, the ones nerdr
// knows first; see agents.Available. A core never looked for them yet
// looks now, keeping nothing: a program looks as it starts, and a client
// gets them with core's state, so this is a test's core.
func (m *Core) Available() []agents.Kind {
	if m.AgentsAt.IsZero() {
		return agents.AvailableIn(m.Cfg.Agents, envPath(m.BaseEnv()))
	}
	return m.Agents
}

// FindAgents looks for the agents now: as core starts, before its loop
// runs, without Mu.
func (m *Core) FindAgents() {
	kinds := agents.AvailableIn(m.Cfg.Agents, envPath(m.BaseEnv()))
	m.Mu.Lock()
	m.Agents, m.AgentsAt = kinds, time.Now()
	m.Mu.Unlock()
}

// LookForAgentsSoon has the agents looked for at the next InfoTick: a
// client with another PATH has come, or the config changed.
func (m *Core) LookForAgentsSoon() { m.agentsSoon = true }

// lookForAgents looks for the agents away from the loop, when it is time
// to; see AgentsEvery.
func (m *Core) lookForAgents(now time.Time) Cmd {
	if !m.agentsSoon && !m.AgentsAt.IsZero() && now.Sub(m.AgentsAt) < AgentsEvery {
		return nil
	}
	m.agentsSoon = false
	specs, path := maps.Clone(m.Cfg.Agents), envPath(m.BaseEnv())
	return func() Event { return agentsFoundMsg{agents.AvailableIn(specs, path), now} }
}

func (m *Core) onAgentsFound(ev agentsFoundMsg) {
	m.AgentsAt = ev.at
	if !slices.Equal(m.Agents, ev.kinds) {
		m.Agents = ev.kinds
	}
}

// envPath is PATH in env.
func envPath(env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], "PATH="); ok {
			return v
		}
	}
	return ""
}
