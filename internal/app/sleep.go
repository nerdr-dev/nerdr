package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// Agents asleep. An agent that is done, and has been seen, holds a process
// and a model session in memory for as long as its tab is open, though
// nobody may look at the tab for days. With sleep_after set, nerdr stops
// such an agent once its tab has been out of sight that many minutes, and
// carries its conversation on, as a restart does, when the tab is visited
// again. A tab gone cold is not started at all when nerdr starts, but
// first when it is visited.
//
// The agents of a tab sleep together, and only when each can carry its
// conversation on: an agent beside the tab's own, or one sharing its kind
// with another, that nerdr knows no conversation of would start afresh.
// Terminals are left running: what runs in them (a dev server, a watch)
// is not the agent's to stop.

// restoreStarts starts the agents of the tabs restored at start, spaced
// startSpacing apart, the tab shown first; those of tabs gone cold sleep
// until visited.
func (m *Model) restoreStarts() []tea.Cmd {
	return []tea.Cmd{m.do(core.StartRestored{Front: m.fleet.Ref(m.Cur()), Beside: m.fleet.Ref(m.splitTab())})}
}
