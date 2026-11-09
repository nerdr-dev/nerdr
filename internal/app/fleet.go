package app

import (
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
)

// Fleet is the cores the TUI shows, a machine's each.
func (m *Model) Fleet() *fleet.Fleet { return &m.fleet }

// home is the machine for what is of no tab: a dialog not yet asked which
// machine it is for, the TUI's own settings. This one, or the first.
func (m *Model) home() *fleet.Machine { return m.fleet.Home() }

// machineOf is the machine of tab t, or of the tab agent t is in: home
// when it is none of the fleet's, nil among them.
func (m *Model) machineOf(t *core.Tab) *fleet.Machine {
	if mc := m.fleet.Of(t); mc != nil {
		return mc
	}
	return m.home()
}

// cfg is the TUI's own settings, those of scope "client": the same in
// each machine's copy, and read of Home's. Core's own are each machine's,
// read of its copy (Core.Cfg).
func (m *Model) cfg() *config.Config { return &m.home().Core.Cfg }
