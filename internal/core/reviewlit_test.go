package core

import (
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// TestFindingsToDecideLit: a review done whose findings wait for the user
// to say whether they go to the agent shows red; once sent, set to go by
// themselves or dropped, or with only an approval to tell of, it does not.
func TestFindingsToDecideLit(t *testing.T) {
	m := restored(t)
	a := m.Tabs[0]
	a.Light = StDone
	fix := state.Message{Text: "fix these"}
	a.Messages = []state.Message{fix}
	a.ReviewDone = &state.ReviewDone{Brief: "2 findings", Msg: fix}
	if got := a.Lit(); got != StBlocked {
		t.Errorf("findings waiting: %v, want blocked", got)
	}

	a.Messages[0].Auto = true // set to go from F3
	if got := a.Lit(); got != StDone {
		t.Errorf("findings set to go: %v, want done", got)
	}

	a.Messages = nil // sent
	if got := a.Lit(); got != StDone {
		t.Errorf("findings sent: %v, want done", got)
	}

	a.ReviewDone = &state.ReviewDone{Brief: "approved", Approved: true}
	if got := a.Lit(); got != StDone {
		t.Errorf("approved, nothing to send: %v, want done", got)
	}
}

// TestLitWorkOverDone: of the agents in one tab, one asking something is
// red over all, and one still working yellow over the others' green, so a
// tab whose agents handed their part back to one that carries on does not
// look done; with a process left running, cyan is over green too.
func TestLitWorkOverDone(t *testing.T) {
	m := restored(t)
	tb := m.Tabs[0]
	tb.PaneList = []*PaneTab{{Kind: PaneAgent, A: tb}}
	var extra []*Tab
	for range 4 {
		a := &Tab{Light: StDone}
		extra = append(extra, a)
		tb.PaneList = append(tb.PaneList, &PaneTab{Kind: PaneAgent, A: a})
	}
	for _, c := range []struct {
		main, other, want Status
	}{
		{StWorking, StDone, StWorking},
		{StDone, StWorking, StWorking},
		{StBackground, StDone, StBackground},
		{StWorking, StBlocked, StBlocked},
		{StIdle, StIdle, StDone},
	} {
		tb.Light = c.main
		extra[2].Light = c.other
		if got := tb.Lit(); got != c.want {
			t.Errorf("main %v, one other %v, rest done: %v, want %v", c.main, c.other, got, c.want)
		}
	}
}
