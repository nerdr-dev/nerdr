package app

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// idleAgent gives tab tb a running pane, done, seen and left ago.
func idleAgent(t *testing.T, tb *core.Tab, ago time.Duration) {
	t.Helper()
	p, err := term.Start([]string{"sh", "-c", "sleep 60"}, t.TempDir(), nil, 60, 12, func() {})
	if err != nil {
		t.Skip("no sh:", err)
	}
	t.Cleanup(p.Close)
	tb.Pane, tb.Light, tb.Changed = p, core.StIdle, time.Now().Add(-ago)
	tb.LastActive = time.Now().Add(-ago) // and left then
	tb.SessionID = "s-" + tb.ID
}

// An agent done, seen and out of sight for sleep_after is stopped, and
// started again, carrying its conversation on, when its tab is visited.
func TestIdleAgentSleepsAndWakes(t *testing.T) {
	m := testModel(t)
	m.Cfg.SleepAfter = 30
	shown, other := m.Cur(), m.Tabs[1]
	idleAgent(t, shown, time.Hour)
	idleAgent(t, other, time.Hour)
	m.SleepIdle(time.Now())
	if shown.Light == core.StAsleep {
		t.Fatal("the tab on screen was put to sleep")
	}
	if other.Light != core.StAsleep || other.Pane != nil {
		t.Fatalf("other: status %v, pane %v", other.Light, other.Pane)
	}
	if lightGlyph(core.StAsleep) != "z" {
		t.Errorf("asleep light %q", lightGlyph(core.StAsleep))
	}
	// The screen is not read for an agent with no process: it stays
	// asleep rather than turning exited.
	m.DetectAgent(other, false, time.Now())
	if other.Light != core.StAsleep {
		t.Fatalf("asleep agent read as %v", other.Light)
	}
	m.WakeTab(other)
	if other.Light != core.StStarting {
		t.Fatalf("woken agent is %v", other.Light)
	}
}

// An agent is left running while it has something to do, or could not
// carry its conversation on.
func TestAgentStaysAwake(t *testing.T) {
	m := testModel(t)
	m.Cfg.SleepAfter = 30
	tb := m.Tabs[1]
	idleAgent(t, tb, time.Hour)
	tb.Messages = []state.Message{{Text: "next", Auto: true}}
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep with a task waiting")
	}
	tb.Messages = nil
	tb.Changed = time.Now().Add(-time.Minute)
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep before sleep_after")
	}
	tb.Changed = time.Now().Add(-time.Hour)
	tb.Agent, tb.SessionID = "copilot", ""
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep with no conversation to carry on")
	}
	m.Cfg.SleepAfter = 0
	tb.Agent, tb.SessionID = "claude", "s"
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep with sleep_after 0")
	}
}

// A tab gone cold is not started with nerdr, but asleep until visited;
// the others are started, the one on screen first.
func TestColdTabSleepsFromTheStart(t *testing.T) {
	m := testModel(t)
	days := 5
	m.Cfg.ColdDays = &days
	cold := m.Tabs[2]
	cold.LastActive = time.Now().Add(-10 * 24 * time.Hour)
	m.Tabs[1].LastActive = time.Now()
	m.restoreStarts()
	if cold.Light != core.StAsleep {
		t.Fatalf("cold tab is %v", cold.Light)
	}
	if m.Tabs[1].Light == core.StAsleep {
		t.Fatal("a tab in use was not started")
	}
}

// beside gives tab o another agent, in a pane of its own.
func beside(o *core.Tab) *core.Tab {
	a := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: o.ID + "-2", Agent: "claude", Dir: o.Dir}}, PartOf: o}
	o.PaneList = append(o.Panes(), &core.PaneTab{Kind: core.PaneAgent, A: a, Opened: time.Now()})
	return a
}

// A tab's agents sleep together, so what keeps one awake keeps them all:
// a task waiting for the agent beside the tab's own, or a review of it;
// and a cold tab with such a task is started with nerdr.
func TestBesideAgentKeepsTheTabAwake(t *testing.T) {
	m := testModel(t)
	m.Cfg.SleepAfter = 30
	tb := m.Tabs[1]
	idleAgent(t, tb, time.Hour)
	a := beside(tb)
	idleAgent(t, a, time.Hour)
	if !m.CanSleep(tb, time.Now()) {
		t.Fatal("the tab cannot sleep to begin with")
	}
	a.Messages = []state.Message{{Text: "next", Auto: true}}
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep with a task waiting for the agent beside")
	}
	a.Messages = nil
	a.Review = &core.ReviewRun{}
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep with the agent beside under review")
	}
	a.Review = nil

	days := 5
	m.Cfg.ColdDays = &days
	cold := m.Tabs[2]
	cold.LastActive = time.Now().Add(-10 * 24 * time.Hour)
	beside(cold).Messages = []state.Message{{Text: "next", Auto: true}}
	m.restoreStarts()
	if cold.Light == core.StAsleep {
		t.Error("a cold tab with a task waiting was not started")
	}
}

// A tab is out of sight for sleep_after before its agents sleep, however
// long they have been idle.
func TestRecentlySeenTabStaysAwake(t *testing.T) {
	m := testModel(t)
	m.Cfg.SleepAfter = 30
	tb := m.Tabs[1]
	idleAgent(t, tb, time.Hour)
	tb.LastActive = time.Now().Add(-time.Minute) // left a minute ago
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep a minute after it was left")
	}
	tb.LastActive = time.Now().Add(-time.Hour)
	tb.ShownAt = time.Now().Add(-time.Minute) // beside the other in a split
	if m.CanSleep(tb, time.Now()) {
		t.Error("asleep a minute after it was on screen")
	}
}

// An agent nerdr does not know carries on only by a resume command,
// reported or configured: a conversation id alone does not let it.
func TestCustomAgentSleepsOnlyWithAWayBack(t *testing.T) {
	m := testModel(t)
	m.Cfg.Agents = map[agents.Kind]agents.Spec{"my-agent": {Command: []string{"my-agent"}}}
	a := m.Tabs[1]
	a.Agent, a.SessionID = "my-agent", "s-1"
	if m.CanCarryOn(a) {
		t.Error("carries on by an id it cannot pass on")
	}
	a.ResumeArgv = []string{"other", "--resume", "s-1"}
	if m.CanCarryOn(a) {
		t.Error("carries on by another program's command")
	}
	a.ResumeArgv = []string{"my-agent", "--resume", "s-1"}
	if !m.CanCarryOn(a) {
		t.Error("does not carry on by its own reported command")
	}
	a.ResumeArgv = nil
	m.Cfg.Agents["my-agent"] = agents.Spec{Command: []string{"my-agent"}, ResumeArgs: []string{"--continue"}}
	if !m.CanCarryOn(a) {
		t.Error("does not carry on by resume_args")
	}
}
