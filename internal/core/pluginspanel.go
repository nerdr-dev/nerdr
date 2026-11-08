package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// pluginsCheckEvery is how long a look at the plugins' folders is
// trusted: the screen is drawn on every frame.
const PluginsCheckEvery = 2 * time.Second

// pluginUI is a plugin's screen, started in tab t.
type PluginUI struct {
	T    *Tab
	Pane *term.Pane `json:"-"`
	// events is where it is told of agents' runs of the plugin.
	Events *os.File `json:"-"`
	// dir is the plugin's folder, and key the content it was started from.
	Dir, Key string
	// remoteRuns is, in a copy of core's state, whether core runs the
	// screen's program, which the copy has no Pane of.
	remoteRuns bool
}

// close stops u's program, and takes its events file away.
func (u *PluginUI) Close() {
	if u.Events != nil {
		_ = u.Events.Close()
		_ = os.Remove(u.Events.Name())
	}
	go u.Pane.Close()
}

// pluginGoneAfter is how long a plugin's folder may be there without its
// plugin.toml before its screens and runs are taken to be orphaned: an
// editor writes plugin.toml by rename.
const PluginGoneAfter = 5 * time.Second

// running reports whether u's program still runs.
func (u *PluginUI) Running() bool {
	if u == nil {
		return false
	}
	if u.Pane == nil {
		return u.remoteRuns
	}
	exited, _, _ := u.Pane.Exited()
	return !exited
}

// maxPreviewLines is the most of a plugin's folder the user is shown to
// scroll through; one with more says to read the rest in the folder.
const MaxPreviewLines = 5000

// maxPreviewFile is the most of one file the question reads to show: a
// large one is read in its folder instead.
const MaxPreviewFile = 1 << 20

func HomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// PluginGone stops the screens and runs of a plugin whose folder is no
// longer there: the user cannot pick them, nor stop them, in F5.
func (m *Core) PluginGone(dir string) {
	delete(m.PluginsMissing, dir)
	for k, u := range m.PluginUIs {
		if u.Dir != dir {
			continue
		}
		delete(m.PluginUIs, k)
		m.Tell(PluginUIGoneMsg{Key: k})
		u.Close()
	}
	for _, r := range m.PluginRuns.ByID {
		if r.Dir != dir {
			continue
		}
		if r.Running() && r.StopBy == "" {
			r.StopBy = "the plugin is gone"
			r.Proc.Stop()
		}
	}
	for k, r := range m.PluginRuns.Runs {
		if r.Dir == dir {
			delete(m.PluginRuns.Runs, k)
		}
	}
}

// CheckPluginsGone stops screens and runs whose plugin folder is gone, or
// has had no plugin.toml for a while. It looks every few seconds, so a
// watch does not outlive its plugin just because F5 is not open.
func (m *Core) CheckPluginsGone(now time.Time) {
	if now.Sub(m.PluginsGoneAt) < PluginsCheckEvery {
		return
	}
	m.PluginsGoneAt = now
	if m.PluginsMissing == nil {
		m.PluginsMissing = map[string]time.Time{}
	}
	dirs := map[string]bool{}
	for _, u := range m.PluginUIs {
		dirs[u.Dir] = true
	}
	for _, r := range m.PluginRuns.ByID {
		dirs[r.Dir] = true
	}
	for _, r := range m.PluginRuns.Runs {
		dirs[r.Dir] = true
	}
	if len(dirs) == 0 {
		for dir := range m.PluginsMissing {
			delete(m.PluginsMissing, dir)
		}
		return
	}
	live := map[string]bool{}
	for _, p := range plugin.Find(plugin.UserDir(config.Path())) {
		live[p.Dir] = true
	}
	for dir := range dirs {
		if live[dir] {
			delete(m.PluginsMissing, dir)
			continue
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			m.PluginGone(dir)
			continue
		}
		since, ok := m.PluginsMissing[dir]
		if !ok {
			m.PluginsMissing[dir] = now
			continue
		}
		if now.Sub(since) >= PluginGoneAfter {
			m.PluginGone(dir)
		}
	}
	for dir := range m.PluginsMissing {
		if !dirs[dir] {
			delete(m.PluginsMissing, dir)
		}
	}
}

// PluginUIOf is p's screen in t, if one was started.
func (m *Core) PluginUIOf(t *Tab, p plugin.Plugin) *PluginUI {
	if t == nil {
		return nil
	}
	return m.PluginUIs[PluginUIKey(t, p)]
}

// StartPluginUI starts plugin p's screen in t, as allowed with key, in place
// of one that ended, and reports whether it did. The one it replaces is gone
// for the client only when the new one does not start. It is told of a run of the
// plugin going on, and the client with PluginUIStartedMsg.
func (m *Core) StartPluginUI(t *Tab, p plugin.Plugin, key string) bool {
	if len(p.UI) == 0 {
		m.Toast(clean.Line(p.Name) + " has no screen of its own")
		return false
	}
	stopped := m.stopPluginUI(t, p)
	fail := func(err error) bool {
		m.Toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		if stopped {
			m.Tell(PluginUIGoneMsg{Key: PluginUIKey(t, p)})
		}
		return false
	}
	w, h := m.PluginUIW, m.PluginUIH
	if w <= 0 || h <= 0 {
		w, h = 60, 20
	}
	events, err := PluginEventsFile(p)
	if err != nil {
		return fail(err)
	}
	dropEvents := func() {
		_ = events.Close()
		_ = os.Remove(events.Name())
	}
	env := append(append(m.TabEnv(t), "NERDR_SHELL=1", "NERDR_PLUGIN_EVENTS="+events.Name()), p.Env(nil)...)
	argv := p.Command(p.UI)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	dir, err := RunDir(t, t.Dir)
	if err != nil {
		dropEvents()
		return fail(err)
	}
	spec, err := PluginSpec(cmd, dir, p.Dir, key)
	if err != nil {
		dir.Close()
		dropEvents()
		return fail(err)
	}
	spec.W, spec.H = w, h
	m.Hold(&spec, Label{Kind: HeldPluginUI, Tab: t.ID, Plugin: p.Dir, Key: key, Events: events.Name()}, false, "")
	pane, err := term.StartWith(m.Programs(), spec, m.Notify)
	dir.Close()
	if err != nil {
		dropEvents()
		return fail(err)
	}
	if m.PluginUIs == nil {
		m.PluginUIs = map[string]*PluginUI{}
	}
	m.PluginUIs[PluginUIKey(t, p)] = &PluginUI{T: t, Pane: pane, Events: events, Dir: p.Dir, Key: key}
	if run := m.PluginRunOf(t, p); run.Running() {
		m.PluginEvent(t, p.Dir, run.StartEvent())
		if text := strings.Join(run.Lines(), "\n"); text != "" {
			m.PluginEvent(t, p.Dir, map[string]any{"event": "output", "run": run.Id, "text": text + "\n"})
		}
	}
	m.Tell(PluginUIStartedMsg{T: m.Ref(t), Key: PluginUIKey(t, p)})
	return true
}

// StopPluginUI stops plugin p's screen in t, if it was started, and tells
// the client it is gone.
func (m *Core) StopPluginUI(t *Tab, p plugin.Plugin) {
	if m.stopPluginUI(t, p) {
		m.Tell(PluginUIGoneMsg{Key: PluginUIKey(t, p)})
	}
}

// stopPluginUI is StopPluginUI telling nobody, for a screen started again in
// its place, and reports whether there was one.
func (m *Core) stopPluginUI(t *Tab, p plugin.Plugin) bool {
	key := PluginUIKey(t, p)
	u := m.PluginUIOf(t, p)
	if u == nil {
		return false
	}
	delete(m.PluginUIs, key)
	u.Close()
	return true
}

// CheckPluginUIs stops the screens of the tabs closed, and forgets their
// runs.
func (m *Core) CheckPluginUIs() {
	for k, u := range m.PluginUIs {
		if !slices.Contains(m.Tabs, u.T) {
			delete(m.PluginUIs, k)
			m.Tell(PluginUIGoneMsg{Key: k})
			u.Close()
		}
	}
	now := time.Now()
	m.CheckPluginsGone(now)
	m.PumpRuns(now)
	for k, r := range m.PluginRuns.Runs {
		if !slices.Contains(m.Tabs, r.T) {
			if r.Running() {
				r.Proc.Stop()
			}
			delete(m.PluginRuns.Runs, k)
			// Leave it in byID until endRun, so a client still reads
			// its result and stopRuns waits for its stop at quitting.
		}
	}
}

// PluginUIPanes are the plugins' screens, to stop when nerdr quits; the
// agents' runs are stopped there and then.
func (m *Core) PluginUIPanes() []*term.Pane {
	m.StopRuns()
	var out []*term.Pane
	for _, u := range m.PluginUIs {
		out = append(out, u.Pane)
		if u.Events != nil {
			_ = os.Remove(u.Events.Name())
		}
	}
	return out
}

// PluginEvent tells the screen of the plugin in folder dir, in t, of an
// agent's run of it, when the screen runs: a JSON object a line, in the
// file NERDR_PLUGIN_EVENTS names, which the screen reads as it grows.
func (m *Core) PluginEvent(t *Tab, dir string, ev map[string]any) {
	u := m.PluginUIs[t.ID+"\x00"+dir]
	if u == nil || u.Events == nil {
		return
	}
	b, err := json.Marshal(ev)
	if err == nil {
		_, _ = u.Events.Write(append(b, '\n'))
	}
}

// EnsurePluginUI has p's screen running, as allowed with key, in tab t.
func (m *Core) EnsurePluginUI(t *Tab, p plugin.Plugin, key string) {
	if u := m.PluginUIOf(t, p); !u.Running() || u.Key != key {
		m.StartPluginUI(t, p, key)
	}
}
