package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"
)

// layout is how the tabs are laid out, for the session.
func (m *Model) layout() core.Layout {
	l := core.Layout{Active: m.Active, SideWidth: m.sideWidth}
	if t := m.splitTab(); t != nil && t.ReviewOf == nil {
		l.Split, l.SplitLeft = t.ID, m.splitLeft
	}
	return l
}
