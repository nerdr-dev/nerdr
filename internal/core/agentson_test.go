package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
)

// TestAgentsAreLookedForOnTheClientsPath: core looks for the agents on
// the PATH of the client that connected, as the agents it starts get it,
// keeps what it found for the client, and looks again every AgentsEvery,
// or at once when told to.
func TestAgentsAreLookedForOnTheClientsPath(t *testing.T) {
	bin := t.TempDir()
	put := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put("fakeone")
	t.Setenv("PATH", t.TempDir()) // core's own: none there
	c := &Core{Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{
		"one": {Command: []string{"fakeone"}},
		"two": {Command: []string{"faketwo"}},
	}}, ClientEnv: []string{"PATH=relative:" + bin}}

	c.FindAgents()
	if got := c.Available(); !slices.Equal(got, []agents.Kind{"one"}) {
		t.Fatalf("found %v on the client's PATH", got)
	}
	now := c.AgentsAt
	if c.lookForAgents(now.Add(time.Second)) != nil {
		t.Fatal("looked again before AgentsEvery")
	}

	put("faketwo")
	c.LookForAgentsSoon()
	cmd := c.lookForAgents(now.Add(time.Second))
	if cmd == nil {
		t.Fatal("told to look soon, it did not")
	}
	ev := cmd().(agentsFoundMsg)
	c.onAgentsFound(ev)
	if got := c.Available(); !slices.Equal(got, []agents.Kind{"one", "two"}) {
		t.Fatalf("found %v once two was installed", got)
	}
	if c.lookForAgents(now.Add(2*time.Second)) != nil {
		t.Fatal("looked again at once after looking")
	}
	if c.lookForAgents(ev.at.Add(AgentsEvery)) == nil {
		t.Fatal("did not look again after AgentsEvery")
	}
}
