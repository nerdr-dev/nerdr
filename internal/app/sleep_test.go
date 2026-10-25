package app

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// idleAgent gives tab tb a running pane, done, seen and left ago.
func idleAgent(t *testing.T, tb *tab, ago time.Duration) {
	t.Helper()
	p, err := term.Start([]string{"sh", "-c", "sleep 60"}, t.TempDir(), nil, 60, 12, func() {})
	if err != nil {
		t.Skip("no sh:", err)
	}
	t.Cleanup(p.Close)
	tb.pane, tb.status, tb.changed = p, stIdle, time.Now().Add(-ago)
	tb.LastActive = time.Now().Add(-ago) // and left then
	tb.SessionID = "s-" + tb.ID
}

// An agent done, seen and out of sight for sleep_after is stopped, and
// started again, carrying its conversation on, when its tab is visited.
func TestIdleAgentSleepsAndWakes(t *testing.T) {
	m := testModel(t)
	m.cfg.SleepAfter = 30
	shown, other := m.cur(), m.tabs[1]
	idleAgent(t, shown, time.Hour)
	idleAgent(t, other, time.Hour)
	m.sleepIdle(time.Now())
	if shown.status == stAsleep {
		t.Fatal("the tab on screen was put to sleep")
	}
	if other.status != stAsleep || other.pane != nil {
		t.Fatalf("other: status %v, pane %v", other.status, other.pane)
	}
	if lightGlyph(stAsleep) != "z" {
		t.Errorf("asleep light %q", lightGlyph(stAsleep))
	}
	// The screen is not read for an agent with no process: it stays
	// asleep rather than turning exited.
	m.detectAgent(other, false, time.Now())
	if other.status != stAsleep {
		t.Fatalf("asleep agent read as %v", other.status)
	}
	m.wakeTab(other)
	if other.status != stStarting {
		t.Fatalf("woken agent is %v", other.status)
	}
}

// An agent is left running while it has something to do, or could not
// carry its conversation on.
func TestAgentStaysAwake(t *testing.T) {
	m := testModel(t)
	m.cfg.SleepAfter = 30
	tb := m.tabs[1]
	idleAgent(t, tb, time.Hour)
	tb.Messages = []state.Message{{Text: "next", Auto: true}}
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep with a task waiting")
	}
	tb.Messages = nil
	tb.changed = time.Now().Add(-time.Minute)
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep before sleep_after")
	}
	tb.changed = time.Now().Add(-time.Hour)
	tb.Agent, tb.SessionID = "copilot", ""
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep with no conversation to carry on")
	}
	m.cfg.SleepAfter = 0
	tb.Agent, tb.SessionID = "claude", "s"
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep with sleep_after 0")
	}
}

// A tab gone cold is not started with nerdr, but asleep until visited;
// the others are started, the one on screen first.
func TestColdTabSleepsFromTheStart(t *testing.T) {
	m := testModel(t)
	days := 5
	m.cfg.ColdDays = &days
	cold := m.tabs[2]
	cold.LastActive = time.Now().Add(-10 * 24 * time.Hour)
	m.tabs[1].LastActive = time.Now()
	m.restoreStarts()
	if cold.status != stAsleep {
		t.Fatalf("cold tab is %v", cold.status)
	}
	if m.tabs[1].status == stAsleep {
		t.Fatal("a tab in use was not started")
	}
}

// beside gives tab o another agent, in a pane of its own.
func beside(o *tab) *tab {
	a := &tab{Tab: state.Tab{ID: o.ID + "-2", Agent: "claude", Dir: o.Dir}, partOf: o}
	o.paneList = append(o.panes(), &paneTab{kind: paneAgent, a: a, used: time.Now()})
	return a
}

// A tab's agents sleep together, so what keeps one awake keeps them all:
// a task waiting for the agent beside the tab's own, or a review of it;
// and a cold tab with such a task is started with nerdr.
func TestBesideAgentKeepsTheTabAwake(t *testing.T) {
	m := testModel(t)
	m.cfg.SleepAfter = 30
	tb := m.tabs[1]
	idleAgent(t, tb, time.Hour)
	a := beside(tb)
	idleAgent(t, a, time.Hour)
	if !m.canSleep(tb, time.Now()) {
		t.Fatal("the tab cannot sleep to begin with")
	}
	a.Messages = []state.Message{{Text: "next", Auto: true}}
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep with a task waiting for the agent beside")
	}
	a.Messages = nil
	a.review = &reviewRun{}
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep with the agent beside under review")
	}
	a.review = nil

	days := 5
	m.cfg.ColdDays = &days
	cold := m.tabs[2]
	cold.LastActive = time.Now().Add(-10 * 24 * time.Hour)
	beside(cold).Messages = []state.Message{{Text: "next", Auto: true}}
	m.restoreStarts()
	if cold.status == stAsleep {
		t.Error("a cold tab with a task waiting was not started")
	}
}

// A tab is out of sight for sleep_after before its agents sleep, however
// long they have been idle.
func TestRecentlySeenTabStaysAwake(t *testing.T) {
	m := testModel(t)
	m.cfg.SleepAfter = 30
	tb := m.tabs[1]
	idleAgent(t, tb, time.Hour)
	tb.LastActive = time.Now().Add(-time.Minute) // left a minute ago
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep a minute after it was left")
	}
	tb.LastActive = time.Now().Add(-time.Hour)
	tb.shownAt = time.Now().Add(-time.Minute) // beside the other in a split
	if m.canSleep(tb, time.Now()) {
		t.Error("asleep a minute after it was on screen")
	}
}

// An agent nerdr does not know carries on only by a resume command,
// reported or configured: a conversation id alone does not let it.
func TestCustomAgentSleepsOnlyWithAWayBack(t *testing.T) {
	m := testModel(t)
	m.cfg.Agents = map[agents.Kind]agents.Spec{"my-agent": {Command: []string{"my-agent"}}}
	a := m.tabs[1]
	a.Agent, a.SessionID = "my-agent", "s-1"
	if m.canCarryOn(a) {
		t.Error("carries on by an id it cannot pass on")
	}
	a.ResumeArgv = []string{"other", "--resume", "s-1"}
	if m.canCarryOn(a) {
		t.Error("carries on by another program's command")
	}
	a.ResumeArgv = []string{"my-agent", "--resume", "s-1"}
	if !m.canCarryOn(a) {
		t.Error("does not carry on by its own reported command")
	}
	a.ResumeArgv = nil
	m.cfg.Agents["my-agent"] = agents.Spec{Command: []string{"my-agent"}, ResumeArgs: []string{"--continue"}}
	if !m.canCarryOn(a) {
		t.Error("does not carry on by resume_args")
	}
}
