package app

import (
	"strings"
	"testing"

	"nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// sideNames are the names of the tabs on the left and right, "" for none.
func sideNames(m *Model) (string, string) {
	l, r := m.sides()
	name := func(t *tab) string {
		if t == nil {
			return ""
		}
		return t.Name
	}
	return name(l), name(r)
}

// bodyRow is the first row of the body as drawn, without its styling.
func bodyRow(m *Model) string {
	return strings.Split(ansi.Strip(m.View()), "\n")[m.barRows()+2]
}

func wantSides(t *testing.T, m *Model, left, right, active string) {
	t.Helper()
	l, r := sideNames(m)
	if l != left || r != right || m.cur().Name != active {
		t.Fatalf("sides %q | %q, active %q; want %q | %q, active %q", l, r, m.cur().Name, left, right, active)
	}
}

func TestSplit(t *testing.T) {
	m := testModel(t)
	m.selectTab(1) // logout
	m.runCommand("z v")
	// logout on the right, its neighbour in the group on the left with
	// the keyboard.
	wantSides(t, m, "login", "logout", "login")
	row := bodyRow(m)
	if !strings.Contains(row, "/w/login") || !strings.Contains(row, "/w/logout") {
		t.Fatalf("not side by side: %q", row)
	}
	lw, rw := m.splitWidths()
	if lw+rw+1 != m.mainW() {
		t.Fatalf("widths %d + %d + rule != %d", lw, rw, m.mainW())
	}
	if w, _ := m.agentSize(); w != lw-2 {
		t.Fatalf("active tab is %d wide, want the left's %d", w, lw-2)
	}

	// tab moves the keyboard across; each keeps its side.
	m.command(keyMsg("tab"))
	wantSides(t, m, "login", "logout", "logout")
	if w, _ := m.agentSize(); w != rw-2 {
		t.Fatalf("active tab is %d wide, want the right's %d", w, rw-2)
	}

	// A tab of another repository is shown alone: the split ends.
	m.selectTab(2) // parse, in lib
	wantSides(t, m, "parse", "", "parse")
	if m.split != nil {
		t.Fatal("split kept across repositories")
	}
	m.runCommand("z v") // alone in lib: nothing to put beside it
	wantSides(t, m, "parse", "", "parse")

	m.selectTab(1) // logout
	m.runCommand("z v")
	m.command(keyMsg("tab"))
	wantSides(t, m, "login", "logout", "logout")

	// A click on the left gives it the keyboard.
	m.Update(tea.MouseMsg{X: 2, Y: m.barRows() + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: 2, Y: m.barRows() + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	wantSides(t, m, "login", "logout", "login")

	m.runCommand("z s")
	wantSides(t, m, "logout", "login", "login")

	// The review panel takes the right while it is open.
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if m.splitShown() || !m.reviewOpen {
		t.Fatal("F1 did not put the review panel in the split's place")
	}
	wantSides(t, m, "login", "", "login")
	m.runCommand("z e")
	wantSides(t, m, "logout", "login", "login")

	m.runCommand("z v")
	wantSides(t, m, "login", "", "login")
	if w, _ := m.agentSize(); w != m.mainW()-2 {
		t.Fatalf("one tab is %d wide, want %d", w, m.mainW()-2)
	}
}

func TestSplitNeedsTwoTabs(t *testing.T) {
	m := testModel(t)
	m.tabs = m.tabs[:1]
	m.runCommand("z v")
	if m.splitShown() {
		t.Fatal("split with one tab")
	}
}

func TestSplitCloseAndRestore(t *testing.T) {
	m := testModel(t)
	m.runCommand("z v") // login on the right, logout on the left
	wantSides(t, m, "logout", "login", "logout")

	s, err := state.Load(m.root)
	if err != nil || s.Split != "1" || s.SplitLeft {
		t.Fatalf("saved split %q left %v (%v)", s.Split, s.SplitLeft, err)
	}
	r := New(m.cfg, m.root, nil, s)
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	wantSides(t, r, "logout", "login", "logout")

	// Closing the tab with the keyboard leaves the other one the window.
	m.closeTab(m.cur(), false, false, nil)
	wantSides(t, m, "login", "", "login")
	if m.split != nil {
		t.Fatal("split kept after closing a side")
	}
}

// TestClosingAnotherTabKeepsYouWhereYouAre covers closing a tab that is
// not the one on screen, as the inbox does: the active tab stays active,
// even when the closed one was before it in another group.
func TestClosingAnotherTabKeepsYouWhereYouAre(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	m.selectTab(2)
	m.closeTab(m.tabs[0], false, false, nil)
	if c := m.cur(); c == nil || c.Name != "parse" || m.entered != c {
		t.Fatalf("on %v after closing login from elsewhere", c)
	}
}

// TestSplitOffAloneInRepository covers z v greyed out for a tab with no
// other tab in its repository, however many tabs other repositories have.
func TestSplitOffAloneInRepository(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	m.selectTab(2)
	if m.commandOff("z v") == "" {
		t.Fatal("z v not off for the only tab in lib")
	}
	m.selectTab(0)
	if why := m.commandOff("z v"); why != "" {
		t.Fatalf("z v off with two tabs in app: %s", why)
	}
}

// TestSplitEndedByAnotherRepositoryResizesTheAgent covers going from a
// split to a tab of another repository: its agent, sized for a side of
// the split, gets the whole frame back.
func TestSplitEndedByAnotherRepositoryResizesTheAgent(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	m.selectTab(2)
	parse := runningTab(t, m)
	m.selectTab(1) // logout
	m.runCommand("z v")
	if !m.splitShown() {
		t.Fatal("no split")
	}
	if w, _ := parse.pane.Size(); w >= m.width-2 {
		t.Fatalf("parse is %d wide in the split, want a side's width", w)
	}
	m.selectTab(2)
	want, wantH := m.tabSize(parse)
	if w, h := parse.pane.Size(); w != want || h != wantH || w < m.mainW()-4 {
		t.Fatalf("parse's terminal is %dx%d after the split ended, want %dx%d", w, h, want, wantH)
	}
}
