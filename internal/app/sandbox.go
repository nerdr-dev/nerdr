package app

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/core"
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

// sandboxedRepos are the main checkouts of t's repositories that nerdr
// holds back in, an agent having run in a sandbox there: no fetch or push,
// and no hooks or setup from their code.
func (m *Model) sandboxedRepos(t *core.Tab) []string {
	if t == nil {
		return nil
	}
	c := &m.machineOf(t).Core
	var out []string
	for _, w := range t.Owner().Worktrees {
		if w.Repo != "" && c.SandboxedRepos[w.Repo] && !slices.Contains(out, w.Repo) {
			out = append(out, w.Repo)
		}
	}
	return out
}

// askForgetSandboxed asks whether nerdr is to forget that agents have run
// in a sandbox in repos, the repositories of tab t, saying what that lets
// nerdr do there again.
func (m *Model) askForgetSandboxed(t *core.Tab, repos []string) tea.Cmd {
	c := &m.machineOf(t).Core
	var lines []string
	for _, r := range repos {
		l := r
		if from := c.SandboxSources[r]; from != "" {
			l += dim("  (" + from + ")")
		}
		lines = append(lines, l)
	}
	lines = append(lines, "",
		"An agent has run in a sandbox here. What it wrote, nerdr does not",
		"run as you, outside the sandbox: so nerdr does not fetch or push in",
		"this repository, and runs no Git hooks or setup on that code.",
		"",
		"Forget it only when you trust what is in the repository and its",
		"worktrees now. nerdr then works there as in any other, until an",
		"agent runs in a sandbox there again.")
	m.dialog = &confirm{
		title:     "Forget sandbox history?",
		lines:     lines,
		defaultNo: true,
		yesLabel:  "forget",
		yes: func(m *Model) tea.Cmd {
			return m.doOn(m.machineOf(t), core.ForgetSandboxed{Repos: repos})
		},
	}
	return nil
}
