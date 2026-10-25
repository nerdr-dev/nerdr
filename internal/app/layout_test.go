package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/state"
)

func TestLayoutKeys(t *testing.T) {
	m := testModel(t)
	for _, k := range []string{"=", "+", "a", "z", "v", "=", "a"} {
		m.runCommand("z " + k)
		if got := m.currentLayout(); got != k {
			t.Fatalf("after %s the layout is %q", k, got)
		}
		if k == "z" || k == "v" {
			continue // these two still toggle, as before the layouts
		}
		// Pressing it again keeps it: the layout keys set, not toggle.
		m.runCommand("z " + k)
		if got := m.currentLayout(); got != k {
			t.Fatalf("after %s twice the layout is %q", k, got)
		}
	}
	m.runCommand("z +")
	if aw, _ := m.agentArea(); aw < m.mainW()*7/10 {
		t.Fatalf("agent 75%% leaves the agent %d of %d columns", aw, m.mainW())
	}
	m.runCommand("z a")
	if m.split != nil {
		t.Fatal("the agent alone kept the split")
	}
}

func TestLayoutPicker(t *testing.T) {
	m := testModel(t)
	m.runCommand("z l")
	if _, ok := m.dialog.(*layoutPicker); !ok {
		t.Fatalf("z l opened %T", m.dialog)
	}
	view := ansi.Strip(m.View())
	for _, l := range layouts {
		if !strings.Contains(view, l.name) {
			t.Fatalf("the dialog does not show %q:\n%s", l.name, view)
		}
	}
	m.Update(keyMsg("+"))
	if m.dialog != nil || m.currentLayout() != "+" {
		t.Fatalf("+ in the dialog: dialog %T, layout %q", m.dialog, m.currentLayout())
	}
}

// TestLayoutPickerArrows covers trying layouts in the dialog: the arrows
// lay the screen out behind it, enter keeps the one tried and esc goes
// back to the one from before.
func TestLayoutPickerArrows(t *testing.T) {
	m := testModel(t)
	m.runCommand("z =")
	m.runCommand("z l")
	for _, want := range []string{"+", "a", "z", "v", "v"} {
		m.Update(keyMsg("right"))
		m.View()
		if _, ok := m.dialog.(*layoutPicker); !ok {
			t.Fatalf("right closed the dialog (%T)", m.dialog)
		}
		if got := m.currentLayout(); got != want {
			t.Fatalf("right: layout %q, want %q", got, want)
		}
	}
	active := m.cur()
	m.Update(keyMsg("esc"))
	if m.dialog != nil || m.currentLayout() != "=" || m.split != nil || m.focus != focusAgent {
		t.Fatalf("esc: dialog %T, layout %q, split %v, focus %v", m.dialog, m.currentLayout(), m.split, m.focus)
	}
	if m.cur() == active {
		t.Fatal("esc left the keyboard on the tab two agents moved it to")
	}

	m.runCommand("z l")
	m.Update(keyMsg("right"))
	m.Update(keyMsg("enter"))
	if m.dialog != nil || m.currentLayout() != "+" {
		t.Fatalf("enter: dialog %T, layout %q", m.dialog, m.currentLayout())
	}
	m.runCommand("z l")
	m.Update(keyMsg("left"))
	m.Update(keyMsg("left")) // past the first: stays
	m.Update(keyMsg("enter"))
	if m.currentLayout() != "=" {
		t.Fatalf("left twice from +: layout %q", m.currentLayout())
	}
}

func TestLayoutFromConfig(t *testing.T) {
	for setting, want := range map[string]string{"normal": "=", "wide": "+", "agent": "a", "panel": "z"} {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		yes := true
		cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, TabDetails: &yes, Mouse: &yes, Layout: setting}
		m := New(cfg, t.TempDir(), nil, state.Session{})
		if got := m.currentLayout(); got != want {
			t.Errorf("layout = %q: got %q, want %q", setting, got, want)
		}
	}
	yes := true
	cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, TabDetails: &yes, Mouse: &yes, Layout: "small"}
	if m := New(cfg, t.TempDir(), nil, state.Session{}); !m.panelSmall || m.reviewOpen {
		t.Errorf("layout = small: small %v, open %v", m.panelSmall, m.reviewOpen)
	}
}

// The palette finds a command by part of its name and runs it; the hint
// line in a group lists the group's keys.
func TestPaletteAndGroupHint(t *testing.T) {
	m := testModel(t)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg(":"))
	if _, ok := m.dialog.(*palette); !ok {
		t.Fatalf(": opened %T", m.dialog)
	}
	for _, r := range "layouts" {
		m.Update(keyMsg(string(r)))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "ctrl+q z l") {
		t.Fatalf("the palette does not show the keys:\n%s", view)
	}
	m.Update(keyMsg("enter"))
	if _, ok := m.dialog.(*layoutPicker); !ok {
		t.Fatalf("enter on layouts opened %T", m.dialog)
	}
	m.Update(keyMsg("esc"))

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("z"))
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Swap sides") || strings.Contains(view, "New tab") {
		t.Fatalf("the hint in z lists the view group's keys only:\n%s", view)
	}
}

// TestPaletteScrollsAtTheEdges: the palette's list, grouped like the
// menu, moves only when the cursor goes past the rows shown — up at the
// top, down at the bottom — not with every step.
func TestPaletteScrollsAtTheEdges(t *testing.T) {
	m := testModel(t)
	m.openPalette()
	p := m.dialog.(*palette)
	const room = 6
	rows := p.rows(p.matches())
	if rows[0].heading == "" {
		t.Fatal("the first row is its group's heading")
	}
	for range 10 {
		p.cursor++
		p.scroll(rows, room)
	}
	top := p.top
	if top == 0 {
		t.Fatal("going down past the bottom scrolls")
	}
	p.cursor--
	p.scroll(rows, room)
	if p.top != top {
		t.Fatalf("one up inside the window scrolled: top %d, was %d", p.top, top)
	}
	for p.cursor > 0 {
		p.cursor--
		p.scroll(rows, room)
	}
	if p.top != 0 {
		t.Fatalf("back at the first command, its heading shows: top %d", p.top)
	}
}
