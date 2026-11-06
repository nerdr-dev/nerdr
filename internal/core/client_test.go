package core

import (
	"strconv"
	"testing"
)

func TestExpandKeysPutsInTheClientsKeys(t *testing.T) {
	key := func(c string) string { return "ctrl+b " + c }
	screen := func(n int) string { return "F" + strconv.Itoa(n) }
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"press " + KeyRef("a s") + " now", "press ctrl+b a s now"},
		{ScreenRef(3) + " and " + KeyRef("x"), "F3 and ctrl+b x"},
		{"broken \x1fk", "broken \x1fk"},
	} {
		if got := ExpandKeys(c.in, key, screen); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTabRefIsTheCoresOwn(t *testing.T) {
	a, b := &Tab{}, &Tab{}
	a.ID, b.ID = "1", "2"
	local := &Core{Tabs: []*Tab{a, b}}
	there := &Core{ID: "box", Tabs: []*Tab{a}}
	if got := local.ByRef(local.Ref(b)); got != b {
		t.Fatalf("the local core's own tab: %v", got)
	}
	if got := local.ByRef(there.Ref(a)); got != nil {
		t.Fatal("a tab of another core with the same id taken as this one's")
	}
}

// TestRefsFindAgentsAndPanes: every agent is found by its TabRef, a reviewer
// and another agent in a tab's panes too, and a pane by its PaneRef; one with
// no id yet is given one.
func TestRefsFindAgentsAndPanes(t *testing.T) {
	tb := &Tab{}
	other := &Tab{PartOf: tb}
	other.ID = "o"
	reviewer := &Tab{ReviewOf: tb}
	reviewer.ID = "r"
	p := &PaneTab{Kind: PaneAgent, A: other}
	q := &PaneTab{Kind: PaneReview, A: reviewer, Of: tb}
	tb.PaneList = []*PaneTab{{Kind: PaneAgent, A: tb}, p, q}
	c := &Core{Tabs: []*Tab{tb}}
	if r := c.Ref(tb); r.Tab == "" || c.ByRef(r) != tb {
		t.Fatalf("a tab with no id: %+v", r)
	}
	for _, a := range []*Tab{other, reviewer} {
		if c.ByRef(c.Ref(a)) != a {
			t.Errorf("agent %s not found", a.ID)
		}
	}
	if o, got := c.PaneByRef(c.PaneRef(q)); o != tb || got != q {
		t.Fatalf("pane: %v %v", o, got)
	}
	if c.ByRef(TabRef{}) != nil {
		t.Fatal("the zero TabRef named a tab")
	}
}
