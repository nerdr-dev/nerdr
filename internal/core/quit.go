package core

import "github.com/nerdr-dev/nerdr/internal/term"

func init() { registerRequests(QuitAll{}) }

// QuitAll stops every program and ends core, keeping the tabs for next
// time: quitting, as against closing a client, which leaves the agents at
// work. The session is saved with what the client last showed; the
// programs are stopped away from the loop, then the holder, and OnQuit is
// called.
type QuitAll struct{}

func (QuitAll) Do(m *Core) Cmd {
	if m.Quitting {
		return nil
	}
	m.Quitting = true
	m.SaveSession()
	panes := m.AllPanes()
	quit := m.OnQuit
	return func() Event {
		StopPanes(panes)
		m.ShutdownHolder()
		if quit != nil {
			quit()
		}
		return nil
	}
}

// AllPanes are the programs core runs: the agents', the terminals' and the
// plugins' screens. Mu is held.
func (m *Core) AllPanes() []*term.Pane {
	var panes []*term.Pane
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			if tp := p.Term(); tp != nil {
				panes = append(panes, tp)
			}
		}
	}
	return append(panes, m.PluginUIPanes()...)
}

// Unattached is the client of a core no client has connected to yet: it
// shows nothing, but the session's layout is kept as it was, for the
// session saved meanwhile and the first client to connect, and the tab the
// user was last in counts as in front.
func Unattached(l Layout, front TabRef) *RemoteClient {
	rc := &RemoteClient{state: ClientState{Layout: l, Front: front}}
	rc.gone.Store(true)
	return rc
}
