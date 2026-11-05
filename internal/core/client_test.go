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
