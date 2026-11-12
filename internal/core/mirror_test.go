package core

import (
	"bytes"
	"encoding"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
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
	m.Shared = map[string]map[FileKey][]string{"a": {{Repo: "/r/x", File: "go.mod"}: {"b"}}}
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
	// The two tabs, core, and the looks at the skills.
	if len(parts) != 4 {
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
	case !slices.Equal(c.Shared["a"][FileKey{Repo: "/r/x", File: "go.mod"}], []string{"b"}):
		t.Errorf("shared: %v", c.Shared)
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
	rpanes := slices.Clone(ra.PaneList)

	if parts, _ := m.Parts(&sent); len(parts) != 0 {
		t.Errorf("%d parts with nothing changed", len(parts))
	}
	// Gen is not sent: core's part is not sent again for it alone.
	m.Gen++
	if parts, _ := m.Parts(&sent); len(parts) != 0 {
		t.Errorf("%d parts with only Gen changed", len(parts))
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
	// A client knows its panes by pointer: they stay the same panes.
	if !slices.Equal(ra.PaneList, rpanes) || ra.PaneList[1].A != rbeside {
		t.Error("the copy's panes were not kept")
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
	if parts, _ := m.Parts(&sent); len(parts) != 4 {
		t.Errorf("%d parts after Reset", len(parts))
	}
}

// TestMirrorSkills: the looks at the agents' skills go in a part of their
// own, sent again only when a look has changed; and those of a core older
// than that, which sent them with the rest, are taken from there.
func TestMirrorSkills(t *testing.T) {
	m, _, _ := mirrored()
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	m.SkillLooks = map[string]SkillLook{"a": {Key: "a/claude/", At: at, Found: []string{"~/x"}}}
	var sent Sent
	parts, err := m.Parts(&sent)
	if err != nil {
		t.Fatal(err)
	}
	r := NewMirror(&Core{})
	applyAll(t, r, parts)
	if got := r.C.SkillLooks["a"]; got.Key != "a/claude/" || !got.At.Equal(at) || !slices.Equal(got.Found, []string{"~/x"}) {
		t.Fatalf("the copy's looks: %+v", r.C.SkillLooks)
	}
	for _, p := range parts {
		if p.Key == PartCore && bytes.Contains(p.Body, []byte("SkillLooks")) {
			t.Error("the looks went with core's part too")
		}
	}

	// Core's part sent again leaves the copy's looks as they are.
	m.Root = "/elsewhere"
	parts, _ = m.Parts(&sent)
	if len(parts) != 1 || parts[0].Key != PartCore {
		t.Fatalf("parts for a change to core: %d", len(parts))
	}
	applyAll(t, r, parts)
	if len(r.C.SkillLooks) != 1 || r.C.Root != "/elsewhere" {
		t.Errorf("after core's part: %d looks, root %s", len(r.C.SkillLooks), r.C.Root)
	}

	// A newer look is sent, alone; one gone, too.
	m.keepSkillLook("a", SkillLook{Key: "a/claude/", At: at.Add(time.Second)})
	parts, _ = m.Parts(&sent)
	if len(parts) != 1 || parts[0].Key != PartSkills {
		t.Fatalf("parts for a newer look: %d", len(parts))
	}
	applyAll(t, r, parts)
	if got := r.C.SkillLooks["a"]; !got.At.Equal(at.Add(time.Second)) || len(got.Found) != 0 {
		t.Errorf("the newer look: %+v", got)
	}
	m.onSkillsLooked(skillsLooked{looks: map[string]SkillLook{}, all: true})
	parts, _ = m.Parts(&sent)
	applyAll(t, r, parts)
	if len(parts) != 1 || len(r.C.SkillLooks) != 0 {
		t.Errorf("after the looks went: %d parts, %d looks", len(parts), len(r.C.SkillLooks))
	}

	// An older core sent them in its own part.
	old := NewMirror(&Core{})
	if err := old.Apply(Part{Key: PartCore, Body: []byte(`{"Root":"/repos","SkillLooks":{"a":{"Key":"k"}}}`)}); err != nil {
		t.Fatal(err)
	}
	if old.C.SkillLooks["a"].Key != "k" {
		t.Errorf("an older core's looks: %+v", old.C.SkillLooks)
	}
}

// TestMirrorAgentMoves: an agent moved from one tab to another stays in
// the copy, whichever tab's part comes first.
func TestMirrorAgentMoves(t *testing.T) {
	// The parts come as Parts gives them, a's then b's then core's; b's
	// first; or, a client behind and its queue coalesced, core's before
	// b's.
	orders := map[string][]int{"in order": {0, 1, 2}, "b first": {2, 1, 0}, "core before b": {0, 2, 1}}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
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
			if len(parts) != 3 || parts[0].Key != PartTab(a.ID) || parts[1].Key != PartTab(b.ID) || parts[2].Key != PartCore {
				t.Fatalf("parts: %v", keys(parts))
			}
			for _, i := range order {
				if err := r.Apply(parts[i]); err != nil {
					t.Fatal(err)
				}
			}
			if got := r.C.Tabs[1].PaneList[1].A; got != moved || got.PartOf != r.C.Tabs[1] {
				t.Errorf("moved agent: %+v", got)
			}

			// Closed, it is let go of.
			b.PaneList = b.PaneList[:1]
			parts, _ = m.Parts(&sent)
			applyAll(t, r, parts)
			if _, ok := r.agents[moved.ID]; ok {
				t.Error("closed agent still kept")
			}
		})
	}
}

func keys(ps []Part) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
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
		case reflect.Map:
			// A map's keys go as JSON's names: strings, numbers, or text.
			k := ty.Key()
			text := k.Implements(reflect.TypeFor[encoding.TextMarshaler]()) &&
				reflect.PointerTo(k).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
			if k.Kind() != reflect.String && (k.Kind() < reflect.Int || k.Kind() > reflect.Uint64) && !text {
				t.Errorf("%s: a key of %s cannot go as JSON: give it MarshalText and UnmarshalText", path, k)
			}
			walk(path, ty.Elem())
		case reflect.Pointer, reflect.Slice, reflect.Array:
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

// TestMirrorKnowsThePrograms: a copy has no program of core's, but knows
// which run, so a client draws them from the screens it is sent: an
// agent's, a terminal's and a plugin's screen, running or exited; and
// learns when one stops.
func TestMirrorKnowsThePrograms(t *testing.T) {
	m, a, _ := mirrored()
	start := func(argv ...string) *term.Pane {
		p, err := term.Start(argv, t.TempDir(), nil, 40, 10, func() {})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	a.Pane = start("sleep", "30")
	beside := a.PaneList[1].A
	beside.Pane = start("true")
	a.PaneList[3].Shell = start("sleep", "30")
	m.PluginUIs["a\x00/p"].Pane = start("sleep", "30")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if ex, _, _ := beside.Pane.Exited(); ex {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("true never exited")
		}
	}
	a.Light, beside.Light = StWorking, StIdle

	c := &Core{}
	r := NewMirror(c)
	var sent Sent
	parts, err := m.Parts(&sent)
	if err != nil {
		t.Fatal(err)
	}
	applyAll(t, r, parts)
	ca := c.ByRef(m.Ref(a))
	cb := c.ByRef(m.Ref(beside))
	cs := c.ByRef(m.Ref(m.Tabs[1]))
	if ca.Pane != nil || !ca.HasProgram() || !ca.AgentRunning() {
		t.Error("the tab's agent runs")
	}
	if !cb.HasProgram() || cb.AgentRunning() {
		t.Error("the agent beside has exited")
	}
	if cs.HasProgram() {
		t.Error("the other tab's agent has no program")
	}
	shell := ProgramRef{Pane: c.PaneRef(c.Tabs[0].PaneList[3])}
	if c.Program(shell) != nil || !c.Runs(shell) {
		t.Error("the terminal runs, with no program in the copy")
	}
	if !c.Runs(ProgramRef{Plugin: "a\x00/p"}) || !c.PluginUIs["a\x00/p"].Running() {
		t.Error("the plugin's screen runs")
	}
	if c.Runs(ProgramRef{Pane: c.PaneRef(c.Tabs[0].PaneList[2])}) {
		t.Error("the reviewer starting has no program")
	}

	a.Pane = nil
	parts, err = m.Parts(&sent)
	if err != nil {
		t.Fatal(err)
	}
	applyAll(t, r, parts)
	if ca.HasProgram() || ca.AgentRunning() {
		t.Error("the tab's agent has stopped")
	}
}
