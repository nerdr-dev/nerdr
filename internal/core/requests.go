package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
)

// The requests a client may make of core; see Request. Each is done with
// Mu held, the tabs it names looked up afresh.

func init() {
	registerRequests(
		Save{},
		SetConfig{},
	)
}

// SetConfig is the config as the client read it from its file: core
// goes by the client's own settings in it from now on (config.Merge).
// Core's own it never takes from a client, whose file may be on another
// machine: it has them from the file where it runs (see EditConfig).
type SetConfig struct {
	Cfg config.Config
}

func (q SetConfig) Do(m *Core) Cmd {
	m.takeConfig(config.Merge(m.Cfg, q.Cfg))
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

func init() { registerRequests(CheckPrograms{}, Detect{}, KeepAwake{}) }

// CheckPrograms looks at the terminals' and plugin screens' programs, for
// those that have ended: once a frame.
type CheckPrograms struct{}

func (CheckPrograms) Do(m *Core) Cmd {
	m.CheckShells()
	m.CheckPluginUIs()
	return nil
}

// Detect looks at every agent now.
type Detect struct{}

func (Detect) Do(m *Core) Cmd { return m.DetectAll(time.Now()) }

// KeepAwake keeps the machine awake while keep_awake asks, the client's
// user having last typed at LastInput.
type KeepAwake struct{ LastInput time.Time }

func (q KeepAwake) Do(m *Core) Cmd {
	m.KeepAwake(time.Now(), func() time.Time { return q.LastInput })
	return nil
}
