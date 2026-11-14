package api

import (
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
)

func names(evs []Event) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Event)
	}
	return out
}

func TestDiff(t *testing.T) {
	ctx1, ctx2 := 10.0, 55.0
	a := core.APIState{
		Tabs: []core.APITab{
			{ID: "t1", Name: "one", Agents: []core.APIAgent{{ID: "t1", Num: 1, State: "working", Context: &ctx1}, {ID: "x", Num: 2, State: "idle"}}},
			{ID: "t2", Name: "two", Agents: []core.APIAgent{{ID: "t2", Num: 1, State: "idle"}}},
		},
		Waiting:   []string{},
		Questions: []core.APIQuestion{{ID: 1, Kind: "repo_setup"}},
	}
	if evs := Diff(a, a); len(evs) != 0 {
		t.Fatalf("no change: %v", names(evs))
	}

	// Only the context used: no news.
	b := a
	b.Tabs = []core.APITab{a.Tabs[0], a.Tabs[1]}
	b.Tabs[0].Agents = []core.APIAgent{{ID: "t1", Num: 1, State: "working", Context: &ctx2}, a.Tabs[0].Agents[1]}
	if evs := Diff(a, b); len(evs) != 0 {
		t.Fatalf("context only: %v", names(evs))
	}

	c := core.APIState{
		Tabs: []core.APITab{
			{ID: "t1", Name: "one", Status: "tests run", Agents: []core.APIAgent{{ID: "t1", Num: 1, State: "blocked"}}},
			{ID: "t3", Name: "three", Agents: []core.APIAgent{{ID: "t3", Num: 1, State: "starting"}}},
		},
		Waiting:   []string{"t1"},
		Questions: []core.APIQuestion{{ID: 2, Kind: "plugin_run"}},
	}
	evs := Diff(a, c)
	want := []string{"tab", "agent", "agent", "tab_opened", "agent", "tab_closed", "waiting", "question", "answered"}
	got := names(evs)
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events %v, want %v", got, want)
		}
	}
	if evs[1].Agent.State != "blocked" || evs[1].Was != "working" || evs[1].Name != "one" {
		t.Fatalf("agent %+v was %q", evs[1].Agent, evs[1].Was)
	}
	if evs[2].Agent.ID != "x" || evs[2].Agent.State != "gone" || evs[2].Was != "idle" {
		t.Fatalf("gone %+v was %q", evs[2].Agent, evs[2].Was)
	}
	if evs[5].TabID != "t2" || evs[5].Name != "two" {
		t.Fatalf("closed %+v", evs[5])
	}
	if evs[8].QuestionID != 1 || evs[7].Question.ID != 2 {
		t.Fatalf("questions %+v %+v", evs[7], evs[8])
	}
}
