package core

// TabRef names a tab among those of every core a client shows: the core's
// ID, and the tab's own, which is only that core's. A client of one core
// alone, on the same machine, has them all with Core "".
type TabRef struct {
	Core string
	Tab  string
}

// Ref is t's TabRef.
func (m *Core) Ref(t *Tab) TabRef { return TabRef{Core: m.ID, Tab: t.ID} }

// ByRef is the tab r names, nil when it is not one of this core's.
func (m *Core) ByRef(r TabRef) *Tab {
	if r.Core != m.ID {
		return nil
	}
	for _, t := range m.Tabs {
		if t.ID == r.Tab {
			return t
		}
	}
	return nil
}
