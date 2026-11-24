package api

import (
	"os"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"
)

// viewCore is testCore, its agent running cat at 40×10.
func viewCore(t *testing.T) (*term.Pane, *client, *client) {
	t.Helper()
	m := testCore()
	p, err := term.Start([]string{"cat"}, t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	m.Tabs[0].Pane = p
	_, sock, keyFile := serve(t, m, nil)
	phone, tablet := dial(t, sock), dial(t, sock)
	phone.hello(keyFile)
	tablet.hello(keyFile)
	return p, phone, tablet
}

// ask sends r and is the answer's result, failing on an error.
func (c *client) ask(r map[string]any) map[string]any {
	c.t.Helper()
	c.send(r)
	a := c.line()
	if a["error"] != nil {
		c.t.Fatalf("%v: %v", r, a["error"])
	}
	res, _ := a["result"].(map[string]any)
	return res
}

func sizeOf(res map[string]any) [2]int {
	cols, _ := res["cols"].(float64)
	rows, _ := res["rows"].(float64)
	return [2]int{int(cols), int(rows)}
}

// TestViewOverTheAPI: each connection is a viewer; the last active sets
// the size, read says it, and a connection closed takes its view with it.
func TestViewOverTheAPI(t *testing.T) {
	p, phone, tablet := viewCore(t)
	if got := sizeOf(phone.ask(map[string]any{"id": 1, "op": "read", "target": "t1"})); got != [2]int{40, 10} {
		t.Fatalf("read says %v, the program 40×10", got)
	}
	if got := sizeOf(phone.ask(map[string]any{"id": 2, "op": "view", "target": "t1", "cols": 50, "rows": 20})); got != [2]int{50, 20} {
		t.Fatalf("view answered %v", got)
	}
	if got := sizeOf(tablet.ask(map[string]any{"id": 3, "op": "view", "agent": "t1", "cols": 120, "rows": 40})); got != [2]int{120, 40} {
		t.Fatalf("view answered %v", got)
	}
	// The phone renews: the tablet's size holds, and the answer says so.
	if got := sizeOf(phone.ask(map[string]any{"id": 4, "op": "view", "target": "t1", "cols": 50, "rows": 20})); got != [2]int{120, 40} {
		t.Fatalf("renewed view answered %v", got)
	}
	// The phone types: its size.
	phone.ask(map[string]any{"id": 5, "op": "type", "target": "t1", "keys": []string{"x"}})
	if got := sizeOf(phone.ask(map[string]any{"id": 6, "op": "read", "target": "t1"})); got != [2]int{50, 20} {
		t.Fatalf("read after typing says %v", got)
	}
	// The phone's connection closes: the tablet's size.
	_ = phone.c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if w, h := p.Size(); w == 120 && h == 40 {
			break
		}
		if time.Now().After(deadline) {
			w, h := p.Size()
			t.Fatalf("%d×%d with the phone gone, want the tablet's 120×40", w, h)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// unview: no view left, the size stays.
	tablet.ask(map[string]any{"id": 7, "op": "unview", "target": "t1"})
	if got := sizeOf(tablet.ask(map[string]any{"id": 8, "op": "read", "target": "t1"})); got != [2]int{120, 40} {
		t.Fatalf("read after unview says %v", got)
	}

	tablet.send(map[string]any{"id": 9, "op": "view", "target": "nope", "cols": 50, "rows": 20})
	if a := tablet.line(); a["error"] == nil {
		t.Fatalf("a view of no program: %v", a)
	}
}
