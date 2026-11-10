package fleet

import (
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
)

func tab(id string) *core.Tab {
	return &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: id, Name: id}}}
}

// twoMachines is this one with tabs a and b, and box with c, its copy
// named as the client names it.
func twoMachines() (Fleet, *Machine, *Machine) {
	here := &Machine{Core: core.Core{Tabs: []*core.Tab{tab("a"), tab("b")}}}
	box := &Machine{Name: "box", Core: core.Core{ID: "box", Tabs: []*core.Tab{tab("c")}}}
	return New(here, box), here, box
}

func TestTabsOneMachineAfterAnother(t *testing.T) {
	f, here, box := twoMachines()
	tabs := f.Tabs()
	if len(tabs) != 3 || tabs[0].ID != "a" || tabs[2].ID != "c" {
		t.Fatalf("tabs %v", tabs)
	}
	c := box.Core.Tabs[0]
	if f.TabIndex(c) != 2 || f.Of(c) != box || f.Of(here.Core.Tabs[1]) != here {
		t.Errorf("c at %d, of %v", f.TabIndex(c), f.Of(c))
	}
	if r := f.Ref(c); r != (core.TabRef{Core: "box", Tab: "c"}) || f.ByRef(r) != c {
		t.Errorf("c's ref %+v", r)
	}
	if f.ByRef(core.TabRef{Tab: "c"}) != nil {
		t.Error("c found as this machine's")
	}
	if f.Of(tab("x")) != nil {
		t.Error("a tab of none's has a machine")
	}
}

func TestForGoesByTheRef(t *testing.T) {
	f, here, box := twoMachines()
	for _, c := range []struct {
		r    core.Request
		want *Machine
	}{
		{core.RenameTab{T: core.TabRef{Core: "box", Tab: "c"}}, box},
		{core.RenameTab{T: core.TabRef{Tab: "a"}}, here},
		{core.ClosePane{P: core.PaneRef{Core: "box", Pane: "p"}}, box},
		{core.QuitAll{}, here},
		{core.RenameTab{T: core.TabRef{Core: "gone", Tab: "c"}}, here},
	} {
		if got := f.For(c.r); got != c.want {
			t.Errorf("%T %+v went to %q", c.r, c.r, got.Name)
		}
	}
}

func TestZeroFleetIsOneMachine(t *testing.T) {
	var f Fleet
	f.Lock()
	f.Home().Core.Tabs = []*core.Tab{tab("a")}
	f.Unlock()
	if len(f.Machines()) != 1 || len(f.Tabs()) != 1 {
		t.Fatalf("%d machines, %d tabs", len(f.Machines()), len(f.Tabs()))
	}
}

// TestGroupsByMachine: the tabs are grouped by machine first, a group of
// another machine's named by it, and found again by that name.
func TestGroupsByMachine(t *testing.T) {
	here := &Machine{Core: core.Core{Tabs: []*core.Tab{tab("a"), tab("b")}}}
	box := &Machine{Name: "box", Core: core.Core{ID: "box", Tabs: []*core.Tab{tab("c"), tab("d")}}}
	for _, tb := range []*core.Tab{here.Core.Tabs[0], box.Core.Tabs[0]} {
		tb.Dir = "/w/app"
	}
	here.Core.Tabs[1].Dir, box.Core.Tabs[1].Dir = "/w/zed", "/w/api"
	f := New(here, box)
	if gs := f.Groups(); len(gs) != 4 || gs[0] != "app" || gs[1] != "zed" || gs[2] != "box:api" || gs[3] != "box:app" {
		t.Fatalf("groups %q", gs)
	}
	if g := f.Group(box.Core.Tabs[0]); g != "box:app" {
		t.Fatalf("c is in %q", g)
	}
	if idx := f.GroupTabs("box:app"); len(idx) != 1 || idx[0] != 2 {
		t.Fatalf("box:app holds %v", idx)
	}
	if idx := f.GroupTabs("app"); len(idx) != 1 || idx[0] != 0 {
		t.Fatalf("app holds %v", idx)
	}
	if mc, g := f.GroupOf("box:api"); mc != box || g != "api" {
		t.Fatalf("box:api is %q's %q", mc.Name, g)
	}
	if mc, g := f.GroupOf("api"); mc != here || g != "api" {
		t.Fatalf("api is %q's %q", mc.Name, g)
	}
}
