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
