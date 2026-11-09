package core

import (
	"os"
	"reflect"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Core's own settings (config.ScopeCore: the agents, where the worktrees
// go, what is installed for the agents) are those of the config file on
// the machine it runs on, which a client may not be on. Core reads them
// there, writes them there when a client asks (EditConfig, ReadConfig),
// and takes them in again when the file changes (WatchConfigFile). A
// client's own settings come from the client (SetConfig), and core sends
// the client its settings with its state (see Mirror).

func init() { registerRequests(EditConfig{}, ReadConfig{}) }

// EditConfig sets Keys in core's config file, each as config.SetKey takes
// it, and has core go by the file from then on: told as a ConfigSavedMsg.
// With MoveWtRoot new worktrees go to the worktrees_dir the file then has
// from now on, not only from the next start, as the first-start setup
// asks before any tab is made; with Apply, the skills are applied after.
type EditConfig struct {
	Job        string
	Keys       []config.Edit
	MoveWtRoot bool
	Apply      *ApplySkills `json:",omitempty"`
}

// ConfigSavedMsg is how EditConfig of Job went: Err is "" when it was
// saved.
type ConfigSavedMsg struct {
	Job string
	Err string `json:",omitempty"`
}

func (q EditConfig) Do(m *Core) Cmd {
	return func() Event {
		ev := configRead{edit: &q}
		if ev.err = config.SetKeys(q.Keys...); ev.err != nil {
			return ev
		}
		ev.mod = configMod()
		ev.cfg, ev.err = config.Load()
		return ev
	}
}

// ReadConfig reads core's config file as written, for the settings to
// tell what it sets from a default: told as a ConfigTextMsg.
type ReadConfig struct{ Job string }

// ConfigTextMsg is core's config file as ReadConfig of Job read it, Path
// where it is.
type ConfigTextMsg struct {
	Job, Path, Text string
	Err             string `json:",omitempty"`
}

func (q ReadConfig) Do(m *Core) Cmd {
	return Later(func() Event {
		text, err := config.Text()
		msg := ConfigTextMsg{Job: q.Job, Path: config.Path(), Text: text}
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

// configRead is core's config file as read: after an EditConfig of it,
// or because it changed. mod is when it was last changed, the zero time
// when there is none.
type configRead struct {
	cfg  config.Config
	mod  time.Time
	err  error
	edit *EditConfig
}

// configMod is when the config file was last changed, the zero time when
// there is none.
func configMod() time.Time {
	st, err := os.Stat(config.Path())
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// WatchConfigFile has core take its settings in again whenever its config
// file changes, edited by hand or by a client: what a core in a process of
// its own does, as cfg is what it read at start. Mu is held, or core is
// not running yet.
func (m *Core) WatchConfigFile(cfg config.Config) {
	m.Cfg = cfg
	m.ConfigFile = config.Path()
	m.cfgMod = configMod()
	m.cfgWatch = true
}

// ConfigPath is where core's config file is: on the machine core runs on.
func (m *Core) ConfigPath() string {
	if m.ConfigFile != "" {
		return m.ConfigFile
	}
	return config.Path()
}

// lookAtConfig reads the config file away from the loop when it changed
// since the last look; see WatchConfigFile.
func (m *Core) lookAtConfig() Cmd {
	if !m.cfgWatch || m.Copy {
		return nil
	}
	last := m.cfgMod
	return func() Event {
		mod := configMod()
		if mod.Equal(last) {
			return nil
		}
		cfg, err := config.Load()
		return configRead{cfg: cfg, mod: mod, err: err}
	}
}

func (m *Core) onConfigRead(ev configRead) Cmd {
	if ev.edit == nil && ev.mod.Equal(m.cfgMod) {
		return nil // taken in already
	}
	if ev.err == nil || !ev.mod.IsZero() {
		m.cfgMod = ev.mod // an error in the file is told once
	}
	if ev.err == nil {
		m.takeConfig(config.Merge(ev.cfg, m.Cfg))
	}
	if ev.edit == nil {
		if ev.err != nil {
			Logf("config: %v", ev.err)
			m.Toast("config: " + ev.err.Error())
		}
		return nil
	}
	saved := ConfigSavedMsg{Job: ev.edit.Job}
	var cmds []Cmd
	if ev.err != nil {
		saved.Err = ev.err.Error()
	} else {
		if ev.edit.MoveWtRoot {
			m.WtRoot = workspace.WorktreesRoot(m.Root, m.Cfg.WorktreesDir)
		}
		if ev.edit.Apply != nil {
			cmds = append(cmds, ev.edit.Apply.Do(m))
		}
	}
	return Batch(append(cmds, m.Tell(saved))...)
}

// takeConfig has core go by c from now on. Mu is held.
func (m *Core) takeConfig(c config.Config) {
	if c.DataDir != m.Cfg.DataDir {
		store.SetDataDir(c.DataDir)
	}
	if !reflect.DeepEqual(c.Agents, m.Cfg.Agents) {
		m.LookForAgentsSoon()
	}
	m.Cfg = c
}
