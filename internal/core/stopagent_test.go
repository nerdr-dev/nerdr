package core

import (
	"os"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"
)

// TestReviewerIsForgottenOnlyOnceStopped: closing a tab whose own agent
// has ended, with a reviewer still running, removes what the reviewer's
// hooks kept only once the reviewer's program has stopped. A hook of one
// still running would make the files anew, with another lock than the one
// a hook beside it holds.
func TestReviewerIsForgottenOnlyOnceStopped(t *testing.T) {
	live, err := term.Start([]string{"sh", "-c", "stty -echo; sleep 30"}, t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(live.Close)

	type forgot struct {
		key     string
		stopped bool
	}
	got := make(chan forgot, 4)
	was := forgetAgent
	forgetAgent = func(key string) {
		stopped := true
		if key == "reviewer-key" {
			stopped, _, _ = live.Exited()
		}
		got <- forgot{key, stopped}
	}
	t.Cleanup(func() { forgetAgent = was })

	m := &Core{}
	tab := &Tab{}
	tab.ID, tab.Name, tab.HookID = "t1", "tab", "agent-key" // its program has ended: no Pane
	r := &Tab{}
	r.ID, r.HookID, r.Pane = "r1", "reviewer-key", live
	tab.Review = &ReviewRun{Seats: []*Seat{{R: r}}}
	m.Tabs = []*Tab{tab}

	cmd := m.CloseTab(tab, false, false, nil)
	if cmd == nil {
		t.Fatal("the tab was not closed")
	}
	cmd()

	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(seen) < 2 {
		select {
		case f := <-got:
			if !f.stopped {
				t.Fatalf("%s was forgotten while its program still ran", f.key)
			}
			seen[f.key] = true
		case <-deadline:
			t.Fatalf("forgotten: %v, want the agent's and the reviewer's", seen)
		}
	}
}
