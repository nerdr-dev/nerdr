package core

import (
	"os"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// TestTypedIsCores: what is typed to an agent but Claude, through the
// program core gives once it has the keyboard or by Type, is followed in
// core, and the line sent is noted; Claude's, read from its log, is not.
func TestTypedIsCores(t *testing.T) {
	for _, kind := range []agents.Kind{agents.Codex, agents.Claude} {
		p, err := term.Start([]string{"sh", "-c", "stty -echo; sleep 30"}, t.TempDir(), os.Environ(), 40, 10, func() {})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		tb := &Tab{Pane: p}
		tb.Agent = kind
		c := &Core{Tabs: []*Tab{tb}}
		ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
		if _, ok := c.Program(ref).(*term.Pane); !ok {
			t.Fatalf("%s: typed followed before the keyboard was given", kind)
		}
		KeysTo{ref}.Do(c)
		c.Program(ref).Input([]byte("fix the parser\r"))
		Type{Ref: ref, B: []byte("and the tests\r")}.Do(c)
		var got []string
		if tb.Typed != nil {
			got = append(got, tb.Typed.Take())
		}
		switch {
		case kind == agents.Claude && tb.Typed != nil:
			t.Errorf("Claude's typing followed")
		case kind != agents.Claude && (len(got) != 1 || got[0] != "and the tests"):
			t.Errorf("%s: lines taken %q", kind, got)
		}
	}
}
