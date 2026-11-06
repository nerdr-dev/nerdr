package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// TestNoClientAllowsNothing: what needs the user's yes does not happen while
// nobody is there to say it. It waits, or is refused; it is never taken as yes.
func TestNoClientAllowsNothing(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, workspace.RepoConfigFile), []byte(`setup = "make deps"`), 0o644); err != nil {
		t.Fatal(err)
	}
	core := &Core{Client: NoClient{}}
	ran := false
	cmd := core.AskSetup([]string{repo}, func() Cmd {
		ran = true
		return nil
	})
	if ran || cmd != nil {
		t.Fatal("a repository's setup ran with nobody to allow it")
	}
	if !core.Asked(AskRepoSetup) {
		t.Fatal("the question did not wait for a client")
	}
	var c Client = NoClient{}
	if c.AgentVisible(&Tab{}) {
		t.Fatal("an agent counted as looked at with nobody there")
	}
	if c.DialogClosed() {
		t.Fatal("a dialog could open with nobody there")
	}
}

// TestNoClientRunsNoPluginForASandboxedAgent: an allowed plugin, asked for by
// an agent in a sandbox, needs a yes nobody can give. The question waits in
// core for a client; unanswered in time, the plugin has not run and the agent
// is told to ask again. What only reads is still answered.
func TestNoClientRunsNoPluginForASandboxedAgent(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	marker := filepath.Join(t.TempDir(), "ran")
	dir := filepath.Join(plugin.UserDir(config.Path()), "free")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("description = \"Free.\"\nrun = [\"touch\", %q]\n", marker)
	if err := os.WriteFile(filepath.Join(dir, plugin.File), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, key, err := plugin.Fresh(plugin.Find(plugin.UserDir(config.Path()))[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Allow(PluginsAllowedFile(), p, key); err != nil {
		t.Fatal(err)
	}

	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a", Dir: t.TempDir()}, InSandbox: true}}
	core := &Core{Client: NoClient{}, Tabs: []*Tab{tb}}
	send := func(what string) chan hooks.Answer {
		reply := make(chan hooks.Answer, 1)
		core.OnPlugin(hooks.Report{Method: hooks.Plugin, What: what, Pane: tb.HookKey(), Name: "free", Dir: tb.Dir, Reply: reply})
		return reply
	}
	wait := func(reply chan hooks.Answer) hooks.Answer {
		t.Helper()
		select {
		case a := <-reply:
			return a
		case <-time.After(5 * time.Second):
			t.Fatal("no answer")
			return hooks.Answer{}
		}
	}
	ask := func(what string) hooks.Answer { t.Helper(); return wait(send(what)) }

	// The run waits for a client as long as the agent waits for its
	// answer, then is to be asked again.
	run := send(hooks.PluginRun)
	if !core.Asked(AskPluginRun) || len(run) != 0 {
		t.Fatalf("the run did not wait for a client: asked %v, answered %d", core.Asked(AskPluginRun), len(run))
	}
	core.Asking[0].Until = time.Now().Add(-time.Second)
	core.ExpireAsks(time.Now())
	if a := wait(run); a.Text != RunBusy || core.Asked(AskPluginRun) {
		t.Fatalf("run: %+v, want %q", a, RunBusy)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the plugin ran with nobody to allow it")
	}
	if len(core.PluginRuns.ByID) != 0 {
		t.Fatalf("runs: %d", len(core.PluginRuns.ByID))
	}
	if a := ask("allowed"); a.Err != "" {
		t.Fatalf("asking whether it is allowed was refused: %+v", a)
	}
}

// TestReportsAreCheckedBeforeTheyAreRouted: a report from a process that is
// not the agent it names goes no further, whatever it asks.
func TestReportsAreCheckedBeforeTheyAreRouted(t *testing.T) {
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a"}}}
	reply := make(chan hooks.Answer, 1)
	core := &Core{Client: NoClient{}, Tabs: []*Tab{tb}}
	stranger := hooks.Peer{Known: true, Chain: []int{1 << 30, 1}}
	core.OnReport(hooks.Report{Method: hooks.Terminal, Text: "echo out", Pane: tb.HookKey(), Peer: stranger, Reply: reply})
	if a := <-reply; a.Err == "" {
		t.Fatalf("taken from a stranger: %+v", a)
	}
}

// TestBackgroundWorkGoesOnWithNoClient: the look at the branches, and the
// rest of InfoTick, is core's, and runs with nobody looking.
func TestBackgroundWorkGoesOnWithNoClient(t *testing.T) {
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a", Dir: t.TempDir()}}}
	tb.Info.Stale = true
	core := &Core{Client: NoClient{}, Tabs: []*Tab{tb}}
	if core.InfoTick(time.Now()) == nil || !core.InfoBusy {
		t.Fatal("the tab's branches were not looked at with no client")
	}
}
