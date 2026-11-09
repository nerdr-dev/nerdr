package core

// TabList is a row of tabs: a core's own (Core.Tabs), or those of every
// core a client shows, one core's after another. What is read of the tabs
// alone is read of a TabList, so that a client reads it the same of both.
type TabList []*Tab

// Find is the tab or agent whose ID is id, nil when none of l's is.
func (l TabList) Find(id string) *Tab {
	if id == "" {
		return nil
	}
	for _, t := range l {
		if t.ID == id {
			return t
		}
		for _, a := range t.AgentsIn() {
			if a.ID == id {
				return a
			}
		}
		for _, p := range t.Panes() {
			if p.A != nil && p.A.ID == id {
				return p.A // a reviewer starting, not yet at its seat
			}
		}
	}
	return nil
}

// FindPane is the pane whose ID is id, and the tab it is in; nils when none
// of l's is.
func (l TabList) FindPane(id string) (*Tab, *PaneTab) {
	if id == "" {
		return nil, nil
	}
	for _, t := range l {
		for _, p := range t.Panes() {
			if p.ID == id {
				return t, p
			}
		}
	}
	return nil, nil
}

// Holds reports whether t is one of l's tabs, or an agent in one.
func (l TabList) Holds(t *Tab) bool {
	for _, o := range l {
		if o == t {
			return true
		}
		for _, a := range o.AgentsIn() {
			if a == t {
				return true
			}
		}
		for _, p := range o.Panes() {
			if p.A == t {
				return true
			}
		}
	}
	return false
}

// HoldsPane reports whether p is in one of l's tabs.
func (l TabList) HoldsPane(p *PaneTab) bool {
	for _, t := range l {
		for _, o := range t.Panes() {
			if o == p {
				return true
			}
		}
	}
	return false
}

// Groups lists the groups that have tabs; see TabList.Groups.
func (m *Core) Groups() []string { return TabList(m.Tabs).Groups() }

// GroupTabs is the indexes into m.Tabs of the tabs in group g, in order.
func (m *Core) GroupTabs(g string) []int { return TabList(m.Tabs).GroupTabs(g) }

// GroupStatus is the light group g shows; see TabList.GroupStatus.
func (m *Core) GroupStatus(g string) Status { return TabList(m.Tabs).GroupStatus(g) }

// GroupCount is how many of group g's tabs are lit st.
func (m *Core) GroupCount(g string, st Status) int { return TabList(m.Tabs).GroupCount(g, st) }

// Neediest is the tab that most wants the user; see TabList.Neediest.
func (m *Core) Neediest() (int, *Tab) { return TabList(m.Tabs).Neediest() }

// Waiting are the agents that need you; see TabList.Waiting.
func (m *Core) Waiting() []*Tab { return TabList(m.Tabs).Waiting() }

// CountStatus is how many tabs have status s.
func (m *Core) CountStatus(s Status) int { return TabList(m.Tabs).CountStatus(s) }

// AllAgents are every agent nerdr runs; see TabList.AllAgents.
func (m *Core) AllAgents() []*Tab { return TabList(m.Tabs).AllAgents() }

// TabIndex is where t is in the tab row, or -1 once it has been closed.
func (m *Core) TabIndex(t *Tab) int { return TabList(m.Tabs).Index(t) }
