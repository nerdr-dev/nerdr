package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Tabs are grouped by the repository their worktree was made from, so the
// work on one codebase stands together. A tab spanning several
// repositories goes in the group named when it was made, or in multiGroup.

// showGroups is whether the bars at the top have the repository row above
// the tab row: with more than one repository, unless put away with the
// prefix and b, or shown in the sidebar. Without it, the tab row shows the
// repository's name, and nerdr's messages on its right.
func (m *Model) showGroups() bool {
	return !m.hideGroups && len(m.fleet.Groups()) > 1 && m.sideW() == 0
}

// barRows is how many rows the bars take, and tabRow the one the tabs are on.
func (m *Model) barRows() int {
	if m.showGroups() {
		return 2
	}
	return 1
}

func (m *Model) tabRow() int { return m.barRows() - 1 }

// selectInGroup goes to the n-th tab (0-based) of the active group; a
// negative n counts from the end.
func (m *Model) selectInGroup(n int) tea.Cmd {
	idx := m.fleet.GroupTabs(m.curGroup())
	if len(idx) == 0 {
		return nil
	}
	if n < 0 {
		n += len(idx)
	}
	if n < 0 || n >= len(idx) {
		return nil
	}
	return m.selectTab(idx[n])
}

// stepTab moves to the next (delta 1) or previous (-1) tab, going on into
// the next or previous group at the end of one, and wrapping round.
func (m *Model) stepTab(delta int) tea.Cmd {
	var idx []int
	for _, g := range m.fleet.Groups() {
		idx = append(idx, m.fleet.GroupTabs(g)...)
	}
	for pos, i := range idx {
		if i == m.Active {
			return m.selectTab(idx[(pos+delta+len(idx))%len(idx)])
		}
	}
	return nil
}

// stepInGroup moves to the next or previous tab of the active group,
// wrapping round within it.
func (m *Model) stepInGroup(delta int) tea.Cmd {
	idx := m.fleet.GroupTabs(m.curGroup())
	for pos, i := range idx {
		if i == m.Active {
			if len(idx) < 2 {
				return nil
			}
			return m.selectTab(idx[(pos+delta+len(idx))%len(idx)])
		}
	}
	return nil
}

// stepGroup moves to the next or previous group, landing on the tab that
// was last active in it.
func (m *Model) stepGroup(delta int) tea.Cmd {
	gs := m.fleet.Groups()
	if len(gs) < 2 {
		return nil
	}
	cur := m.curGroup()
	for pos, g := range gs {
		if g == cur {
			return m.selectGroup(gs[(pos+delta+len(gs))%len(gs)])
		}
	}
	return nil
}

// selectGroup goes to group g's last active tab, or its first.
func (m *Model) selectGroup(g string) tea.Cmd {
	return m.selectTab(m.groupTab(g))
}

// groupTab is the index of group g's last active tab, or its first; -1
// when it has none.
func (m *Model) groupTab(g string) int {
	idx := m.fleet.GroupTabs(g)
	if len(idx) == 0 {
		return -1
	}
	want := m.lastInGroup[g]
	for _, i := range idx {
		if m.fleet.Ref(m.fleet.Tabs()[i]) == want {
			return i
		}
	}
	return idx[0]
}

// curGroup is the active tab's group.
func (m *Model) curGroup() string {
	if t := m.Cur(); t != nil {
		return t.GroupName()
	}
	return ""
}
