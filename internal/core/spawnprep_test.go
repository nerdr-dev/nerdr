package core

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
)

// stubTrust has TrustCodex count its calls, and answer trusted.
func stubTrust(t *testing.T) *atomic.Int32 {
	var n atomic.Int32
	was := TrustCodex
	TrustCodex = func(argv, env []string, write bool) (bool, bool, error) {
		n.Add(1)
		return true, write, nil
	}
	t.Cleanup(func() { TrustCodex = was })
	return &n
}

// TestSpawnAsksTheCLIAwayFromMu: codex, which nerdr asks whether it
// trusts nerdr's hooks before starting it, is not run by Spawn, which has
// Mu held, but by the Cmd it returns; the agent starts with the answer.
func TestSpawnAsksTheCLIAwayFromMu(t *testing.T) {
	asked := stubTrust(t)
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec, HookSock: "/run/nerdr.sock"}
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{"cat"}}}
	tab := &Tab{}
	tab.Dir, tab.Agent = t.TempDir(), agents.Codex
	m.Tabs = []*Tab{tab}

	cmd := m.Spawn(tab, agents.Launch{Argv: []string{"cat", "--no-daemon"}}, false)
	if asked.Load() != 0 || len(rec.specs) != 0 {
		t.Fatalf("Spawn asked codex %d times, started %d", asked.Load(), len(rec.specs))
	}
	if cmd == nil {
		t.Fatal("nothing to ask codex")
	}
	ev, ok := cmd().(SpawnReady)
	if !ok || asked.Load() != 1 || !ev.Prep.CodexAsked || !ev.Prep.CodexTrusted {
		t.Fatalf("the Cmd gave %+v, asking %d times", ev, asked.Load())
	}
	m.OnSpawnReady(ev)
	if tab.Pane == nil || len(rec.specs) != 1 {
		t.Fatalf("codex did not start: %q", tab.Err)
	}
	defer tab.Pane.Close()
	if !m.CodexAsked || len(m.CodexHooks) == 0 {
		t.Error("codex's answer was not kept")
	}
	if argv := strings.Join(rec.specs[0].Argv, " "); !strings.Contains(argv, "hooks.Stop=") {
		t.Errorf("trusted, codex started without nerdr's hooks: %q", argv)
	}

	// Asked once: the next starts at once.
	next := &Tab{}
	next.Dir, next.Agent = tab.Dir, agents.Codex
	m.Tabs = append(m.Tabs, next)
	if cmd := m.Spawn(next, agents.Launch{Argv: []string{"cat", "--no-daemon"}}, false); next.Pane == nil {
		t.Fatalf("asked already, codex did not start at once (cmd %v)", cmd != nil)
	}
	defer next.Pane.Close()
	if asked.Load() != 1 {
		t.Errorf("codex asked %d times", asked.Load())
	}

	// Closed while codex was asked: not started.
	gone := &Tab{}
	gone.Dir, gone.Agent = tab.Dir, agents.Codex
	m.CodexAsked = false
	m.Tabs = append(m.Tabs, gone)
	cmd = m.Spawn(gone, agents.Launch{Argv: []string{"cat", "--no-daemon"}}, false)
	m.Tabs = m.Tabs[:2]
	m.OnSpawnReady(cmd().(SpawnReady))
	if gone.Pane != nil {
		gone.Pane.Close()
		t.Error("an agent closed while its CLI was asked started")
	}
}

// TestApplySkillsAsksCodexAwayFromMu: the request that puts the skills in
// place has codex trust nerdr's hooks in the Cmd it returns, not itself.
func TestApplySkillsAsksCodexAwayFromMu(t *testing.T) {
	asked := stubTrust(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	codex := dir + "/codex"
	writeExec(t, codex)
	m := &Core{Client: NoClient{}}
	m.Cfg.Install = config.InstallHome
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{codex}}}
	cmd := ApplySkills{}.Do(m)
	if asked.Load() != 0 {
		t.Fatal("ApplySkills asked codex itself")
	}
	if cmd == nil {
		t.Fatal("codex was not to be asked")
	}
	ev, ok := cmd().(SkillsAppliedMsg)
	if !ok || asked.Load() != 1 {
		t.Fatalf("the Cmd gave %T, asking %d times", ev, asked.Load())
	}
	m.OnSkillsApplied(ev)
	if !m.CodexAsked || len(m.CodexHooks) == 0 {
		t.Error("codex's answer was not kept")
	}
}

// writeExec writes a program at path that does nothing.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
