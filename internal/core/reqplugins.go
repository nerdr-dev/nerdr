package core

import "github.com/nerdr-dev/nerdr/internal/plugin"

// The requests about the plugins' screens and runs in the plugins pane. A
// plugin is named by its folder, and read from there again. A global
// plugin's are of no tab: T is empty, from the global tab.

func init() { registerRequests(StartPluginUI{}, StopPluginUI{}, StopPickedRun{}, PluginUISize{}) }

// StartPluginUI starts the screen of the plugin in folder Dir in tab T,
// as allowed with Key.
type StartPluginUI struct {
	T        TabRef
	Dir, Key string
}

func (q StartPluginUI) Do(m *Core) Cmd {
	if t, p, ok := m.pluginOf(q.T, q.Dir); ok {
		m.StartPluginUI(t, p, q.Key)
	}
	return nil
}

// StopPluginUI stops the screen of the plugin in folder Dir in tab T.
type StopPluginUI struct {
	T   TabRef
	Dir string
}

func (q StopPluginUI) Do(m *Core) Cmd {
	if t, p, ok := m.pluginOf(q.T, q.Dir); ok {
		m.StopPluginUI(t, p)
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
	if t, p, ok := m.pluginOf(q.T, q.Dir); ok {
		m.StopPickedRun(t, p)
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

// pluginOf is the tab r names and the plugin in folder dir, read again:
// ok when the tab is there, or for a global plugin, which needs none.
func (m *Core) pluginOf(r TabRef, dir string) (*Tab, plugin.Plugin, bool) {
	t := m.ByRef(r)
	p := plugin.Load(dir)
	return t, p, t != nil || p.IsGlobal()
}
