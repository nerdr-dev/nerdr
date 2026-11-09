package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// runningTab gives the active tab an agent that is up and idle.
func runningTab(t *testing.T, m *Model) *core.Tab {
	t.Helper()
	pane, err := term.Start([]string{"cat"}, t.TempDir(), nil, 60, 10, func() {})
	if err != nil {
		t.Skip("no cat:", err)
	}
	t.Cleanup(pane.Close)
	tb := m.Cur()
	tb.Pane, tb.Light = pane, core.StIdle
	return tb
}

// launched runs the command a switch returns, for the agent it starts and
// the first message it is given.
func launched(t *testing.T, cmd tea.Cmd) core.LaunchMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("the switch started nothing")
	}
	l, ok := cmd().(core.LaunchMsg)
	if !ok {
		t.Fatal("the switch did not start an agent")
	}
	return l
}

// With the agent running, the user is asked whether to switch at once or
// to ask it for a handoff first; switching at once has the new agent read
// the old one's conversation.
func TestSwitchAsksWhetherToWaitForAHandoff(t *testing.T) {
	m := testModel(t)
	tb := runningTab(t, m)
	if cmd := m.pickAgent(tb, agents.OpenCode); cmd != nil {
		t.Fatal("switched without asking")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Switch now: opencode reads claude's conversation") || !strings.Contains(view, "Ask claude for a handoff first") {
		t.Fatalf("no choice:\n%s", view)
	}
	_, cmd := m.Update(keyMsg("1"))
	if tb.Agent != agents.OpenCode || tb.Handoff != nil || cmd == nil {
		t.Fatalf("1 switches at once: agent %s, waiting %v", tb.Agent, tb.Handoff != nil)
	}

	m = testModel(t)
	tb = runningTab(t, m)
	m.pickAgent(tb, agents.OpenCode)
	m.Update(keyMsg("2"))
	if tb.Agent != agents.Claude || tb.Handoff == nil {
		t.Fatalf("2 asks for a handoff: agent %s, waiting %v", tb.Agent, tb.Handoff != nil)
	}
}

// An agent asked for a handoff is waited for, and the agent switched to is
// given it.
func TestSwitchWaitsForTheHandoff(t *testing.T) {
	m := testModel(t)
	tb := runningTab(t, m)
	tb.Messages = nil
	m.home().Core.AddMessage(tb, "later work", true)

	m.home().Core.AskHandoff(tb, agents.OpenCode)
	if tb.Handoff == nil || tb.Agent != agents.Claude {
		t.Fatalf("not waiting: %+v", tb.Handoff)
	}
	if len(tb.Messages) != 2 || !strings.Contains(tb.Messages[0].Text, "handoff -") || tb.Messages[1].Text != "later work" {
		t.Fatalf("the request goes ahead of the queue: %+v", tb.Messages)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "waiting for claude's handoff to opencode") {
		t.Fatalf("the panel does not say it is waiting:\n%s", view)
	}

	l := launched(t, fromCore(m.home().Core.OnHandoff(hooks.Report{Method: hooks.Handoff, Pane: tb.HookKey(), Handoff: "THE SUMMARY"})))
	if tb.Agent != agents.OpenCode || tb.Handoff != nil {
		t.Fatalf("not switched: agent %s, waiting %v", tb.Agent, tb.Handoff != nil)
	}
	if len(tb.Messages) != 1 || tb.Messages[0].Text != "later work" {
		t.Fatalf("the request is taken back: %+v", tb.Messages)
	}
	if l.Kind != agents.OpenCode || !strings.Contains(l.Handoff, "THE SUMMARY") || !strings.Contains(l.Handoff, "from claude to you (opencode)") {
		t.Fatalf("the new agent is not given the handoff: %q", l.Handoff)
	}
	m.Update(l)
	if tb.Messages[0].Text != l.Handoff || tb.Messages[1].Text != "later work" {
		t.Fatalf("the handoff goes first to the new agent: %+v", tb.Messages)
	}
}

// An agent that will not send a handoff is not waited for: the new agent
// is told to read its conversation instead.
func TestSwitchGoesAheadWithoutAHandoff(t *testing.T) {
	for name, stall := range map[string]func(*core.Tab){
		"too long":      func(tb *core.Tab) { tb.Handoff.AskedAt = time.Now().Add(-core.HandoffGiveUp - time.Second) },
		"not taken up":  func(tb *core.Tab) { tb.QueuePaused = true },
		"agent stopped": func(tb *core.Tab) { tb.Pane.Close(); tb.Light = core.StExited },
	} {
		t.Run(name, func(t *testing.T) {
			m := testModel(t)
			tb := runningTab(t, m)
			m.home().Core.AskHandoff(tb, agents.OpenCode)
			if m.home().Core.CheckHandoff(tb, time.Now()) != nil {
				t.Fatal("gave up at once")
			}
			stall(tb)
			l := launched(t, fromCore(m.home().Core.CheckHandoff(tb, time.Now())))
			if tb.Agent != agents.OpenCode || tb.QueuePaused || len(tb.Messages) != 0 {
				t.Fatalf("not switched cleanly: agent %s, paused %v, messages %+v", tb.Agent, tb.QueuePaused, tb.Messages)
			}
			if !strings.Contains(l.Handoff, "claude did not write a handoff") {
				t.Fatalf("the new agent is not told to look for itself: %q", l.Handoff)
			}
		})
	}
}

// An agent not running is not asked: the switch is at once.
func TestSwitchFromAStoppedAgent(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	l := launched(t, m.pickAgent(tb, agents.OpenCode))
	if tb.Handoff != nil || l.Kind != agents.OpenCode || !strings.Contains(l.Handoff, "did not write a handoff") {
		t.Fatalf("waiting %v, message %q", tb.Handoff != nil, l.Handoff)
	}
}

// A handoff the user asked for before switching is kept, the switch is
// offered, and the agent switched to gets it without anything asked.
func TestHandoffSentUnasked(t *testing.T) {
	m := testModel(t)
	// The switch menu offers only agents on the PATH, which CI has none
	// of; cat stands in for opencode.
	m.home().Core.Cfg.Agents = map[agents.Kind]agents.Spec{agents.OpenCode: {Command: []string{"cat"}}}
	tb := runningTab(t, m)
	m.home().Core.OnHandoff(hooks.Report{Method: hooks.Handoff, Pane: tb.HookKey(), Handoff: "WRITTEN EARLIER"})
	if tb.Agent != agents.Claude || tb.HandoffReady == nil {
		t.Fatal("an unasked handoff should be kept, not switch")
	}
	if _, ok := m.dialog.(*menu); !ok || !strings.Contains(ansi.Strip(m.View()), "gets the handoff it wrote") {
		t.Fatalf("the switch is not offered:\n%s", ansi.Strip(m.View()))
	}
	m.dialog = nil
	l := launched(t, m.pickAgent(tb, agents.OpenCode))
	if !strings.Contains(l.Handoff, "WRITTEN EARLIER") || len(tb.Messages) != 0 {
		t.Fatalf("message %q, queue %+v", l.Handoff, tb.Messages)
	}

	// Stale, or from another agent than the one now in the tab: not used.
	tb.HandoffReady = &core.HandoffNote{From: agents.Claude, Text: "x", At: time.Now()}
	if tb.ReadyHandoff(time.Now()) != "" {
		t.Fatal("a handoff from claude was offered for opencode")
	}
	tb.HandoffReady = &core.HandoffNote{From: agents.OpenCode, Text: "x", At: time.Now().Add(-core.HandoffFresh - time.Minute)}
	if tb.ReadyHandoff(time.Now()) != "" {
		t.Fatal("a stale handoff was offered")
	}
}

// While waiting, ctrl+q A offers to switch at once or to cancel.
func TestCancelTheSwitch(t *testing.T) {
	m := testModel(t)
	tb := runningTab(t, m)
	tb.Messages = nil
	m.home().Core.AskHandoff(tb, agents.OpenCode)
	m.openSwitchAgent()
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Switch now") || !strings.Contains(view, "Cancel the switch") {
		t.Fatalf("no way out of waiting:\n%s", view)
	}
	m.Update(keyMsg("2"))
	if tb.Handoff != nil || tb.Agent != agents.Claude || len(tb.Messages) != 0 {
		t.Fatalf("not cancelled: waiting %v, agent %s, messages %+v", tb.Handoff != nil, tb.Agent, tb.Messages)
	}
}
