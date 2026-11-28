package api

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
)

// TestSpinoffOverTheSocket: the parts a tab's agent sent are in the state
// and come as a spinoff event; spinoff_skip answers the first alone, and
// the event once none wait has spinoff null.
func TestSpinoffOverTheSocket(t *testing.T) {
	old := pollEvery
	pollEvery = 20 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	m := testCore()
	m.Client = core.NoClient{}
	_, sock, keyFile := serve(t, m, nil)
	c := dial(t, sock)
	c.hello(keyFile)
	c.send(map[string]any{"id": 1, "op": "subscribe"})
	c.line()

	m.Mu.Lock()
	m.Tabs[0].Spinoff = &spinoff.Pending{Next: []spinoff.Part{{ID: "p1", Branch: "fix-a", Summary: "A.", Prompt: "Do A."}}}
	m.Mu.Unlock()
	// next is the next line that is the event named, or the answer of id.
	next := func(event string, id float64) map[string]any {
		t.Helper()
		for {
			l := c.line()
			if l["event"] == event || (event == "" && l["id"] == id) {
				return l
			}
		}
	}
	ev := next("spinoff", 0)
	so, ok := ev["spinoff"].(map[string]any)
	if ev["tab_id"] != "t1" || !ok {
		t.Fatalf("event %v", ev)
	}
	pt := so["parts"].([]any)[0].(map[string]any)
	if pt["id"] != "p1" || pt["branch"] != "fix-a" || pt["prompt_ready"] != true || pt["prompt"] != nil || pt["kind"] != "claude" {
		t.Fatalf("part %v", pt)
	}

	c.send(map[string]any{"id": 2, "op": "state"})
	tab := next("", 2)["result"].(map[string]any)["tabs"].([]any)[0].(map[string]any)
	if tab["spinoff"] == nil {
		t.Fatalf("no spinoff in the state: %v", tab)
	}

	c.send(map[string]any{"id": 3, "op": "spinoff_skip", "tab": "t1", "part": "p2"})
	if a := next("", 3); !strings.Contains(a["error"].(string), "no part p2") {
		t.Fatalf("answer %v", a)
	}
	c.send(map[string]any{"id": 4, "op": "spinoff_create", "tab": "t1", "part": "p1", "kind": "gemini"})
	if a := next("", 4); !strings.Contains(a["error"].(string), `no agent "gemini"`) {
		t.Fatalf("answer %v", a)
	}
	c.send(map[string]any{"id": 5, "op": "spinoff_skip", "tab": "t1", "part": "p1"})
	if a := next("", 5); a["error"] != nil {
		t.Fatalf("answer %v", a)
	}
	ev = next("spinoff", 0)
	if v, ok := ev["spinoff"]; !ok || v != nil || ev["tab_id"] != "t1" {
		t.Fatalf("the spinoff is done: %v", ev)
	}
}
