package core

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// A long wait of core's ends when its loop stops, rather than outliving it.
func TestTickEndsWithTheLoop(t *testing.T) {
	c := &Core{Client: NoClient{}, Wake: make(chan struct{}, 1)}
	l := NewLoop(c, nil)
	cmd := c.Tick(time.Hour, func(time.Time) Event { return "late" })
	got := make(chan Event, 1)
	go func() { got <- cmd() }()
	l.Stop()
	select {
	case ev := <-got:
		if ev != nil {
			t.Fatalf("got %v after the stop", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait outlived the loop")
	}
	if c.Tick(time.Hour, func(time.Time) Event { return "late" })() != nil {
		t.Fatal("a wait begun after the stop waited")
	}
}

// Turns ending one after another leave one wait for the recap, put off to
// after the last.
func TestRecapLaterWaitsOnce(t *testing.T) {
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "a", Agent: agents.Claude}}}
	c := &Core{Client: NoClient{}, Tabs: []*Tab{tb}}
	first := c.RecapLater(tb)
	if first == nil {
		t.Fatal("no wait for the recap")
	}
	if c.RecapLater(tb) != nil {
		t.Fatal("a second wait for the same tab")
	}
	// The wait ends; the second turn's recap is not due yet.
	cmd, ok := c.Handle(RecapMsg{T: tb, Recap: true})
	if !ok || cmd == nil || !tb.RecapWaiting {
		t.Fatal("the wait was not put off for the last turn")
	}
	tb.RecapDue = time.Now().Add(-time.Second)
	c.Handle(RecapMsg{T: tb, Recap: true})
	if tb.RecapWaiting {
		t.Fatal("the wait did not end")
	}
	if c.RecapLater(tb) == nil {
		t.Fatal("no wait for a turn after")
	}
}
