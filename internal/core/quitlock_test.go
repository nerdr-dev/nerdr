package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stuckRun is a run told to stop already, whose program has not ended.
func stuckRun() *PluginRun {
	p := &RunProc{Stopped: make(chan struct{})}
	p.StopOnce.Do(func() {})
	return &PluginRun{Id: "r", Proc: p}
}

// Quitting with a plugin's run that is slow to stop does not keep Mu: the
// runs are told to stop in QuitAll.Do, and waited for in its Cmd, away from
// the loop.
func TestQuitWaitsForRunsAwayFromMu(t *testing.T) {
	run := stuckRun()
	c := &Core{Client: NoClient{}, PluginRuns: PluginRuns{ByID: map[string]*PluginRun{"r": run}}}
	done := make(chan Cmd, 1)
	go func() { done <- QuitAll{}.Do(c) }()
	var cmd Cmd
	select {
	case cmd = <-done:
	case <-time.After(time.Second):
		t.Fatal("QuitAll.Do waited for the run")
	}
	if cmd == nil {
		t.Fatal("no Cmd to stop the programs")
	}
	ended := make(chan struct{})
	go func() { cmd(); close(ended) }()
	select {
	case <-ended:
		t.Fatal("the Cmd did not wait for the run")
	case <-time.After(100 * time.Millisecond):
	}
	close(run.Proc.Stopped)
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("the Cmd did not end with the run")
	}
}

// The look for plugins gone is on the disk away from Mu, one at a time:
// CheckPluginsGone only gathers what is in use, and pluginsChecked stops
// what was found gone.
func TestPluginsGoneLookedForAwayFromMu(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	gone := filepath.Join(t.TempDir(), "gone")
	c := &Core{Client: NoClient{}, PluginRuns: PluginRuns{Runs: map[string]*PluginRun{
		"k": {Dir: gone, Ended: time.Now()},
	}}}
	now := time.Now()
	cmd := c.CheckPluginsGone(now)
	if cmd == nil {
		t.Fatal("not looked for")
	}
	if len(c.PluginRuns.Runs) != 1 {
		t.Fatal("the disk was looked at with Mu held")
	}
	if c.CheckPluginsGone(now.Add(time.Hour)) != nil {
		t.Fatal("a second look started while the first was on")
	}
	ev := cmd()
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	c.Handle(ev)
	if c.PluginsLooking {
		t.Fatal("the look did not end")
	}
	if len(c.PluginRuns.Runs) != 0 {
		t.Fatal("the run of a gone plugin is still there")
	}
	if c.CheckPluginsGone(now.Add(2*time.Hour)) != nil {
		t.Fatal("looked with nothing in use")
	}
}
