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

// Save saves the session now, with how the client lays out the tabs.
type Save struct{}

func (Save) Do(m *Core) Cmd {
	m.SaveSession()
	return nil
}
