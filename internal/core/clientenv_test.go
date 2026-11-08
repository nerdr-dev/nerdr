package core

import (
	"slices"
	"testing"
)

// TestProgramsTakeTheClientsEnv: what a tab starts once a client has
// said its environment runs with that one, not the core's, which is the
// holder's and may be out of date; before, it runs with the core's.
func TestProgramsTakeTheClientsEnv(t *testing.T) {
	t.Setenv("NERDR_TEST_FROM", "core")
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec}
	m.Cfg.Shell = "true"
	tab := &Tab{}
	tab.Dir = t.TempDir()

	if env := m.AgentEnv(tab); !slices.Contains(env, "NERDR_TEST_FROM=core") {
		t.Errorf("with no client, an agent did not get the core's environment")
	}
	m.ClientEnv = []string{"NERDR_TEST_FROM=client", "SSH_AUTH_SOCK=/client/agent", "TMUX=/tmp/tmux", "NERDR_TAB=forged"}
	env := m.AgentEnv(tab)
	for _, want := range []string{"NERDR_TEST_FROM=client", "SSH_AUTH_SOCK=/client/agent", "NERDR_TAB=" + tab.HookKey()} {
		if !slices.Contains(env, want) {
			t.Errorf("agent's environment lacks %s", want)
		}
	}
	for _, not := range []string{"NERDR_TEST_FROM=core", "TMUX=/tmp/tmux", "NERDR_TAB=forged"} {
		if slices.Contains(env, not) {
			t.Errorf("agent's environment has %s", not)
		}
	}
	p, _ := m.NewShellIn(tab, true)
	if p == nil || p.Shell == nil {
		t.Fatal("the terminal did not start")
	}
	defer p.Shell.Close()
	if s := rec.specs[len(rec.specs)-1]; !slices.Contains(s.Env, "NERDR_TEST_FROM=client") {
		t.Errorf("the terminal did not get the client's environment")
	}
	m.BaseEnv()[0] = "NERDR_TEST_FROM=changed"
	if m.ClientEnv[0] != "NERDR_TEST_FROM=client" {
		t.Error("BaseEnv hands out the client's own slice")
	}
}
