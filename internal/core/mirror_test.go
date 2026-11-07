package core

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// mirrored is a core of two tabs pointing at each other in every way a
// tab may: an agent beside the tab's own, a reviewer at its seat and one
// still starting, an overlap, a plugin's run and screen, a summary.
func mirrored() (*Core, *Tab, *Tab) {
	m := &Core{ID: "", Root: "/repos", Gen: 7}
	a, b := &Tab{}, &Tab{}
	a.ID, a.Name, a.Agent = "a", "parser", "claude"
	a.Messages = []state.Message{{Text: "next: the tests", Auto: true}}
	a.Review = &ReviewRun{Id: "r1", Round: 2}
	a.Light = StWorking
	a.Changed = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	b.ID, b.Name, b.Agent = "b", "docs", "codex"
	b.Tab.Review = &state.Review{} // the session's, beside core's own Review

	beside := &Tab{}
	beside.ID, beside.Name, beside.PartOf = "a2", "helper", a
	seated := &Tab{}
	seated.ID, seated.ReviewOf = "rev", a
	starting := &Tab{}
	starting.ID, starting.ReviewOf = "rev2", a
	a.Review.Seats = []*Seat{{R: seated}}
	a.PaneList = []*PaneTab{
		{ID: "p1", Kind: PaneAgent, A: a},
		{ID: "p2", Kind: PaneAgent, A: beside},
		{ID: "p3", Kind: PaneReview, A: starting, Of: a},
		{ID: "p4", Kind: PaneShell, N: 1},
	}
	b.Summary = &Summary{T: b, Add: 3}
	m.Tabs = []*Tab{a, b}
	m.Overlaps = map[string][]Overlap{"a": {{With: b, Files: []string{"x/go.mod"}}}}
	run := &PluginRun{Id: "run1", T: a, From: beside, Values: map[string]any{"n": 2}}
	m.PluginRuns = PluginRuns{Runs: map[string]*PluginRun{"k": run}, ByID: map[string]*PluginRun{"run1": run}}
	m.PluginUIs = map[string]*PluginUI{"a\x00/p": {T: a, Dir: "/p", Key: "a\x00/p"}}
	m.Asking = []*Question{{ID: 1, Kind: AskPluginRun, A: m.Ref(a), Vals: map[string]any{"x": "y"}}}
	return m, a, b
}

func applyAll(t *testing.T, r *Mirror, parts []Part) {
	t.Helper()
	for _, p := range parts {
		if err := r.Apply(p); err != nil {
			t.Fatalf("apply %s: %v", p.Key, err)
		}
	}
}

// TestMirror: a client's copy has all core sends, pointing at its own tabs.
func TestMirror(t *testing.T) {
	m, _, _ := mirrored()
	var sent Sent
	parts, err := m.Parts(&sent)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("%d parts", len(parts))
	}
	r := NewMirror(&Core{})
	// The order core's part comes in does not matter.
	applyAll(t, r, append(parts[len(parts)-1:], parts[:len(parts)-1]...))

	c := r.C
	if len(c.Tabs) != 2 || c.Tabs[0].Name != "parser" || c.Tabs[1].Name != "docs" || c.Root != "/repos" {
		t.Fatalf("copy: %+v", c.Tabs)
	}
	a, b := c.Tabs[0], c.Tabs[1]
	beside, starting, seated := a.PaneList[1].A, a.PaneList[2].A, a.Review.Seats[0].R
	switch {
	case a.PaneList[0].A != a:
		t.Error("the tab's own pane is not the tab")
	case beside == nil || beside.Name != "helper" || beside.PartOf != a:
		t.Errorf("agent beside: %+v", beside)
	case seated == nil || seated.ID != "rev" || seated.ReviewOf != a:
		t.Errorf("seated reviewer: %+v", seated)
	case starting == nil || starting.ReviewOf != a || a.PaneList[2].Of != a:
		t.Errorf("starting reviewer: %+v", starting)
	case b.Summary == nil || b.Summary.T != b:
		t.Error("summary")
	case c.Overlaps["a"][0].With != b:
		t.Error("overlap")
	case c.PluginRuns.Runs["k"].T != a || c.PluginRuns.ByID["run1"].From != beside:
		t.Error("plugin run")
	case c.PluginUIs["a\x00/p"].T != a:
		t.Error("plugin screen")
	case c.ByRef(TabRef{Tab: "rev2"}) != starting:
		t.Error("ByRef in the copy")
	case b.Tab.Review == nil || a.Review.Round != 2:
		t.Error("the two Reviews")
	case len(c.Asking) != 1 || c.Asking[0].A != (TabRef{Tab: "a"}):
		t.Error("question")
	}

	// The copy counts its changes in a Gen of its own.
	if c.Gen != uint64(len(parts)) {
		t.Errorf("Gen %d after %d parts", c.Gen, len(parts))
	}

	// The copy sends what core sent, but for its Gen.
	c.Gen = m.Gen
	again, err := c.Parts(&Sent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(parts) {
		t.Fatalf("%d parts again", len(again))
	}
	for i := range parts {
		if parts[i].Key != again[i].Key || !bytes.Equal(parts[i].Body, again[i].Body) {
			t.Errorf("%s differs:\n%s\n%s", parts[i].Key, parts[i].Body, again[i].Body)
		}
	}
}

// TestMirrorChanges: only what changed is sent, and the copy's tabs stay
// the same tabs.
func TestMirrorChanges(t *testing.T) {
	m, a, b := mirrored()
	var sent Sent
	parts, _ := m.Parts(&sent)
	r := NewMirror(&Core{})
	applyAll(t, r, parts)
	ra, rb, rbeside := r.C.Tabs[0], r.C.Tabs[1], r.C.Tabs[0].PaneList[1].A

	if parts, _ := m.Parts(&sent); len(parts) != 0 {
		t.Errorf("%d parts with nothing changed", len(parts))
	}
	b.Name = "docs 2"
	a.PaneList[1].A.Light = StDone
	parts, _ = m.Parts(&sent)
	keys := []string{}
	for _, p := range parts {
		keys = append(keys, p.Key)
	}
	if !reflect.DeepEqual(keys, []string{PartTab("a"), PartTab("b")}) {
		t.Errorf("changed: %v", keys)
	}
	applyAll(t, r, parts)
	if r.C.Tabs[1] != rb || rb.Name != "docs 2" || r.C.Tabs[0] != ra || ra.PaneList[1].A != rbeside || rbeside.Light != StDone {
		t.Error("the copy's tabs were not kept")
	}
	if rb.Summary.T != rb || r.C.Overlaps["a"][0].With != rb {
		t.Error("pointers into a tab sent again")
	}

	// A tab closed goes from the copy, its agents with it; sent again
	// should it come back.
	m.Tabs = []*Tab{b}
	m.Overlaps, m.PluginRuns, m.PluginUIs = nil, PluginRuns{}, nil
	parts, _ = m.Parts(&sent)
	applyAll(t, r, parts)
	if len(r.C.Tabs) != 1 || r.C.Tabs[0] != rb || r.C.ByRef(TabRef{Tab: "a2"}) != nil || len(r.agents) != 1 {
		t.Errorf("after close: %d tabs, %d agents", len(r.C.Tabs), len(r.agents))
	}
	m.Tabs = []*Tab{b, a}
	parts, _ = m.Parts(&sent)
	if len(parts) != 2 {
		t.Errorf("%d parts for a tab back", len(parts))
	}

	// One fallen behind is sent all.
	sent.Reset()
	if parts, _ := m.Parts(&sent); len(parts) != 3 {
		t.Errorf("%d parts after Reset", len(parts))
	}
}

// TestMirrorAgentMoves: an agent moved from one tab to another stays in
// the copy, whichever tab's part comes first.
func TestMirrorAgentMoves(t *testing.T) {
	m, a, b := mirrored()
	var sent Sent
	parts, _ := m.Parts(&sent)
	r := NewMirror(&Core{})
	applyAll(t, r, parts)
	moved := r.C.Tabs[0].PaneList[1].A

	p := a.PaneList[1]
	a.PaneList = slicesDelete(a.PaneList, 1)
	p.A.PartOf = b
	b.PaneList = []*PaneTab{{ID: "q1", Kind: PaneAgent, A: b}, p}
	parts, _ = m.Parts(&sent)
	// b's part first, then a's, which no longer has it.
	for i := len(parts) - 1; i >= 0; i-- {
		if err := r.Apply(parts[i]); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.C.Tabs[1].PaneList[1].A; got != moved || got.PartOf != r.C.Tabs[1] {
		t.Errorf("moved agent: %+v", got)
	}
}

func slicesDelete(s []*PaneTab, i int) []*PaneTab { return append(s[:i:i], s[i+1:]...) }

// TestWireFields: everything of core's that goes to a client can go as
// JSON: a func or a chan must be marked json:"-", or Parts fails.
func TestWireFields(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(path string, ty reflect.Type)
	walk = func(path string, ty reflect.Type) {
		if seen[ty] {
			return
		}
		seen[ty] = true
		switch ty.Kind() {
		case reflect.Func, reflect.Chan, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
			t.Errorf("%s (%s) cannot go as JSON: mark it json:\"-\"", path, ty)
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(path, ty.Elem())
		case reflect.Struct:
			for i := range ty.NumField() {
				f := ty.Field(i)
				if f.IsExported() && f.Tag.Get("json") != "-" {
					walk(path+"."+f.Name, f.Type)
				}
			}
		}
	}
	walk("Core", reflect.TypeFor[Core]())
	walk("Tab", reflect.TypeFor[Tab]())
}
