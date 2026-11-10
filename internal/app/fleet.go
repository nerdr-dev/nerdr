package app

import (
	tea "github.com/charmbracelet/bubbletea"

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

// tellingOn is the machine whose core told what Tell takes: one in
// another process says so (told); core in the TUI's process is Home's.
func (m *Model) tellingOn() *fleet.Machine {
	if m.telling != nil {
		return m.telling
	}
	return m.fleet.Home()
}

// tabOn is the machine of the active tab: what the panels show is of it,
// the pull requests and issues, the skills and plugins. Home with no tab.
func (m *Model) tabOn() *fleet.Machine { return m.machineOf(m.Cur()) }

// dialogOn is the machine a dialog of no tab is for — a new tab, a new
// repository, search, usage, settings: the one chosen in it (ctrl+o), or
// the active tab's. What the user asks naming no tab goes there too.
func (m *Model) dialogOn() *fleet.Machine {
	if d, ok := m.dialog.(interface{ machine() *fleet.Machine }); ok {
		if mc := d.machine(); mc != nil && m.fleet.Named(mc.Name) == mc {
			return mc
		}
	}
	if mc := m.chosen; mc != nil && m.fleet.Named(mc.Name) == mc {
		return mc
	}
	return m.tabOn()
}

// machineDialog is a dialog of no tab that can be moved to another
// machine: reopen opens it anew on dialogOn, its lists read from there.
type machineDialog interface {
	reopen(m *Model) tea.Cmd
}

// nextMachine moves the dialog open to the next machine of the fleet's
// that is not grey, when it can be moved: ctrl+o.
func (m *Model) nextMachine() (tea.Cmd, bool) {
	d, ok := m.dialog.(machineDialog)
	ms := m.fleet.Machines()
	if !ok || len(ms) < 2 {
		return nil, false
	}
	at := m.dialogOn()
	i := 0
	for j, mc := range ms {
		if mc == at {
			i = j
		}
	}
	for k := 1; k < len(ms); k++ {
		if mc := ms[(i+k)%len(ms)]; !grey(mc) {
			m.chosen = mc
			m.fleet.Bump()
			m.dialog = nil // the next opens on mc
			return d.reopen(m), true
		}
	}
	return nil, true
}

// onTitle is title, of a dialog of no tab, naming the machine it is on
// when there are several.
func (m *Model) onTitle(title string) string {
	if len(m.fleet.Machines()) < 2 {
		return title
	}
	return title + " · on " + machineLabel(m.dialogOn())
}
