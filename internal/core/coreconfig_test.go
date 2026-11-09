package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
)

// configHome gives the test a config file of its own, with text in it.
func configHome(t *testing.T, text string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	if text != "" {
		writeConfig(t, text)
	}
	return home
}

func writeConfig(t *testing.T, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEditConfigWritesCoresOwnFile: a client changes core's settings by
// asking core, which writes the file where it runs and goes by it, the
// client's own settings untouched.
func TestEditConfigWritesCoresOwnFile(t *testing.T) {
	configHome(t, "prefix = \"ctrl+b\"\n")
	cl := &eventsClient{}
	c := &Core{Root: t.TempDir(), Client: cl}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c.WatchConfigFile(cfg)
	c.Cfg.Prefix = "ctrl+a" // the client's, as it sent it

	cmd := EditConfig{Job: "j1", MoveWtRoot: true, Now: true, Keys: []config.Edit{
		{Key: "worktrees_dir", Value: config.Quote("/srv/wt")},
		{Table: "review", Key: "rounds", Value: "4"},
	}}.Do(c)
	for _, ev := range runNow(cmd, 5*time.Second) {
		c.Handle(ev)
	}
	if c.Cfg.WorktreesDir != "/srv/wt" || c.Cfg.Review.Rounds != 4 {
		t.Fatalf("core goes by worktrees %q, rounds %d", c.Cfg.WorktreesDir, c.Cfg.Review.Rounds)
	}
	if c.Cfg.Prefix != "ctrl+a" {
		t.Fatalf("the client's prefix became %q", c.Cfg.Prefix)
	}
	if c.WtRoot == "" {
		t.Fatal("new worktrees do not go to the new folder")
	}
	if len(cl.told) != 1 || cl.told[0] != (ConfigSavedMsg{Job: "j1"}) {
		t.Fatalf("told %#v", cl.told)
	}
	if got, _ := config.Load(); got.WorktreesDir != "/srv/wt" || got.Prefix != "ctrl+b" {
		t.Fatalf("the file has worktrees %q, prefix %q", got.WorktreesDir, got.Prefix)
	}
}

// TestEditConfigThatFails says why, and core goes on as it was.
func TestEditConfigThatFails(t *testing.T) {
	configHome(t, "")
	cl := &eventsClient{}
	c := &Core{Client: cl}
	c.WatchConfigFile(config.Config{WorktreesDir: "/old"})
	cmd := EditConfig{Job: "j", Keys: []config.Edit{{Key: "worktrees_dir", Value: "not toml"}}}.Do(c)
	for _, ev := range runNow(cmd, 5*time.Second) {
		c.Handle(ev)
	}
	if c.Cfg.WorktreesDir != "/old" {
		t.Fatalf("worktrees %q", c.Cfg.WorktreesDir)
	}
	if len(cl.told) != 1 || cl.told[0].(ConfigSavedMsg).Err == "" {
		t.Fatalf("told %#v", cl.told)
	}
}

// TestCoreTakesInItsConfigFileWhenChanged: edited by hand on the machine
// core runs on, its own settings are taken in, but for those read only at
// start, which wait for it and are told once; the client's stay as the
// client sent them.
func TestCoreTakesInItsConfigFileWhenChanged(t *testing.T) {
	configHome(t, "worktrees_dir = \"/one\"\n")
	cl := &eventsClient{}
	c := &Core{Client: cl}
	cfg, _ := config.Load()
	c.WatchConfigFile(cfg)
	c.Cfg.Prefix = "ctrl+a"
	if ev := c.lookAtConfig()(); ev != nil {
		t.Fatalf("looked again with the file unchanged: %#v", ev)
	}

	writeConfig(t, "worktrees_dir = \"/two\"\nprefix = \"ctrl+x\"\n[agents.claude]\ncommand = [\"sandbox\", \"claude\"]\n")
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(config.Path(), later, later); err != nil {
		t.Fatal(err)
	}
	ev := c.lookAtConfig()()
	if ev == nil {
		t.Fatal("the change was not seen")
	}
	c.Handle(ev)
	if c.Cfg.WorktreesDir != "/one" {
		t.Fatalf("worktrees %q before a restart", c.Cfg.WorktreesDir)
	}
	if len(cl.told) != 1 || cl.told[0] != ToastMsg("worktrees_dir changed: restart nerdr to use it") {
		t.Fatalf("told %#v", cl.told)
	}
	if got := c.Cfg.Agents["claude"].Command; len(got) != 2 || got[0] != "sandbox" {
		t.Fatalf("claude's command %q", got)
	}
	if c.Cfg.Prefix != "ctrl+a" {
		t.Fatalf("the client's prefix became %q", c.Cfg.Prefix)
	}
	if c.lookAtConfig()() != nil {
		t.Fatal("looked again once taken in")
	}
}

// TestCoreNotWatchingReadsNoFile: a core that was not told to watch its
// file (a client's copy, or nerdr with no core of its own, where the
// client reads the file) never reads it.
func TestCoreNotWatchingReadsNoFile(t *testing.T) {
	configHome(t, "worktrees_dir = \"/one\"\n")
	c := &Core{}
	if c.lookAtConfig() != nil {
		t.Fatal("looks at the file unasked")
	}
	c.WatchConfigFile(config.Config{})
	c.Copy = true
	if c.lookAtConfig() != nil {
		t.Fatal("a copy looks at the file")
	}
}

// TestCopyTakesCoresOwnSettings: the copy of core's state a client keeps
// has core's own settings, from the machine core runs on, and keeps the
// client's.
func TestCopyTakesCoresOwnSettings(t *testing.T) {
	c := &Core{Cfg: config.Config{
		Prefix:       "ctrl+b",
		WorktreesDir: "/srv/wt",
		Agents:       map[agents.Kind]agents.Spec{"claude": {Command: []string{"sandbox", "claude"}}},
	}}
	parts, err := c.Parts(&Sent{})
	if err != nil {
		t.Fatal(err)
	}
	copied := &Core{Cfg: config.Config{Prefix: "ctrl+a", WorktreesDir: "/home/me/wt"}}
	mr := NewMirror(copied)
	for _, p := range parts {
		if err := mr.Apply(p); err != nil {
			t.Fatal(err)
		}
	}
	if copied.Cfg.WorktreesDir != "/srv/wt" || len(copied.Cfg.Agents["claude"].Command) != 2 {
		t.Fatalf("the copy has worktrees %q, agents %v", copied.Cfg.WorktreesDir, copied.Cfg.Agents)
	}
	if copied.Cfg.Prefix != "ctrl+a" {
		t.Fatalf("the copy's prefix became %q", copied.Cfg.Prefix)
	}
}

// TestSetConfigTakesOnlyTheClientsSettings: the config a client sends
// never changes how core starts its agents, where its worktrees go, or
// whether plugin runs are confirmed: the client's file may be on another
// machine.
func TestSetConfigTakesOnlyTheClientsSettings(t *testing.T) {
	c := &Core{Cfg: config.Config{
		Prefix:            "ctrl+b",
		WorktreesDir:      "/srv/wt",
		ConfirmPluginRuns: true,
		Agents:            map[agents.Kind]agents.Spec{"claude": {Command: []string{"sandbox", "claude"}}},
	}}
	SetConfig{Cfg: config.Config{
		Prefix:       "ctrl+a",
		WorktreesDir: "/home/me/wt",
		Agents:       map[agents.Kind]agents.Spec{"claude": {Command: []string{"claude"}}},
	}}.Do(c)
	if c.Cfg.Prefix != "ctrl+a" {
		t.Errorf("the client's prefix not taken: %q", c.Cfg.Prefix)
	}
	if c.Cfg.WorktreesDir != "/srv/wt" || !c.Cfg.ConfirmPluginRuns || c.Cfg.Agents["claude"].Command[0] != "sandbox" {
		t.Errorf("core's own settings taken from the client: %+v", c.Cfg)
	}
}
