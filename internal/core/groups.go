package core

import (
	"path/filepath"
	"sort"

	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Groups lists the groups that have tabs: repositories alphabetically,
// multi last.
func (l TabList) Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range l {
		g := t.GroupName()
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == MultiGroup) != (out[j] == MultiGroup) {
			return out[j] == MultiGroup
		}
		return out[i] < out[j]
	})
	return out
}

// GroupTabs is the indexes into l of the tabs in group g, in order.
func (l TabList) GroupTabs(g string) []int {
	var out []int
	for i, t := range l {
		if t.GroupName() == g {
			out = append(out, i)
		}
	}
	return out
}

// GroupStatus is the light a group shows: that of the tab in it that most
// needs attention, so a question in a group not in view is still seen.
func (l TabList) GroupStatus(g string) Status {
	best := StExited
	for _, i := range l.GroupTabs(g) {
		if s := l[i].Lit(); StatusRank(s) > StatusRank(best) {
			best = s
		}
	}
	return best
}

// GroupCount is how many of group g's tabs are lit st, for its entry to
// say so beside its own light, which is one tab's: a done tab's green
// outranks working ones, and alone reads as if the group were all done.
func (l TabList) GroupCount(g string, st Status) int {
	n := 0
	for _, i := range l.GroupTabs(g) {
		if l[i].Lit() == st {
			n++
		}
	}
	return n
}

// RememberGroup is the group of a tab on wts in dir: group when one is
// given, which is then kept for dir, else the one kept for it. Only
// worktrees in several repositories have one.
func (m *Core) RememberGroup(dir, group string, wts []workspace.Worktree) string {
	if len(wts) < 2 {
		return ""
	}
	if group == "" {
		return m.GroupNames[dir]
	}
	if m.GroupNames == nil {
		m.GroupNames = map[string]string{}
	}
	m.GroupNames[dir] = group
	return group
}

// KnownGroupNames is the names given to groups, of open tabs and of
// worktrees kept, sorted.
func (m *Core) KnownGroupNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(g string) {
		if g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	for _, t := range m.Tabs {
		if len(t.Worktrees) > 1 {
			add(t.Group)
		}
	}
	for _, g := range m.GroupNames {
		add(g)
	}
	sort.Strings(out)
	return out
}

const MultiGroup = "multi"

// group is the name of the group a tab belongs to.
func (t *Tab) GroupName() string {
	switch len(t.Worktrees) {
	case 0:
		return filepath.Base(t.Dir)
	case 1:
		return filepath.Base(t.Worktrees[0].Repo)
	}
	if t.Group != "" {
		return t.Group
	}
	return MultiGroup
}
