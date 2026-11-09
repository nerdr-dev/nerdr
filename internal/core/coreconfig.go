package core

import (
	"fmt"
	"os"
	"reflect"
	"strings"
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
// With Now, the settings core reads only at start (atStart) are taken at
// once too, as the setup and the skills' list do; else they wait for the
// next start, as when the file is edited by hand.
type EditConfig struct {
	Job        string
	Keys       []config.Edit
	MoveWtRoot bool
	Now        bool
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
		var r config.Read
		ev.cfg, r, ev.err = config.EditAndLoad(q.Keys...)
		ev.n, ev.mod = r.N, r.Mod
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
// when there is none; n numbers the read, a greater one having read the
// file later (config.LoadNumbered): reads run side by side, and one may
// come after another that read the file as it was later.
type configRead struct {
	cfg  config.Config
	mod  time.Time
	n    uint64
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
		cfg, r, err := config.LoadNumbered()
		return configRead{cfg: cfg, mod: r.Mod, n: r.N, err: err}
	}
}

func (m *Core) onConfigRead(ev configRead) Cmd {
	if ev.edit == nil && ev.mod.Equal(m.cfgMod) {
		return nil // taken in already
	}
	stale := ev.n != 0 && ev.n < m.cfgRead
	if (ev.err == nil || !ev.mod.IsZero()) && !stale {
		m.cfgMod = ev.mod // an error in the file is told once
	}
	if ev.err == nil && !stale {
		m.cfgRead = ev.n
		c := config.Merge(ev.cfg, m.Cfg)
		if ev.edit == nil || !ev.edit.Now {
			c = m.keepAtStart(c)
		}
		m.takeConfig(c)
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

// atStart are core's settings it reads only when it starts, by name:
// changed in the file, they keep their value until then.
var atStart = []struct {
	key string
	get func(*config.Config) any
}{
	{"worktrees_dir", func(c *config.Config) any { return &c.WorktreesDir }},
	{"install", func(c *config.Config) any { return &c.Install }},
	{"data_dir", func(c *config.Config) any { return &c.DataDir }},
	{"enabled_skills", func(c *config.Config) any { return &c.EnabledSkills }},
}

// keepAtStart is c with what atStart has as core has it now, and a toast
// of those c would change, once for each new value. Mu is held.
func (m *Core) keepAtStart(c config.Config) config.Config {
	if m.cfgNoted == nil {
		m.cfgNoted = map[string]string{}
	}
	var pending []string
	for _, s := range atStart {
		now := reflect.ValueOf(s.get(&m.Cfg)).Elem()
		next := reflect.ValueOf(s.get(&c)).Elem()
		v := fmt.Sprint(next.Interface())
		switch {
		case reflect.DeepEqual(now.Interface(), next.Interface()):
			delete(m.cfgNoted, s.key)
		case m.cfgNoted[s.key] != v:
			m.cfgNoted[s.key] = v
			pending = append(pending, s.key)
		}
		next.Set(now)
	}
	switch len(pending) {
	case 0:
	case 1:
		m.Toast(pending[0] + " changed: restart nerdr to use it")
	default:
		m.Toast(strings.Join(pending, ", ") + " changed: restart nerdr to use them")
	}
	return c
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
