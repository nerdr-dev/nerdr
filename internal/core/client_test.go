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
