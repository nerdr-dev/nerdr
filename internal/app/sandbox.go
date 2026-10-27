package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
)

// An agent whose kind is set sandboxed = true runs in a sandbox its
// command puts it in, and nerdr is not to be its way out. nerdr runs as
// the user, outside every sandbox, so what it does when an agent asks is
// done with everything the user can do. Such an agent is confined, and
// nerdr refuses it
//
//   - a terminal pane (`nerdr terminal`): the user's shell, outside the
//     sandbox;
//   - an agent, a tab or a spun-off part run by a kind that is not
//     sandboxed: it would run outside, with the task the confined agent
//     wrote;
//   - a message to an agent not in a sandbox, which could be told to run
//     what the confined one cannot;
//   - reading a terminal or an agent outside a sandbox, and the other tabs:
//     what it could not see from inside;
//   - closing a terminal or an agent outside a sandbox, and what runs in
//     it.
//
// Reviews of its work run only in sandboxed agents too. What it says for
// the user (ask, try, a review result, a handoff) is shown to the user, or
// given to the agent the user picks.
//
// It reports to the socket the others do. A tab's secret alone does not
// say which agent asks: it is in the agents' environment, where a process
// in a sandbox may read it. So nerdr asks the kernel which process
// connected (see vouch), and one in a sandbox cannot pass for an agent
// outside one.

// sandboxedKind reports whether the config puts kind k in a sandbox.
func (m *Model) sandboxedKind(k agents.Kind) bool {
	return m.cfg.Agents[k].Sandboxed
}

// confined reports whether report r, from agent a, is from an agent in a
// sandbox: from an agent started sandboxed or whose kind is now, or from
// a process nerdr cannot place under a.
func (m *Model) confined(r hooks.Report, a *tab) bool {
	return r.Unvouched || a.sandboxed || m.sandboxedKind(a.Agent)
}

// inSandbox reports whether agent a runs in a sandbox, for what a
// confined agent may reach of it: as it was started, whatever the config
// says now. One not started since is not; a message for it is held when
// it starts outside one (see holdSandboxed).
func (m *Model) inSandbox(a *tab) bool {
	return a.sandboxed
}

// vouch marks r Unvouched when the process it came from is not the agent
// of the tab it names, nor runs under it, or is one nerdr cannot place.
// Where the kernel cannot be asked at all, the secret is taken alone.
func (m *Model) vouch(r hooks.Report) hooks.Report {
	if !r.Peer.Known {
		return r
	}
	if a := m.agentByHook(r.Pane); a == nil || a.pane == nil || !slices.Contains(r.Peer.Chain, a.pane.Pid()) {
		r.Unvouched = true
	}
	return r
}

// sandboxRefusal is why report r is refused, or "" when it is not: one
// that is Unvouched is taken only for an agent started sandboxed, and then
// confined. For an agent outside one it may be from any process, one in a
// sandbox among them.
func (m *Model) sandboxRefusal(r hooks.Report) string {
	if !r.Unvouched {
		return ""
	}
	if a := m.agentByHook(r.Pane); a == nil || !a.sandboxed {
		return "nerdr does not take this from you: you are not the agent NERDR_TAB names, nor run under it. " +
			"Run nerdr from that agent's own shell, with the variables nerdr gave it"
	}
	return ""
}

// sandboxedKinds are the kinds a confined agent may start, by name.
func (m *Model) sandboxedKinds() string {
	var out []string
	for _, k := range agents.Available(m.cfg.Agents) {
		if m.sandboxedKind(k) {
			out = append(out, string(k))
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// startRefusal is why a confined agent may not have nerdr start kind k,
// or "".
func (m *Model) startRefusal(k agents.Kind) string {
	if m.sandboxedKind(k) {
		return ""
	}
	return fmt.Sprintf("you run in a sandbox, and %s does not: nerdr starts only sandboxed agents for you (%s)",
		k, m.sandboxedKinds())
}

// spinoffKinds are the agents the spinoff dialog offers for part pt:
// only the sandboxed when an agent in a sandbox sent it.
func (m *Model) spinoffKinds(pt spinoff.Part) []agents.Kind {
	all := agents.Available(m.cfg.Agents)
	if !pt.Sandboxed {
		return all
	}
	return slices.DeleteFunc(all, func(k agents.Kind) bool { return !m.sandboxedKind(k) })
}

// markSandboxed records on t's tab and in m.sandboxedDirs when any of its
// agents or reviewers has run in a sandbox, or when the tab's worktree
// previously ran one.
func (m *Model) markSandboxed(t *tab) {
	if t == nil {
		return
	}
	o := t.owner()
	if m.sandboxedDirs[o.Dir] {
		o.Sandboxed = true
	}
	for _, w := range o.Worktrees {
		if m.sandboxedDirs[w.Path] {
			o.Sandboxed = true
		}
	}
	for _, a := range o.agentsIn() {
		if a.sandboxed || a.Sandboxed {
			a.Sandboxed = true
			o.Sandboxed = true
		}
	}
	if o.Sandboxed {
		if m.sandboxedDirs == nil {
			m.sandboxedDirs = map[string]bool{}
		}
		if o.Dir != "" {
			m.sandboxedDirs[o.Dir] = true
		}
		for _, w := range o.Worktrees {
			if w.Path != "" {
				m.sandboxedDirs[w.Path] = true
			}
		}
	}
}

// hasSandboxed reports whether any agent of t's tab runs or ran in a sandbox.
func (t *tab) hasSandboxed() bool {
	if t == nil {
		return false
	}
	o := t.owner()
	if o.Sandboxed || o.sandboxed {
		return true
	}
	for _, a := range o.agentsIn() {
		if a.sandboxed || a.Sandboxed {
			return true
		}
	}
	return false
}

// holdSandboxed keeps the messages agents in a sandbox wrote for t from
// being typed in by themselves, t having started outside one: they wait
// in F3 for the user.
func (m *Model) holdSandboxed(t *tab) {
	held := 0
	for i, msg := range t.Messages {
		if msg.Sandboxed && msg.Auto {
			t.Messages[i].Auto = false
			held++
		}
	}
	if held > 0 {
		m.saveSession()
		m.toast(fmt.Sprintf("%s: %d message(s) from an agent in a sandbox wait in F3, %s not being in one",
			t.Name, held, t.Agent))
	}
}

// noTerminal is what a confined agent is told when it asks for a
// terminal pane.
const noTerminal = "you run in a sandbox, and a terminal pane would not: " +
	"nerdr does not run commands outside the sandbox for you. Run it in your own shell, " +
	"or ask the user to run it"
