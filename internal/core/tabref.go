package core

// TabRef names a tab among those of every core a client shows: the core's
// ID, and the tab's own, which is only that core's. A client of one core
// alone, on the same machine, has them all with Core "". The zero TabRef is
// no tab. Any agent is named so: a tab's own, another in its panes, or a
// reviewer.
type TabRef struct {
	Core string
	Tab  string
}

// PaneRef names a pane the same way: the core's ID, and the pane's own.
type PaneRef struct {
	Core string
	Pane string
}

// Ref is t's TabRef; the zero one for nil. A tab or agent with no id yet, as
// one from an old session may be, is given one.
func (m *Core) Ref(t *Tab) TabRef {
	if t == nil {
		return TabRef{}
	}
	if t.ID == "" {
		t.ID = NewTabID()
	}
	return TabRef{Core: m.ID, Tab: t.ID}
}

// PaneRef is p's PaneRef; the zero one for nil.
func (m *Core) PaneRef(p *PaneTab) PaneRef {
	if p == nil {
		return PaneRef{}
	}
	if p.ID == "" {
		p.ID = NewTabID()
	}
	return PaneRef{Core: m.ID, Pane: p.ID}
}

// ByRef is the tab or agent r names, nil when it is not one of this core's.
func (m *Core) ByRef(r TabRef) *Tab {
	if r.Core != m.ID || r.Tab == "" {
		return nil
	}
	for _, t := range m.Tabs {
		if t.ID == r.Tab {
			return t
		}
		for _, a := range t.AgentsIn() {
			if a.ID == r.Tab {
				return a
			}
		}
		for _, p := range t.Panes() {
			if p.A != nil && p.A.ID == r.Tab {
				return p.A // a reviewer starting, not yet at its seat
			}
		}
	}
	return nil
}

// PaneByRef is the pane r names, and the tab it is in; nils when it is not
// one of this core's.
func (m *Core) PaneByRef(r PaneRef) (*Tab, *PaneTab) {
	if r.Core != m.ID || r.Pane == "" {
		return nil, nil
	}
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			if p.ID == r.Pane {
				return t, p
			}
		}
	}
	return nil, nil
}
