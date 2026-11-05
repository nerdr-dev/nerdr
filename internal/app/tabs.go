package app

import (
	"github.com/nerdr-dev/nerdr/internal/state"
)

// saveSession writes the open tabs so the next start restores them.
func (m *Model) saveSession() {
	s := state.Session{Root: m.Root, Active: m.Active}
	if t := m.splitTab(); t != nil && t.ReviewOf == nil {
		s.Split, s.SplitLeft = t.ID, m.splitLeft
	}
	for _, t := range m.Tabs {
		st := t.Tab
		st.Agents = t.SavedAgents()
		if w := t.Handoff; w != nil {
			// The switch it is for does not outlive nerdr.
			if i := t.FindMessage(w.Asked); i >= 0 {
				st.Messages = append(append([]state.Message{}, st.Messages[:i]...), st.Messages[i+1:]...)
			}
		}
		s.Tabs = append(s.Tabs, st)
	}
	s.PRs = m.savedPRWatch()
	s.Issues = m.SavedIssueWatch()
	s.GroupNames = m.GroupNames
	s.SandboxedDirs = m.SandboxedDirs
	s.SideWidth = m.sideWidth
	if err := state.Save(s); err != nil {
		m.toast("could not save session: " + err.Error())
	}
}
