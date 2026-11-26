package app

import (
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
)

// What the review panel wants asked of an agent — explain this line, draft
// replies to the PR's comments, fix the failing build — goes to the agent
// in the tab, as a message, rather than to an agent run on the side: the
// tab's agent is the one that is set up (and perhaps sandboxed) to work
// in the worktree. The message opens for the user to read, change and
// send, since it can quote what other people wrote.

// askAgent opens the message dialog for the tab working in msg.Dir, with
// msg.Text written in.
func (m *Model) askAgent(msg review.AgentMsg) tea.Cmd {
	mc := m.machineOf(m.panelOwner())
	if msg.OnMachine {
		// The panel may show another machine's tab by now; the message
		// is for the agent of Dir on the machine it was read from.
		if mc = m.fleet.Named(msg.Machine); mc == nil {
			m.toast("nerdr no longer shows the machine " + msg.Machine)
			return nil
		}
	}
	t := mc.Core.TabFor(msg.Dir)
	if t == nil {
		m.toast("no tab works in " + msg.Dir)
		return nil
	}
	var cmd tea.Cmd
	if t != m.Cur() {
		cmd = m.selectTab(m.fleet.TabIndex(t))
	}
	m.openMessage(false)
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		return cmd
	}
	d.note = dim("Read it before sending: it may quote what others wrote.")
	d.auto = true
	d.sent = msg.Sent
	d.input.SetValue(msg.Text)
	d.fit(m)
	return cmd
}
