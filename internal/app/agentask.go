package app

import (
	"path/filepath"

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
	t := m.tabFor(msg.Dir)
	if t == nil {
		m.toast("no tab works in " + msg.Dir)
		return nil
	}
	var cmd tea.Cmd
	if t != m.cur() {
		cmd = m.selectTab(m.tabIndex(t))
	}
	m.openMessage(false)
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		return cmd
	}
	d.note = dim("Read it before sending: it may quote what others wrote.")
	d.auto = true
	d.input.SetValue(msg.Text)
	d.fit(m)
	return cmd
}

// tabFor is the tab one of whose worktrees is dir, or nil.
func (m *Model) tabFor(dir string) *tab {
	dir = filepath.Clean(dir)
	for _, t := range m.tabs {
		if filepath.Clean(t.Dir) == dir {
			return t
		}
		for _, w := range t.Worktrees {
			if filepath.Clean(w.Path) == dir {
				return t
			}
		}
	}
	return nil
}
