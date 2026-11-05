package app

import (
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
)

// A tab's agent can be swapped for another in the same worktree — claude
// for opencode, say — without closing the tab. The agent that is stopped
// keeps its conversation in the tab, so switching back continues it.
//
// The agent taking over is told what the old one did (internal/handoff).
// The user chooses whether a running agent is first asked to write a
// handoff. Without one, the new agent reads the old one's conversation
// from its log. With one asked for, the switch waits for it — but not for
// long, and not at all for an agent that cannot: one that answers without
// sending a handoff (out of tokens, say), does not take the message up, or
// has stopped is switched from at once, as if none had been asked for.

// openSwitchAgent offers the other agents on PATH in place of the agent
// whose pane has the keyboard, or, while a switch is waiting for a
// handoff, not to wait.
func (m *Model) openSwitchAgent() tea.Cmd {
	t := m.CurAgent()
	if t == nil {
		return nil
	}
	if w := t.Handoff; w != nil {
		m.dialog = &menu{title: "Switch " + m.AgentName(t) + " to " + string(w.To), x: m.width / 3, y: m.height / 3, hi: 1, items: []menuItem{
			{label: "Waiting for " + string(t.Agent) + "'s handoff"},
			{label: "Switch now: " + string(w.To) + " reads " + string(t.Agent) + "'s conversation", key: "1",
				run: func(m *Model) tea.Cmd { return fromCore(m.SwitchAgent(t, w.To, "")) }},
			{label: "Cancel the switch", key: "2",
				run: func(m *Model) tea.Cmd { m.CancelHandoff(t); return nil }},
		}}
		return nil
	}
	var items []menuItem
	for _, k := range agents.Available(m.Cfg.Agents) {
		if k == t.Agent {
			continue
		}
		label := string(k)
		_, visited := t.Sessions[k]
		if t.Sessions[k] != "" || (k == agents.Codex && visited) {
			label += " · continues its conversation"
		}
		items = append(items, menuItem{label: label, key: strconv.Itoa(len(items) + 1),
			run: func(m *Model) tea.Cmd { return m.pickAgent(t, k) }})
	}
	if len(items) == 0 {
		m.toast("no other agent on PATH")
		return nil
	}
	head := "Stops " + string(t.Agent) + "; its conversation is kept"
	if t.ReadyHandoff(time.Now()) != "" {
		head = "Stops " + string(t.Agent) + "; the next agent gets the handoff it wrote"
	}
	items = append([]menuItem{{label: head}}, items...)
	m.dialog = &menu{title: "Switch " + m.AgentName(t) + " to", items: items, x: m.width / 3, y: m.height / 3, hi: 1}
	return nil
}

// pickAgent switches t to k with the handoff its agent already wrote, or
// with its agent running, asks whether to ask it for one first; else it
// switches at once.
func (m *Model) pickAgent(t *core.Tab, k agents.Kind) tea.Cmd {
	if !m.Live(t) || k == t.Agent {
		return nil
	}
	if text := t.ReadyHandoff(time.Now()); text != "" {
		return fromCore(m.SwitchAgent(t, k, text))
	}
	if !t.AgentRunning() || t.QueuePaused {
		return fromCore(m.SwitchAgent(t, k, ""))
	}
	// Asking costs the old agent a turn and the user a wait, and is no use
	// when it is out of tokens: the user knows which, nerdr does not.
	old := t.Agent
	m.dialog = &menu{title: "Switch " + m.AgentName(t) + " to " + string(k), x: m.width / 3, y: m.height / 3, hi: 1, items: []menuItem{
		{label: string(k) + " is told what " + string(old) + " did"},
		{label: "Switch now: " + string(k) + " reads " + string(old) + "'s conversation", key: "1",
			run: func(m *Model) tea.Cmd { return fromCore(m.SwitchAgent(t, k, "")) }},
		{label: "Ask " + string(old) + " for a handoff first", key: "2",
			run: func(m *Model) tea.Cmd { m.AskHandoff(t, k); return nil }},
	}}
	return nil
}
