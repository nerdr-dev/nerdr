package core

import (
	"strings"
	"time"
)

// The requests about where the user is looking: the tab they come to and
// the one they leave, the agent they see; and the note they leave on a
// tab.

func init() { registerRequests(SwitchTab{}, NoteTab{}) }

// SwitchTab is the user leaving tab Left for tab To, with tab Beside
// shown beside it: the one left is recorded as it is, for its summary
// when they come back; the one come to is summed up, and its agents, and
// those of the one beside, woken.
type SwitchTab struct{ Left, To, Beside TabRef }

func (q SwitchTab) Do(m *Core) Cmd {
	now := time.Now()
	var cmds []Cmd
	if old := m.ByRef(q.Left); old != nil && m.TabIndex(old) >= 0 {
		cmds = append(cmds, m.Leave(old, now))
		old.FocusVisit = &FocusVisit{}
		cmds = append(cmds, CaptureFocus(old, old.FocusVisit, false))
	}
	t := m.ByRef(q.To)
	if t == nil {
		return Batch(cmds...)
	}
	cmds = append(cmds, m.Enter(t, now), m.LookUpGoal(t), m.WakeTab(t))
	if s := m.ByRef(q.Beside); s != nil && s != t {
		cmds = append(cmds, m.WakeTab(s))
	}
	t.Unseen = nil
	if t.FocusVisit != nil {
		cmds = append(cmds, CaptureFocus(t, t.FocusVisit, true))
	}
	return Batch(cmds...)
}

// NoteTab leaves Note on tab T, for the user to see when they come back.
type NoteTab struct {
	T    TabRef
	Note string
}

func (q NoteTab) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil && m.TabIndex(t) >= 0 {
		t.Note = strings.TrimSpace(q.Note)
		m.SaveSession()
		return m.Tell(PanesMsg{}) // the panel above the agent may have changed height
	}
	return nil
}

// UnseenMsg is a change in what tab T changed while the user was away,
// for the review panel to mark.
type UnseenMsg struct{ T TabRef }

// OnFocusFiles takes what a tab's worktrees held when the user left it,
// or came back to it: once both are known, the files changed between are
// those changed while they were away.
func (m *Core) OnFocusFiles(msg FocusFilesMsg) Cmd {
	t := msg.T
	if m.TabIndex(t) < 0 || t.FocusVisit != msg.Visit {
		return nil
	}
	v := msg.Visit
	if msg.Returning {
		v.After = msg.Files
	} else {
		v.Before = msg.Files
	}
	if v.Before == nil || v.After == nil {
		return nil
	}
	t.Unseen = map[string]map[string]bool{}
	for p, before := range v.Before {
		after, ok := v.After[p]
		if !ok {
			continue
		}
		t.Unseen[p] = map[string]bool{}
		for f, stamp := range after {
			if before[f] != stamp {
				t.Unseen[p][f] = true
			}
		}
		for f := range before {
			if _, ok := after[f]; !ok {
				t.Unseen[p][f] = true
			}
		}
	}
	return m.Tell(UnseenMsg{T: m.Ref(t)})
}
