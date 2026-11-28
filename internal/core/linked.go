package core

import (
	"fmt"
	"slices"
	"strings"
)

// Linked tabs are a tab and the tabs its agent had made, with a spinoff or
// nerdr tab new: often the same fix in another repository. Their agents
// can send each other messages (nerdr send --tab), under the same rule as
// in a tab: what an agent in a sandbox writes reaches only agents in one.

// LinkTabs links tabs a and b, both ways.
func (m *Core) LinkTabs(a, b *Tab) {
	if a == nil || b == nil || a == b {
		return
	}
	a, b = a.Owner(), b.Owner()
	if !slices.Contains(a.Linked, b.ID) {
		a.Linked = append(a.Linked, b.ID)
	}
	if !slices.Contains(b.Linked, a.ID) {
		b.Linked = append(b.Linked, a.ID)
	}
	m.SaveSession()
}

// LinkedTabs are the tabs linked to t that are still open.
func (m *Core) LinkedTabs(t *Tab) []*Tab {
	var out []*Tab
	for _, id := range t.Owner().Linked {
		if lt := m.TabByID(id); lt != nil && lt != t.Owner() {
			out = append(out, lt)
		}
	}
	return out
}

// FindLinked is the tab linked to t named, or with the ID, name, and why
// there is none when there is not.
func (m *Core) FindLinked(t *Tab, name string) (*Tab, string) {
	linked := m.LinkedTabs(t)
	var found []*Tab
	for _, lt := range linked {
		if lt.ID == name {
			return lt, ""
		}
		if lt.Name == name {
			found = append(found, lt)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], ""
	case len(found) > 1:
		return nil, fmt.Sprintf("more than one linked tab is called %s: name it by its ID (%s)", name, tabIDs(found))
	case len(linked) == 0:
		return nil, "no tab is linked to yours: only the tabs your tab made with a spinoff or nerdr tab new, " +
			"or that made yours, can be sent to. Ask the user instead"
	}
	return nil, fmt.Sprintf("no tab linked to yours is called %s; those linked are %s", name, m.LinkedList(t))
}

// ReplyTab is the linked tab that from waits on an answer from, given
// name: the one it sent to under that name, if it is still linked, else
// the one called so now.
func (m *Core) ReplyTab(from *Tab, name string) (*Tab, string) {
	if id := from.SentTo[name]; id != "" {
		if lt, _ := m.FindLinked(from.Owner(), id); lt != nil {
			return lt, ""
		}
	}
	return m.FindLinked(from.Owner(), name)
}

func tabIDs(ts []*Tab) string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ", ")
}

// LinkedList names the tabs linked to t, with where they are and their
// agents, for an agent of t.
func (m *Core) LinkedList(t *Tab) string {
	var out []string
	for _, lt := range m.LinkedTabs(t) {
		out = append(out, fmt.Sprintf("%s, --tab %s (in %s: %s)", lt.Name, lt.ID, lt.Dir, m.AgentList(lt, nil)))
	}
	return strings.Join(out, "; ")
}
