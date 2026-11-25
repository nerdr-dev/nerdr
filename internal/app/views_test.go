package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// viewPanes are t's view panes of screen i, in the row's order.
func viewPanes(t *core.Tab, i int) []*core.PaneTab {
	var out []*core.PaneTab
	for _, p := range t.Panes() {
		if p.Kind == core.PaneView && p.Screen == i {
			out = append(out, p)
		}
	}
	return out
}

// TestViewPanes: +changes and +plugins open panes of the tab's own, as
// many as you like, side by side with the terminals; each pane's panel is
// its own, what is typed in one not in the other, nor in the dialog.
func TestViewPanes(t *testing.T) {
	m := paneModel(t)
	m.home().Core.Cfg.PaneMinWidth = 20
	tb := m.Cur()
	m.newView(core.ViewChanges)
	m.newView(core.ViewChanges)
	m.newView(core.ViewPlugins)
	changes, plugins := viewPanes(tb, core.ViewChanges), viewPanes(tb, core.ViewPlugins)
	if len(changes) != 2 || len(plugins) != 1 {
		t.Fatalf("two changes panes and one of plugins should be open: %d, %d", len(changes), len(plugins))
	}
	if m.focused(tb) != plugins[0] || m.focus != core.FocusAgent {
		t.Fatal("the pane opened last should have the keyboard")
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"changes 2", "plugins"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q on the screen:\n%s", want, v)
		}
	}

	// What is typed in one changes pane is in its panel alone.
	m.focusPane(tb, changes[0])
	m.Update(keyMsg("/"))
	m.Update(keyMsg("x"))
	m.View()
	one := m.views[changes[0]].st.review.View()
	two := m.views[changes[1]].st.review.View()
	if !strings.Contains(one, "/x") || strings.Contains(two, "/x") || strings.Contains(m.review.View(), "/x") {
		t.Fatal("the filter typed in the first changes pane should be in it alone")
	}

	// esc gives the keyboard back to the agent; t x closes a pane.
	m.Update(keyMsg("esc"))                                  // the filter
	m.Update(viewMsg{p: changes[0], msg: review.LeaveMsg{}}) // as esc's command gives back
	if m.focused(tb) != tb.OwnPane() || m.focus != core.FocusAgent {
		t.Fatal("esc in the pane's panel should give the keyboard to the agent")
	}
	m.focusPane(tb, changes[1])
	m.runCommand("t x")
	if slices.Contains(tb.Panes(), changes[1]) {
		t.Fatal("t x should close the changes pane")
	}
	m.Update(core.FrameMsg{})
	if _, ok := m.views[changes[1]]; ok {
		t.Fatal("a closed pane's panel should go")
	}
}

// TestViewKeys: F5 goes to the tab's plugins pane, opening one when it
// has none; F1 to its changes pane when it has one, else the dialog; t c
// puts the F1 dialog in a pane.
func TestViewKeys(t *testing.T) {
	m := paneModel(t)
	tb := m.Cur()
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if !m.panelDlg || len(viewPanes(tb, core.ViewChanges)) != 0 {
		t.Fatal("F1 with no changes pane should be the dialog")
	}
	m.runCommand("t c")
	changes := viewPanes(tb, core.ViewChanges)
	if m.panelDlg || len(changes) != 1 || m.focused(tb) != changes[0] {
		t.Fatal("t c should put the F1 dialog in a pane, with the keyboard")
	}
	pressPlugins(m)
	plugins := viewPanes(tb, core.ViewPlugins)
	if len(plugins) != 1 || m.focused(tb) != plugins[0] {
		t.Fatal("t p should open the plugins in a pane, with the keyboard")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if m.panelDlg || m.focused(tb) != changes[0] {
		t.Fatal("F1 should go to the changes pane")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if m.focused(tb) != tb.OwnPane() {
		t.Fatal("F1 in the changes pane should go back to the agent")
	}
	pressPlugins(m)
	if len(viewPanes(tb, core.ViewPlugins)) != 1 || m.focused(tb) != plugins[0] {
		t.Fatal("t p should go to the plugins pane there is, not open another")
	}
}

// TestViewPanesStayCurrent: the view panes' panels, drawn again from
// their last drawing, are as they are, through keys and ticks.
func TestViewPanesStayCurrent(t *testing.T) {
	m := paneModel(t)
	m.home().Core.Cfg.PaneMinWidth = 20
	tb := m.Cur()
	m.newView(core.ViewChanges)
	m.newView(core.ViewPlugins)
	m.newView(core.ViewChanges)
	ticks := []tea.Msg{core.FrameMsg{}, core.DetectMsg{}, core.InfoTickMsg{}}
	for _, k := range []string{"down", "/", "a", "esc", "tab", "up", "enter", "esc"} {
		for _, p := range viewPanes(tb, core.ViewChanges) {
			m.focusPane(tb, p)
			m.Update(keyMsg(k))
			m.View()
			for _, tick := range ticks {
				m.Update(tick)
				m.View() // panics if a reused panel is out of date
			}
		}
	}
}

// TestViewStaysOnItsScreen: m in a changes pane does not move its panel
// to another screen; F1 from the pane, with F3's dialog over it, goes
// back to the pane, not to the agent.
func TestViewStaysOnItsScreen(t *testing.T) {
	m := paneModel(t)
	tb := m.Cur()
	m.newView(core.ViewChanges)
	p := viewPanes(tb, core.ViewChanges)[0]
	m.Update(keyMsg("m"))
	if s := m.views[p].st.review.ScreenIndex(); s != core.ViewChanges {
		t.Fatalf("m moved the changes pane to F%d", s+1)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF3})
	if !m.panelDlg {
		t.Fatal("F3 should be the dialog")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if m.panelDlg || m.focused(tb) != p || m.focus != core.FocusAgent {
		t.Fatal("F1 should close the dialog and go to the changes pane")
	}
}

// TestViewToastAndPalette: what a pane's panel says reaches the status
// line; the palette opened from it runs its commands for the window.
func TestViewToastAndPalette(t *testing.T) {
	m := paneModel(t)
	tb := m.Cur()
	m.newView(core.ViewChanges)
	p := viewPanes(tb, core.ViewChanges)[0]
	m.toastText = ""
	m.Update(keyMsg("]")) // one repository: the panel says so
	if m.toastText == "" || m.views[p].st.review.PopToast() != "" {
		t.Fatalf("the pane's word did not reach the status line: %q", m.toastText)
	}
	m.Update(viewMsg{p: p, msg: review.HelpMsg{}})
	if _, ok := m.dialog.(*palette); !ok || m.dialogView != nil {
		t.Fatalf("the palette should be the window's: %T, %v", m.dialog, m.dialogView)
	}
}

// TestPanelDialogHidesThePanesUnder: with the panel's dialog open, a
// click where a pane's button is under it does not reach the button.
func TestPanelDialogHidesThePanesUnder(t *testing.T) {
	m := paneModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyF3})
	m.View()
	if m.panelZones <= m.underZones {
		t.Fatal("the panes under the dialog drew no zones to hide")
	}
	for _, r := range m.zoneRects {
		if r.id >= m.underZones && r.id < m.panelZones {
			if id := m.zoneAt(r.x0, r.y); id >= m.underZones && id < m.panelZones {
				t.Fatalf("zone %d under the dialog is in reach", id)
			}
		}
	}
}

// TestClickInAViewGivesItTheKeyboard: a click on a row of a plugins pane
// without the keyboard picks the row and gives the pane the keyboard, as
// a click on any pane does.
func TestClickInAViewGivesItTheKeyboard(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "first", "cat\n")
	writePlugin(t, plugins, "second", "cat\n")
	m := paneModel(t)
	tb := m.Cur()
	pressPlugins(m)
	p := viewPanes(tb, core.ViewPlugins)[0]
	m.focusPane(tb, tb.OwnPane())
	m.View()
	clickOn(t, m, "second", tea.MouseButtonLeft)
	if m.focused(tb) != p || m.focus != core.FocusAgent {
		t.Fatal("the click should give the plugins pane the keyboard")
	}
	if m.views[p].st.plugins.picked == "" || !strings.HasSuffix(m.views[p].st.plugins.picked, "second") {
		t.Fatalf("the click should pick the row: %q", m.views[p].st.plugins.picked)
	}
}
