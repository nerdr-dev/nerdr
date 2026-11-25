package app

import (
	"testing"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// lookAt makes all the TUI keeps per tab t and its panes, as drawing and
// going to them would.
func lookAt(m *Model, t *core.Tab) {
	m.tl(t)
	for _, p := range t.Panes() {
		m.pl(p)
	}
	for _, p := range t.Panes() {
		if m.views == nil {
			m.views = map[*core.PaneTab]*viewPane{}
		}
		m.views[p] = &viewPane{}
	}
	if m.paneBars == nil {
		m.paneBars = map[*core.Tab]*barRow{}
	}
	m.paneBars[t] = &barRow{}
	if m.tabRows == nil {
		m.tabRows = map[string]*barRow{}
	}
	m.tabRows[m.fleet.Group(t)] = &barRow{}
}

// kept says whether the TUI still keeps anything per tab t or its panes.
func kept(m *Model, t *core.Tab, panes []*core.PaneTab) bool {
	_, look := m.tabLooks[t]
	_, bar := m.paneBars[t]
	for _, p := range panes {
		if _, ok := m.paneLooks[p]; ok {
			return true
		}
		if _, ok := m.views[p]; ok {
			return true
		}
	}
	return look || bar
}

// TestLooksGoWithTheirTabs covers a tab core lets go of by itself (closed
// by another client, or an agent): what the TUI kept for it, and for its
// panes, goes at the next Update, and what it kept for the others stays.
func TestLooksGoWithTheirTabs(t *testing.T) {
	m := testModel(t)
	var gone, stays *core.Tab
	var gonePanes []*core.PaneTab
	locked(m, func() {
		tabs := m.home().Core.Tabs
		if len(tabs) < 2 {
			t.Fatalf("want two tabs, have %d", len(tabs))
		}
		gone, stays = tabs[len(tabs)-1], tabs[0]
		lookAt(m, gone)
		lookAt(m, stays)
		gonePanes = gone.Panes()
		// Gone from core, not by this client: no closeTab here.
		m.home().Core.Tabs = tabs[:len(tabs)-1]
		m.home().Core.Gen++
	})
	m.Update(core.ChangedMsg{})
	locked(m, func() {
		if kept(m, gone, gonePanes) {
			t.Fatal("what the TUI kept for a tab core let go of is kept still")
		}
		if !kept(m, stays, stays.Panes()) {
			t.Fatal("what the TUI keeps for a tab still there went too")
		}
	})
}

// TestLooksGoWithTheirServer covers a server taken out: what the TUI kept
// for its tabs goes with them.
func TestLooksGoWithTheirServer(t *testing.T) {
	m, _, _, _, _ := twoMachines(t)
	if err := config.AddServer(config.Server{Name: "box"}); err != nil {
		t.Fatal(err)
	}
	tb, _ := boxTab(t, m)
	var panes []*core.PaneTab
	locked(m, func() {
		m.cfg().Servers = []config.Server{{Name: "box"}}
		lookAt(m, tb)
		panes = tb.Panes()
		m.removeServer(m.fleet.Named("box"))
		if kept(m, tb, panes) {
			t.Fatal("what the TUI kept for a removed server's tab is kept still")
		}
	})
}
