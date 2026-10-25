package app

import (
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// TestQuitLeavesConversationsAlone: an agent stopped by quitting, which
// had just resumed its conversation, is not taken for one that could not
// resume it: nothing is started in its place, and the conversation it
// resumes next time is kept.
func TestQuitLeavesConversationsAlone(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	a := tb.extraAgents()[0]
	a.SessionID, a.resumed = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", true
	old := a.pane

	stop := m.quit()
	// The agent moved to another conversation (a /clear) just as nerdr
	// was quitting: that one is resumed next time.
	moved := "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
	m.Update(hooks.Report{Method: hooks.ReportAgentSession, Pane: a.hookKey(), SessionID: moved})
	if a.SessionID != moved {
		t.Fatalf("the conversation reported while quitting was not taken: %q", a.SessionID)
	}
	stop() // the agents stopped, as the program does out of the screen's way
	m.Update(detectMsg{})
	if a.pane != old || a.SessionID == "" {
		t.Fatalf("the stopped agent was started anew: pane changed %v, session %q", a.pane != old, a.SessionID)
	}
	if ans, _ := askNerdr(m, a, hooks.Report{Method: hooks.Query, What: hooks.QueryAgents}); !strings.Contains(ans.Err, "quitting") {
		t.Fatalf("a report while quitting: %+v", ans)
	}
	s, err := state.Load(m.root)
	if err != nil || len(s.Tabs) == 0 || len(s.Tabs[0].Agents) != 1 || s.Tabs[0].Agents[0].SessionID != a.SessionID {
		t.Fatalf("the conversation to resume was not kept: %v %+v", err, s.Tabs)
	}
}
