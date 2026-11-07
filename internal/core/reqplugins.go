package core

import "github.com/nerdr-dev/nerdr/internal/plugin"

// The requests about the plugins' screens and runs in F5. A plugin is
// named by its folder, and read from there again.

func init() { registerRequests(StartPluginUI{}, StopPluginUI{}, StopPickedRun{}, PluginUISize{}) }

// StartPluginUI starts the screen of the plugin in folder Dir in tab T,
// as allowed with Key.
type StartPluginUI struct {
	T        TabRef
	Dir, Key string
}

func (q StartPluginUI) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		m.StartPluginUI(t, plugin.Load(q.Dir), q.Key)
	}
	return nil
}

// StopPluginUI stops the screen of the plugin in folder Dir in tab T.
type StopPluginUI struct {
	T   TabRef
	Dir string
}

func (q StopPluginUI) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		m.StopPluginUI(t, plugin.Load(q.Dir))
	}
	return nil
}

// StopPickedRun stops the run of the plugin in folder Dir by an agent of
// tab T.
type StopPickedRun struct {
	T   TabRef
	Dir string
}

func (q StopPickedRun) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		m.StopPickedRun(t, plugin.Load(q.Dir))
	}
	return nil
}

// PluginUISize is how big the client shows a plugin's screen: the next
// one starts at it.
type PluginUISize struct{ W, H int }

func (q PluginUISize) Do(m *Core) Cmd {
	m.PluginUIW, m.PluginUIH = q.W, q.H
	return nil
}
