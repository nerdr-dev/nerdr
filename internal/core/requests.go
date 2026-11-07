package core

import "github.com/nerdr-dev/nerdr/internal/config"

// The requests a client may make of core; see Request. Each is done with
// Mu held, the tabs it names looked up afresh.

func init() {
	registerRequests(
		Save{},
		SetConfig{},
	)
}

// SetConfig is the config as the client changed it, and saved it when
// it could: core goes by it from now on.
type SetConfig struct{ Cfg config.Config }

func (q SetConfig) Do(m *Core) Cmd {
	m.Cfg = q.Cfg
	return nil
}

// Save saves the session now, with how the client lays out the tabs:
// Layout, for a client that keeps it (LayoutKeeper), or as the client
// says when asked.
type Save struct{ Layout *Layout }

// LayoutKeeper is a Client that keeps the layout a client in a process of
// its own last said, for the session.
type LayoutKeeper interface{ KeepLayout(l Layout) }

func (q Save) Do(m *Core) Cmd {
	if k, ok := m.Client.(LayoutKeeper); ok && q.Layout != nil {
		k.KeepLayout(*q.Layout)
	}
	m.SaveSession()
	return nil
}
