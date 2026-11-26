package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// sideModel is testModel with the tabs on the left.
func sideModel(t *testing.T) *Model {
	m := testModel(t) // login, logout in app; parse in lib
	m.sideBar = true
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

func TestSidebarShowsReposOverTabs(t *testing.T) {
	m := sideModel(t)
	if m.barRows() != 1 {
		t.Fatalf("one row over the sidebar and the body, not %d", m.barRows())
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) != 30 {
		t.Fatalf("%d lines for a window 30 rows tall", len(lines))
	}
	repos, app, login, parse := -1, -1, -1, -1
	for y, l := range lines {
		side := ansi.Strip(ansi.Truncate(l, m.sideW(), ""))
		switch {
		case strings.Contains(side, "repositories"):
			repos = y
		case strings.Contains(side, "╭") && strings.Contains(side, "app"):
			app = y
		case strings.Contains(side, "1:login"):
			login = y
		case strings.Contains(side, "lib (1)"):
			parse = y
		}
	}
	if repos < 0 || parse < 0 || app < 0 || login < 0 || !(repos < parse && parse < app && app < login) {
		t.Fatalf("the repositories over app's tabs (repos %d, lib %d, app %d, login %d):\n%s",
			repos, parse, app, login, strings.Join(lines, "\n"))
	}
	if w, _ := m.agentArea(); w != m.width-m.sideW()-m.railCols() {
		t.Fatalf("the agent has %d columns; the window less the sidebar and the F-keys is %d", w, m.width-m.sideW()-m.railCols())
	}
}

func TestSidebarArrows(t *testing.T) {
	m := sideModel(t)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if m.onGroups {
		t.Fatal("the arrows start on the tabs")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.Cur().Name != "logout" || !m.prefix {
		t.Fatalf("on %s, prefix %v: ↓ goes to the next tab and keeps the prefix", m.Cur().Name, m.prefix)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.Cur().Name != "login" {
		t.Fatalf("on %s: ↑ goes to the tab before", m.Cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if !m.onGroups || m.Cur().Name != "login" {
		t.Fatalf("← goes to the repositories without going anywhere: on %s", m.Cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.Cur().Name != "parse" {
		t.Fatalf("on %s: ↓ in the repositories goes to lib", m.Cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if m.onGroups {
		t.Fatal("→ goes back to the tabs")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.prefix || m.nav {
		t.Fatal("enter stays")
	}
}

func TestSidebarClicks(t *testing.T) {
	m := sideModel(t)
	clickOn(t, m, "lib (1)", tea.MouseButtonLeft)
	if m.Cur().Name != "parse" {
		t.Fatalf("on %s: a click on lib goes there", m.Cur().Name)
	}
	clickOn(t, m, "app (2)", tea.MouseButtonLeft)
	clickOn(t, m, "2:logout", tea.MouseButtonLeft)
	if m.Cur().Name != "logout" {
		t.Fatalf("on %s: a click on logout goes there", m.Cur().Name)
	}
}

func TestSidebarToggle(t *testing.T) {
	m := testModel(t)
	w, _ := m.agentArea()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("l"))
	m.handleKey(keyMsg("t"))
	if !m.sideBar || m.sideW() == 0 {
		t.Fatal("z t puts the tabs on the left")
	}
	if nw, _ := m.agentArea(); nw != w-m.sideW() {
		t.Fatalf("the agent is %d wide with the sidebar, %d without, the sidebar %d", nw, w, m.sideW())
	}
}

func TestSmallSidebar(t *testing.T) {
	m := sideModel(t)
	wide := m.sideW()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("l"))
	m.handleKey(keyMsg("m"))
	if !m.sideSmall || m.sideW() != railW+1 || m.sideW() >= wide {
		t.Fatalf("z m makes the sidebar small: %d columns, %d wide", m.sideW(), wide)
	}
	if w, _ := m.agentArea(); w != m.width-m.sideW()-m.railCols() {
		t.Fatalf("the agent has %d columns, the window less the small sidebar and the F-keys %d", w, m.width-m.sideW()-m.railCols())
	}
	screen := ansi.Strip(m.View())
	if strings.Contains(screen, "login") && strings.Contains(screen, "1:login") {
		t.Fatalf("the small sidebar has no names:\n%s", screen)
	}

	// A click on a light goes there.
	lines := strings.Split(screen, "\n")
	var lights []int
	for y, l := range lines[m.barRows():] {
		y += m.barRows()
		if strings.Contains(ansi.Truncate(l, railW, ""), "○") {
			lights = append(lights, y)
		}
	}
	// lights: app, lib (repositories), login, logout (app's tabs)
	if len(lights) != 4 {
		t.Fatalf("%d lights, not 4:\n%s", len(lights), screen)
	}
	m.Update(tea.MouseMsg{X: 2, Y: lights[1], Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.View()
	if m.Cur().Name != "parse" {
		t.Fatalf("on %s: a click on lib's light goes there", m.Cur().Name)
	}

	// The prefix shows the names over the agent, without resizing it.
	w, _ := m.agentArea()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if peek := ansi.Strip(m.View()); !strings.Contains(peek, "1:parse") {
		t.Fatalf("the prefix shows the wide sidebar:\n%s", peek)
	}
	if nw, _ := m.agentArea(); nw != w {
		t.Fatal("the agent keeps its size while the sidebar is shown wide")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if s := ansi.Strip(m.View()); strings.Contains(s, "1:parse") {
		t.Fatalf("small again after the prefix:\n%s", s)
	}
}

// A tab's details stay on its own line in the list, after its name, so
// they do not read as the name wrapped; the lists end in what makes a new
// one of theirs.
func TestSidebarDetailsOnTheTabsLine(t *testing.T) {
	m := sideModel(t)
	m.showDetails = true
	for _, tb := range m.home().Core.Tabs {
		tb.Info = core.TabInfo{At: time.Now(), Wts: []core.WtInfo{{Add: 120, Del: 33}}}
		tb.Info.Wts[0].Stats.Behind = 4
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	var side []string
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		side = append(side, ansi.Truncate(l, m.sideW(), ""))
	}
	all := strings.Join(side, "\n")
	if !strings.Contains(all, "2:logout ↓4") {
		t.Fatalf("logout's details are not on its line:\n%s", all)
	}
	for _, l := range side {
		if strings.HasPrefix(strings.TrimLeft(strings.Trim(l, "│ "), " "), "↓4") {
			t.Fatalf("details on a line of their own:\n%s", all)
		}
	}
	for _, want := range []string{"+ new repo", "+ new tab"} {
		if !strings.Contains(all, want) {
			t.Fatalf("no %q:\n%s", want, all)
		}
	}
	scanned(m)
	clickOn(t, m, "+ new repo", tea.MouseButtonLeft)
	if w, ok := m.dialog.(*wizard); !ok || w.step != stepNewRepo {
		t.Fatalf("+ new repo opened %T", m.dialog)
	}
}

// TestWalkHintSaysThePrefixIsOn: while the arrows walk the sidebar, the
// big hint gives way, and a box in the corner says the prefix is still on
// until enter leaves it.
func TestWalkHintSaysThePrefixIsOn(t *testing.T) {
	m := sideModel(t)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "ctrl+q is still on") || !strings.Contains(v, "stay in this tab") {
		t.Fatalf("walking, the screen should say the prefix is still on:\n%s", v)
	}
	// The prefix again: every key, the prefix still on; an arrow walks on.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if v := ansi.Strip(m.View()); !m.prefix || !strings.Contains(v, "Keys after ctrl+q") || strings.Contains(v, "is still on") {
		t.Fatalf("the prefix again should bring back every key (prefix %v):\n%s", m.prefix, v)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if v := ansi.Strip(m.View()); !m.prefix || !strings.Contains(v, "is still on") {
		t.Fatalf("an arrow should walk on with the corner box:\n%s", v)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if v := ansi.Strip(m.View()); strings.Contains(v, "is still on") {
		t.Fatalf("after enter the box should be gone:\n%s", v)
	}
}

// TestWalkHintOff: with prefix_hint off there is no box to bring back,
// and the prefix again stays in the tab walked to, as enter does.
func TestWalkHintOff(t *testing.T) {
	m := sideModel(t)
	no := false
	m.home().Core.Cfg.PrefixHint = &no
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if m.prefix || m.nav {
		t.Fatalf("the prefix again, the hint off, should end the walk: prefix %v, nav %v", m.prefix, m.nav)
	}
}

// TestWalkHintTakesClicks: a click on the corner box ends the walk and
// reaches nothing under it.
func TestWalkHintTakesClicks(t *testing.T) {
	m := sideModel(t)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	tab := m.Cur()
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, "stay in this tab"); i >= 0 {
			if m.zoneAt(ansi.StringWidth(line[:i]), y) < 0 {
				t.Fatal("the box should be an area of its own, over what is under it")
			}
		}
	}
	if cmd := clickOn(t, m, "stay in this tab", tea.MouseButtonLeft); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("a click on the box should do nothing under it, got %T", msg)
		}
	}
	if m.prefix || m.nav || m.Cur() != tab || m.dialog != nil {
		t.Fatalf("the click should end the walk in the tab: prefix %v, nav %v, dialog %T", m.prefix, m.nav, m.dialog)
	}
}
