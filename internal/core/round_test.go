package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// watcher is a client that looks at everything and remembers what it was told.
type watcher struct {
	NoClient
	saved  int
	toasts []string
}

func (w *watcher) Toast(s string)           { w.toasts = append(w.toasts, s) }
func (w *watcher) SaveSession()             { w.saved++ }
func (w *watcher) AgentVisible(_ *Tab) bool { return true }

// sendEnterIn runs c and what it leads to, and returns the SendEnter among
// them. The rest, such as looking in the agent's logs, is left to finish by
// itself.
func sendEnterIn(t *testing.T, c Cmd) SendEnter {
	t.Helper()
	found := make(chan SendEnter, 1)
	var run func(Cmd)
	run = func(c Cmd) {
		if c == nil {
			return
		}
		switch ev := c().(type) {
		case BatchEvent:
			for _, x := range ev {
				go run(x)
			}
		case SendEnter:
			found <- ev
		}
	}
	go run(c)
	select {
	case ev := <-found:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no enter after the message")
		return SendEnter{}
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 500 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// TestWholeRoundWithNoTUI is what core is for: an agent is started, reports
// through its integration, is given a message from its queue, works on it and
// exits, with core and no user interface.
func TestWholeRoundWithNoTUI(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile read -r line; do\n  echo \"got $line\"\n  [ \"$line\" = bye ] && exit 0\ndone\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := &watcher{}
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a", Name: "a", Dir: dir, Agent: agents.Codex}}}
	c := &Core{Client: w, Tabs: []*Tab{tb}, Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{
		agents.Codex: {Command: []string{script}},
	}}}
	c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatalf("not started: %s", tb.Err)
	}
	defer tb.Pane.Close()

	report := func(state string) {
		t.Helper()
		c.OnHook(hooks.Report{Method: hooks.ReportAgent, Pane: tb.HookKey(), Source: "test", Agent: tb.AgentID(), State: state})
	}
	report("idle")
	tb.Messages = []state.Message{{Text: "do this", Auto: true}}
	time.Sleep(QueueQuiet + 100*time.Millisecond)

	ev := sendEnterIn(t, c.DetectAgent(tb, true, time.Now()))
	if len(tb.Messages) != 0 || !tb.AwaitWork || w.saved == 0 {
		t.Fatalf("not sent: %+v, await %v, saved %d", tb.Messages, tb.AwaitWork, w.saved)
	}
	c.SendEnter(ev)
	waitFor(t, "the agent answers", func() bool { return strings.Contains(tb.Pane.Text(0), "got") })

	report("working")
	c.DetectAgent(tb, true, time.Now())
	if tb.Light != StWorking || tb.AwaitWork {
		t.Fatalf("working: %s, await %v", tb.Light.Label(), tb.AwaitWork)
	}

	tb.Pane.Input([]byte("bye\r"))
	waitFor(t, "the agent exits", func() bool {
		exited, _, _ := tb.Pane.Exited()
		return exited
	})
	c.DetectAgent(tb, true, time.Now())
	if tb.Light != StExited {
		t.Fatalf("after exit: %s", tb.Light.Label())
	}
	if len(w.toasts) != 0 {
		t.Errorf("told the user %q", w.toasts)
	}
}

// TestEnterForAReplacedAgent: an agent replaced by another with the same ID,
// as a reviewer can be, takes its place between a message and the enter after
// it. The enter is not for the new program, which gets only its own.
func TestEnterForAReplacedAgent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile read -r line; do echo \"got line\"; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Core{Client: &watcher{}, Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{
		agents.Codex: {Command: []string{script}},
	}}}
	start := func() *Tab {
		tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a", Name: "a", Dir: dir, Agent: agents.Codex}}}
		c.Tabs = []*Tab{tb}
		c.Start(tb, false)
		if tb.Pane == nil {
			t.Fatalf("not started: %s", tb.Err)
		}
		t.Cleanup(tb.Pane.Close)
		return tb
	}
	old := start()
	late := SendEnter{ID: old.ID, Gen: old.Gen}
	now := start()

	c.SendEnter(late)
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(now.Pane.Text(0), "got line") || !now.EnterAt.IsZero() {
		t.Fatalf("the replacement got the old agent's enter: %q", now.Pane.Text(0))
	}
	c.SendEnter(SendEnter{ID: now.ID, Gen: now.Gen})
	waitFor(t, "its own enter", func() bool { return strings.Contains(now.Pane.Text(0), "got line") })
}
