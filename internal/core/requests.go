package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The requests a client may make of core; see Request. Each is done with
// Mu held, the tabs it names looked up afresh.

func init() {
	registerRequests(
		Save{},
		SetConfig{},
	)
}

// SetConfig is the config as the client changed it, and saved it when
// it could: core goes by it from now on. With MoveWtRoot new worktrees go
// to its worktrees_dir now, the setup having asked before any tab was
// made; else from the next start.
type SetConfig struct {
	Cfg        config.Config
	MoveWtRoot bool
}

func (q SetConfig) Do(m *Core) Cmd {
	if q.Cfg.DataDir != m.Cfg.DataDir {
		store.SetDataDir(q.Cfg.DataDir)
	}
	m.Cfg = q.Cfg
	if q.MoveWtRoot {
		m.WtRoot = workspace.WorktreesRoot(m.Root, q.Cfg.WorktreesDir)
	}
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
