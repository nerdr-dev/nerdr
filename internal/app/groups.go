package app

import (
	"path/filepath"
	"sort"

	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// Tabs are grouped by the repository their worktree was made from, so the
// work on one codebase stands together. A tab spanning several
// repositories goes in the group named when it was made, or in multiGroup.

const multiGroup = "multi"

// showGroups is whether the bars at the top have the repository row above
// the tab row: with more than one repository, unless put away with the
// prefix and b, or shown in the sidebar. Without it, the tab row shows the
// repository's name, and nerdr's messages on its right.
func (m *Model) showGroups() bool {
	return !m.hideGroups && len(m.groups()) > 1 && m.sideW() == 0
}

// barRows is how many rows the bars take, and tabRow the one the tabs are on.
func (m *Model) barRows() int {
	if m.showGroups() {
		return 2
	}
	return 1
}

func (m *Model) tabRow() int { return m.barRows() - 1 }

// group is the name of the group a tab belongs to.
func (t *tab) group() string {
	switch len(t.Worktrees) {
	case 0:
		return filepath.Base(t.Dir)
	case 1:
		return filepath.Base(t.Worktrees[0].Repo)
	}
	if t.Group != "" {
		return t.Group
	}
	return multiGroup
}

// groups lists the groups that have tabs: repositories alphabetically,
// multi last.
func (m *Model) groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range m.tabs {
		g := t.group()
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == multiGroup) != (out[j] == multiGroup) {
			return out[j] == multiGroup
		}
		return out[i] < out[j]
	})
	return out
}

// groupTabs is the indexes into m.tabs of the tabs in group g, in order.
func (m *Model) groupTabs(g string) []int {
	var out []int
	for i, t := range m.tabs {
		if t.group() == g {
			out = append(out, i)
		}
	}
	return out
}

// curGroup is the active tab's group.
func (m *Model) curGroup() string {
	if t := m.cur(); t != nil {
		return t.group()
	}
	return ""
}

// selectInGroup goes to the n-th tab (0-based) of the active group; a
// negative n counts from the end.
func (m *Model) selectInGroup(n int) tea.Cmd {
	idx := m.groupTabs(m.curGroup())
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
	for _, g := range m.groups() {
		idx = append(idx, m.groupTabs(g)...)
	}
	for pos, i := range idx {
		if i == m.active {
			return m.selectTab(idx[(pos+delta+len(idx))%len(idx)])
		}
	}
	return nil
}

// stepInGroup moves to the next or previous tab of the active group,
// wrapping round within it.
func (m *Model) stepInGroup(delta int) tea.Cmd {
	idx := m.groupTabs(m.curGroup())
	for pos, i := range idx {
		if i == m.active {
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
	gs := m.groups()
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
	idx := m.groupTabs(g)
	if len(idx) == 0 {
		return -1
	}
	want := m.lastInGroup[g]
	for _, i := range idx {
		if m.tabs[i].ID == want {
			return i
		}
	}
	return idx[0]
}

// groupStatus is the light a group shows: that of the tab in it that most
// needs attention, so a question in a group not in view is still seen.
func (m *Model) groupStatus(g string) status {
	best := stExited
	for _, i := range m.groupTabs(g) {
		if s := m.tabs[i].lit(); statusRank(s) > statusRank(best) {
			best = s
		}
	}
	return best
}

// groupCount is how many of group g's tabs are lit st, for its entry to
// say so beside its own light, which is one tab's: a done tab's green
// outranks working ones, and alone reads as if the group were all done.
func (m *Model) groupCount(g string, st status) int {
	n := 0
	for _, i := range m.groupTabs(g) {
		if m.tabs[i].lit() == st {
			n++
		}
	}
	return n
}

// rememberGroup is the group of a tab on wts in dir: group when one is
// given, which is then kept for dir, else the one kept for it. Only
// worktrees in several repositories have one.
func (m *Model) rememberGroup(dir, group string, wts []workspace.Worktree) string {
	if len(wts) < 2 {
		return ""
	}
	if group == "" {
		return m.groupNames[dir]
	}
	if m.groupNames == nil {
		m.groupNames = map[string]string{}
	}
	m.groupNames[dir] = group
	return group
}

// knownGroupNames is the names given to groups, of open tabs and of
// worktrees kept, sorted.
func (m *Model) knownGroupNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(g string) {
		if g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	for _, t := range m.tabs {
		if len(t.Worktrees) > 1 {
			add(t.Group)
		}
	}
	for _, g := range m.groupNames {
		add(g)
	}
	sort.Strings(out)
	return out
}
