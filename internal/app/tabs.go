package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
)

// layout is how the tabs are laid out, for the session of the core that
// asks (tellingOn).
func (m *Model) layout() core.Layout { return m.layoutOn(m.tellingOn()) }

// layoutOn is the layout as the core of mc keeps it: the tab in front and
// the one beside it by its own tabs, when they are its.
func (m *Model) layoutOn(mc *fleet.Machine) core.Layout {
	l := core.Layout{Active: -1, SideWidth: m.sideWidth}
	if cur := m.Cur(); cur != nil && m.machineOf(cur) == mc {
		l.Active = m.fleet.IndexOn(mc, cur)
	} else if len(m.fleet.Machines()) == 1 {
		l.Active = m.Active
	}
	if t := m.splitTab(); t != nil && t.ReviewOf == nil && m.machineOf(t) == mc {
		l.Split, l.SplitLeft = t.ID, m.splitLeft
	}
	return l
}
