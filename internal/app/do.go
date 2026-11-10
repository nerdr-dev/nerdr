package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
)

// do has core do r: what the user did that changes core's state rather
// than only what the TUI shows. It goes to the machine of the tab r names,
// or, naming none, to the one the user is on (forOf). With core in this
// process it is done there and then, Mu held as in Update; see
// core.Request.
func (m *Model) do(r core.Request) tea.Cmd { return m.doOn(m.forOf(r), r) }

// forOf is the machine to do r: that of the tab or pane it names, else
// that of the dialog open or the active tab (dialogOn).
func (m *Model) forOf(r core.Request) *fleet.Machine {
	if mc := m.fleet.Naming(r); mc != nil {
		return mc
	}
	return m.dialogOn()
}

// doOn has the core of mc do r, as do.
func (m *Model) doOn(mc *fleet.Machine, r core.Request) tea.Cmd {
	if mc.Conn != nil {
		return m.doRemote(mc, r)
	}
	return fromCore(r.Do(&mc.Core))
}

// saveLayout has core save the session, with how the TUI lays out the
// tabs now.
func (m *Model) saveLayout() {
	for _, mc := range m.fleet.Live() {
		l := m.layoutOn(mc)
		m.doOn(mc, core.Save{Layout: &l})
	}
}
