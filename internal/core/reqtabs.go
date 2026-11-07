package core

// The requests about the tabs: their names, groups and order.

func init() {
	registerRequests(RenameTab{}, RenameGroup{}, SwapTabs{}, WakeTab{})
}

// RenameTab names tab T Name.
type RenameTab struct {
	T    TabRef
	Name string
}

func (q RenameTab) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil && q.Name != "" {
		t.Name = q.Name
		m.SaveSession()
	}
	return nil
}

// RenameGroup moves the tabs of group G that span several repositories
// into the group named Name, the multi group when it is "".
type RenameGroup struct{ G, Name string }

func (q RenameGroup) Do(m *Core) Cmd {
	moved := false
	for _, i := range m.GroupTabs(q.G) {
		t := m.Tabs[i]
		if len(t.Worktrees) < 2 {
			continue
		}
		t.Group, moved = q.Name, true
		if q.Name == "" {
			delete(m.GroupNames, t.Dir)
		} else {
			m.RememberGroup(t.Dir, q.Name, t.Worktrees)
		}
	}
	if moved {
		m.SaveSession()
	}
	return nil
}

// SwapTabs swaps the places of tabs A and B; Layout is how the client
// lays them out after, as Save has it.
type SwapTabs struct {
	A, B   TabRef
	Layout *Layout
}

func (q SwapTabs) Do(m *Core) Cmd {
	i, j := m.TabIndex(m.ByRef(q.A)), m.TabIndex(m.ByRef(q.B))
	if i < 0 || j < 0 {
		return nil
	}
	m.Tabs[i], m.Tabs[j] = m.Tabs[j], m.Tabs[i]
	return Save{Layout: q.Layout}.Do(m)
}

// WakeTab wakes tab T's agents, asleep while it was not looked at.
type WakeTab struct{ T TabRef }

func (q WakeTab) Do(m *Core) Cmd { return m.WakeTab(m.ByRef(q.T)) }
