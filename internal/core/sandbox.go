package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
)

// Confined reports whether report r, from agent a, is from an agent in a
// sandbox: from an agent started sandboxed or whose kind is now
// (kindSandboxed), or from a process nerdr cannot place under a.
func Confined(r hooks.Report, a *Agent, kindSandboxed bool) bool {
	return r.Unvouched || a.InSandbox || kindSandboxed
}

// Vouch marks r Unvouched when the process it came from is not agent a, the
// one r names, nor runs under it, or is one nerdr cannot place. a is nil when
// no agent reports under that name. Where the kernel cannot be asked at all,
// the secret is taken alone. A process with the agent's id that started at
// another time is another, given the id once the agent's had ended.
func Vouch(r hooks.Report, a *Agent) hooks.Report {
	if !r.Peer.Known {
		return r
	}
	if a == nil || a.Pane == nil || !slices.Contains(r.Peer.Chain, a.Pane.Pid()) {
		r.Unvouched = true
		return r
	}
	if start := r.Peer.StartOf(a.Pane.Pid()); a.PidStart != 0 && start != a.PidStart {
		r.Unvouched = true
	}
	return r
}

// Refusal is why report r, naming agent a, is refused, or "" when it is not:
// one that is Unvouched is taken only for an agent started sandboxed, and
// then confined. For an agent outside one it may be from any process, one in a
// sandbox among them.
func Refusal(r hooks.Report, a *Agent) string {
	if !r.Unvouched {
		return ""
	}
	if a == nil || !a.InSandbox {
		return "nerdr does not take this from you: you are not the agent NERDR_TAB names, nor run under it. " +
			"Run nerdr from that agent's own shell, with the variables nerdr gave it"
	}
	return ""
}

// SandboxedKind reports whether the config puts kind k in a sandbox.
func (m *Core) SandboxedKind(k agents.Kind) bool {
	return m.Cfg.Agents[k].Sandboxed
}

// Confined reports whether report r, from agent a, is from an agent in a
// sandbox; see core.Confined.
func (m *Core) Confined(r hooks.Report, a *Tab) bool {
	return Confined(r, &a.AgentState, m.SandboxedKind(a.Agent))
}

// InSandbox reports whether agent a runs in a sandbox, for what a
// confined agent may reach of it: as it was started, whatever the config
// says now. One not started since is not; a message for it is held when
// it starts outside one (see holdSandboxed).
func (m *Core) InSandbox(a *Tab) bool {
	return a.InSandbox
}

// Vouch marks r Unvouched when the process it came from is not the agent
// of the tab it names; see core.Vouch.
func (m *Core) Vouch(r hooks.Report) hooks.Report {
	return Vouch(r, m.HookAgent(r.Pane))
}

// SandboxRefusal is why report r is refused, or "" when it is not; see
// core.Refusal.
func (m *Core) SandboxRefusal(r hooks.Report) string {
	return Refusal(r, m.HookAgent(r.Pane))
}

// HookAgent is the agent that reports as id, nil when none does.
func (m *Core) HookAgent(id string) *Agent {
	if t := m.AgentByHook(id); t != nil {
		return &t.AgentState
	}
	return nil
}

// SandboxedKinds are the kinds a confined agent may start, by name.
func (m *Core) SandboxedKinds() string {
	var out []string
	for _, k := range agents.Available(m.Cfg.Agents) {
		if m.SandboxedKind(k) {
			out = append(out, string(k))
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// StartRefusal is why a confined agent may not have nerdr start kind k,
// or "".
func (m *Core) StartRefusal(k agents.Kind) string {
	if m.SandboxedKind(k) {
		return ""
	}
	return fmt.Sprintf("you run in a sandbox, and %s does not: nerdr starts only sandboxed agents for you (%s)",
		k, m.SandboxedKinds())
}

// SpinoffKinds are the agents the spinoff dialog offers for part pt:
// only the sandboxed when an agent in a sandbox sent it.
func (m *Core) SpinoffKinds(pt spinoff.Part) []agents.Kind {
	all := agents.Available(m.Cfg.Agents)
	if !pt.Sandboxed {
		return all
	}
	return slices.DeleteFunc(all, func(k agents.Kind) bool { return !m.SandboxedKind(k) })
}

// MarkSandboxed records on t's tab and in m.sandboxedDirs when any of its
// agents or reviewers has run in a sandbox, or when the tab's worktree
// previously ran one.
func (m *Core) MarkSandboxed(t *Tab) {
	if t == nil {
		return
	}
	o := t.Owner()
	if m.SandboxedDirs[o.Dir] {
		o.Sandboxed = true
	}
	for _, w := range o.Worktrees {
		if m.SandboxedDirs[w.Path] {
			o.Sandboxed = true
		}
	}
	for _, a := range o.AgentsIn() {
		if a.InSandbox || a.Sandboxed {
			a.Sandboxed = true
			o.Sandboxed = true
		}
	}
	if o.Sandboxed {
		if m.SandboxedDirs == nil {
			m.SandboxedDirs = map[string]bool{}
		}
		if o.Dir != "" {
			m.SandboxedDirs[o.Dir] = true
		}
		for _, w := range o.Worktrees {
			if w.Path != "" {
				m.SandboxedDirs[w.Path] = true
			}
		}
	}
}

// NoTerminal is what a confined agent is told when it asks for a
// terminal pane.
const NoTerminal = "you run in a sandbox, and a terminal pane would not: " +
	"nerdr does not run commands outside the sandbox for you. Run it in your own shell, " +
	"or ask the user to run it"

// holdSandboxed keeps the messages agents in a sandbox wrote for t from
// being typed in by themselves, t having started outside one: they wait
// in F3 for the user.
func (m *Core) HoldSandboxed(t *Tab) {
	held := 0
	for i, msg := range t.Messages {
		if msg.Sandboxed && msg.Auto {
			t.Messages[i].Auto = false
			held++
		}
	}
	if held > 0 {
		m.SaveSession()
		m.Toast(fmt.Sprintf("%s: %d message(s) from an agent in a sandbox wait in F3, %s not being in one",
			t.Name, held, t.Agent))
	}
}

// HasSandboxed reports whether any agent of t's tab runs or ran in a sandbox.
func (t *Tab) HasSandboxed() bool {
	if t == nil {
		return false
	}
	o := t.Owner()
	if o.Sandboxed || o.InSandbox {
		return true
	}
	for _, a := range o.AgentsIn() {
		if a.InSandbox || a.Sandboxed {
			return true
		}
	}
	return false
}
