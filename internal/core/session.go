package core

import (
	"os"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
)

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

// Restore takes up session sess, as read when nerdr starts: its tabs,
// their agents not started yet, and what was known of the pull requests,
// the issues, the groups and where agents ran in a sandbox. now is when
// it starts.
func (m *Core) Restore(sess state.Session, now time.Time) {
	for _, st := range sess.Tabs {
		t := &Tab{AgentState: Agent{Tab: st, Light: StStarting}}
		TrustWorktrees(t)
		t.TakeQueue()
		m.RestoreAgents(t)
		if t.LastActive.IsZero() {
			t.LastActive = now // from before it was kept: not cold on that account
		}
		m.Tabs = append(m.Tabs, t)
	}
	m.LoadPRWatch(sess.PRs)
	m.LoadIssueWatch(sess.Issues)
	m.GroupNames = sess.GroupNames
	m.SandboxedDirs = sess.SandboxedDirs
	if m.GroupNames == nil {
		m.GroupNames = map[string]string{}
	}
	if m.SandboxedDirs == nil {
		m.SandboxedDirs = map[string]bool{}
	} else {
		for dir := range m.SandboxedDirs {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				delete(m.SandboxedDirs, dir)
			}
		}
	}
	for _, t := range m.Tabs {
		m.MarkSandboxed(t)
	}
}
