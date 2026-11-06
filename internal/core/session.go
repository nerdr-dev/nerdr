package core

import "github.com/nerdr-dev/nerdr/internal/state"

// Layout is what the session keeps of the client's: how it shows the tabs.
type Layout struct {
	Active    int
	Split     string
	SplitLeft bool
	SideWidth int
}

// SaveSession saves the tabs, with how the client lays them out.
func (m *Core) SaveSession() {
	var l Layout
	if m.Client != nil {
		l = m.Client.Layout()
	}
	s := state.Session{Root: m.Root, Active: l.Active, Split: l.Split, SplitLeft: l.SplitLeft, SideWidth: l.SideWidth, PRs: m.SavedPRWatch()}
	for _, t := range m.Tabs {
		st := t.Tab
		st.Agents = m.SavedAgents(t)
		st.Review = m.savedReview(t)
		if w := t.Handoff; w != nil {
			// The switch it is for does not outlive nerdr.
			if i := t.FindMessage(w.Asked); i >= 0 {
				st.Messages = append(append([]state.Message{}, st.Messages[:i]...), st.Messages[i+1:]...)
			}
		}
		s.Tabs = append(s.Tabs, st)
	}
	s.Issues = m.SavedIssueWatch()
	s.GroupNames = m.GroupNames
	s.SandboxedDirs = m.SandboxedDirs
	if err := state.Save(s); err != nil {
		m.Toast("could not save session: " + err.Error())
	}
}
