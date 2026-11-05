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

func waitLocked(t *testing.T, c *Core, what string, ok func() bool) {
	t.Helper()
	waitFor(t, what, func() bool {
		c.Mu.Lock()
		defer c.Mu.Unlock()
		return ok()
	})
}

// TestRoundOnTheLoop: with the loop running and nobody there at all, an agent
// reports, gets its queued message, answers and exits, each thing arriving as
// it would from a real agent, core's own ticks doing the rest.
func TestRoundOnTheLoop(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile read -r line; do\n  echo \"got $line\"\n  [ \"$line\" = bye ] && exit 0\ndone\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a", Name: "a", Dir: dir, Agent: agents.Codex}}}
	c := &Core{Client: NoClient{}, Tabs: []*Tab{tb}, Wake: make(chan struct{}, 1), Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{
		agents.Codex: {Command: []string{script}},
	}}}
	c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatalf("not started: %s", tb.Err)
	}
	defer tb.Pane.Close()

	l := NewLoop(c, nil)
	l.Start()
	defer l.Stop()

	report := func(st string) {
		l.Post(hooks.Report{Method: hooks.ReportAgent, Pane: tb.HookKey(), Source: "test", Agent: "codex", State: st})
	}
	report("idle")
	c.Mu.Lock()
	tb.Messages = []state.Message{{Text: "do this", Auto: true}}
	c.Mu.Unlock()

	waitLocked(t, c, "the message goes in", func() bool { return len(tb.Messages) == 0 })
	waitFor(t, "the agent answers", func() bool { return strings.Contains(tb.Pane.Text(0), "got") })

	report("working")
	waitLocked(t, c, "the light turns", func() bool { return tb.Light == StWorking })

	tb.Pane.Input([]byte("bye\r"))
	waitLocked(t, c, "the exit is seen", func() bool { return tb.Light == StExited })
}

// TestLoopDoesNotWaitForAClient: a client that has stopped reading holds up
// nothing of core's.
func TestLoopDoesNotWaitForAClient(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1)}
	l := NewLoop(c, func(Event) { <-block })
	l.Start()
	defer l.Stop()

	type notCores struct{ n int }
	for i := range 1000 {
		l.Post(notCores{i}) // each goes to the client, which is not there
	}
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a"}}}
	tb.GoalLooking = true
	c.Mu.Lock()
	c.Tabs = []*Tab{tb}
	c.Mu.Unlock()
	l.Post(GoalMsg{T: tb})
	waitLocked(t, c, "core goes on", func() bool { return !tb.GoalLooking })
}

// TestLoopTellsTheClient: what is not core's goes on as it came, in order, and
// what core handled comes as word to draw again, never queued twice in a row.
func TestLoopTellsTheClient(t *testing.T) {
	got := make(chan Event, 16)
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1)}
	l := NewLoop(c, func(ev Event) { got <- ev })
	l.Start()
	defer l.Stop()

	next := func() Event {
		t.Helper()
		select {
		case ev := <-got:
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("nothing came")
			return nil
		}
	}
	l.Post("for the client")
	for {
		ev := next()
		if ev == "for the client" {
			break
		}
		if _, ok := ev.(RedrawMsg); !ok {
			t.Fatalf("unexpected %#v", ev)
		}
	}
	l.Post(SummaryMsg{})
	for {
		ev := next()
		if _, ok := ev.(ChangedMsg); ok {
			break
		}
		if _, ok := ev.(RedrawMsg); !ok {
			t.Fatalf("unexpected %#v", ev)
		}
	}
}

// TestLoopPanicReachesTheClient: it is the client that can put the terminal
// back, so a panic on core's goroutines must get there.
func TestLoopPanicReachesTheClient(t *testing.T) {
	got := make(chan Event, 16)
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1)}
	l := NewLoop(c, func(ev Event) { got <- ev })
	l.Start()
	defer l.Stop()
	l.runCmd(func() Event { panic("boom") })
	for {
		select {
		case ev := <-got:
			if p, ok := ev.(PanicMsg); ok {
				if !strings.Contains(p.String(), "boom") {
					t.Fatalf("panic says %q", p.String())
				}
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the panic did not arrive")
		}
	}
}

// TestLoopWhileQuitting: requests are turned away, and nothing is looked at.
func TestLoopWhileQuitting(t *testing.T) {
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1), Quitting: true}
	l := NewLoop(c, nil)
	l.Start()
	defer l.Stop()
	reply := make(chan hooks.Answer, 1)
	l.Post(hooks.Report{Method: "any", Reply: reply})
	select {
	case a := <-reply:
		if !strings.Contains(a.Err, "quitting") {
			t.Fatalf("answered %q", a.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
}

// TestLoopDrawsDoNotPileUp: while the client is busy, however much is drawn,
// it is told to draw again at most once of each kind.
func TestLoopDrawsDoNotPileUp(t *testing.T) {
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1)}
	l := NewLoop(c, nil)
	for range 1000 {
		for _, ev := range []Event{FrameMsg{}, RedrawMsg{}, ChangedMsg{}} {
			l.send(ev)
		}
	}
	l.send("news")
	var got []Event
	for {
		ev, ok := l.next()
		if !ok {
			break
		}
		got = append(got, ev)
	}
	if len(got) != 3 || got[0] != "news" || got[1] != (ChangedMsg{}) {
		t.Fatalf("the client would get %v", got)
	}
}

// TestLoopPanicLetsGoOfMu: a panic in core's handling of an event leaves Mu
// free, for the client to put the terminal back and stop the agents.
func TestLoopPanicLetsGoOfMu(t *testing.T) {
	got := make(chan Event, 16)
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1)}
	l := NewLoop(c, func(ev Event) { got <- ev })
	l.Start()
	defer l.Stop()
	l.Post(GoalMsg{}) // no tab: handling it panics
	for {
		select {
		case ev := <-got:
			if _, ok := ev.(PanicMsg); !ok {
				continue
			}
			if !c.Mu.TryLock() {
				t.Fatal("Mu is still held after the panic")
			}
			c.Mu.Unlock()
			return
		case <-time.After(5 * time.Second):
			t.Fatal("the panic did not arrive")
		}
	}
}
