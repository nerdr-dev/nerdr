package core

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/plugin"
)

// A private plugin's screen and runs are each tab's own; a global one's
// are one for all tabs.
func TestGlobalPluginIsOneForAllTabs(t *testing.T) {
	ta, tb := &Tab{ID: "a", Name: "api", Agent: "claude"}, &Tab{ID: "b", Name: "web", Agent: "codex"}
	priv := plugin.Plugin{Name: "p", Dir: "/p"}
	glob := plugin.Plugin{Name: "g", Dir: "/g", Scope: plugin.Global}
	if PluginUIKey(ta, priv) == PluginUIKey(tb, priv) {
		t.Error("a private plugin has one key for two tabs")
	}
	if k := PluginUIKey(ta, glob); k != PluginUIKey(tb, glob) || k != PluginUIKey(nil, glob) {
		t.Error("a global plugin has a key of each tab's")
	}

	m := &Core{}
	run := &PluginRun{Id: "r1", T: ta, From: ta, Dir: "/g", Global: true, Agent: "api claude"}
	m.PluginRuns.Runs = map[string]*PluginRun{PluginUIKey(ta, glob): run}
	m.PluginRuns.ByID = map[string]*PluginRun{run.Id: run}
	if m.PluginRunOf(tb, glob) != run || m.PluginRunOf(nil, glob) != run {
		t.Error("another tab does not see the global run")
	}
	if m.PluginRunOf(tb, priv) != nil {
		t.Error("a private plugin's run seen in another tab")
	}
	why := m.GlobalBusy(tb, glob)
	if !strings.Contains(why, "api claude") || !strings.Contains(why, "r1") {
		t.Errorf("another tab's agent while it runs: %q", why)
	}
	if why := m.GlobalBusy(ta, glob); why == "" {
		t.Error("its own agent, not watching, runs it twice at once")
	}
	if m.GlobalBusy(tb, priv) != "" {
		t.Error("a private plugin is busy for another tab")
	}
	run.Watch = true
	if why := m.GlobalBusy(ta, glob); why != "" {
		t.Errorf("its own agent's watch is not taken over: %q", why)
	}
	run.Ended = time.Now()
	if why := m.GlobalBusy(tb, glob); why != "" {
		t.Errorf("ended, still busy: %q", why)
	}
	if got := m.RunAgentLabel(tb, tb, true); got != "web codex" {
		t.Errorf("a global run's agent: %q", got)
	}
}

// A global plugin's programs run with no tab's variables.
func TestGlobalEnvHasNoTab(t *testing.T) {
	base := []string{"HOME=/h", "NERDR_TAB=x", "NERDR_WORKTREES=[]", "NERDR_WORKTREE_INFO=[]", "HERDR_PANE=1"}
	env := GlobalEnvFrom(base)
	for _, kv := range env {
		if strings.HasPrefix(kv, "NERDR_TAB=") || strings.HasPrefix(kv, "NERDR_WORKTREES=") || strings.HasPrefix(kv, "NERDR_WORKTREE_INFO=") || strings.HasPrefix(kv, "HERDR_") {
			t.Errorf("global env has %s", kv)
		}
	}
	if !slices.Contains(env, "HOME=/h") || !slices.Contains(env, "NERDR=1") {
		t.Errorf("global env: %q", env)
	}
}

// A plugin does not run beside a run of it from before its scope changed:
// one started private is still ending when it is now global, or the
// other way.
func TestNoRunBesideOneOfTheOldScope(t *testing.T) {
	ta := &Tab{ID: "a", Name: "api", Agent: "claude"}
	m := &Core{}
	old := &PluginRun{Id: "r1", T: ta, From: ta, Dir: "/p", Agent: "claude"}
	m.PluginRuns.ByID = map[string]*PluginRun{old.Id: old}
	for _, global := range []bool{true, false} {
		old.Global = !global
		p := plugin.Plugin{Name: "p", Dir: "/p"}
		if global {
			p.Scope = plugin.Global
		}
		if why := m.GlobalBusy(ta, p); !strings.Contains(why, "changed between private and global") {
			t.Errorf("global %v: %q", global, why)
		}
	}
	old.Global = false
	if why := m.GlobalBusy(ta, plugin.Plugin{Name: "p", Dir: "/p"}); why != "" {
		t.Errorf("a private plugin beside its own run of the same scope: %q", why)
	}
}
