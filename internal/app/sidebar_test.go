package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	if m.cur().Name != "logout" || !m.prefix {
		t.Fatalf("on %s, prefix %v: ↓ goes to the next tab and keeps the prefix", m.cur().Name, m.prefix)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.cur().Name != "login" {
		t.Fatalf("on %s: ↑ goes to the tab before", m.cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if !m.onGroups || m.cur().Name != "login" {
		t.Fatalf("← goes to the repositories without going anywhere: on %s", m.cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.cur().Name != "parse" {
		t.Fatalf("on %s: ↓ in the repositories goes to lib", m.cur().Name)
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
	if m.cur().Name != "parse" {
		t.Fatalf("on %s: a click on lib goes there", m.cur().Name)
	}
	clickOn(t, m, "app (2)", tea.MouseButtonLeft)
	clickOn(t, m, "2:logout", tea.MouseButtonLeft)
	if m.cur().Name != "logout" {
		t.Fatalf("on %s: a click on logout goes there", m.cur().Name)
	}
}

func TestSidebarToggle(t *testing.T) {
	m := testModel(t)
	w, _ := m.agentArea()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("z"))
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
	m.handleKey(keyMsg("z"))
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
	if m.cur().Name != "parse" {
		t.Fatalf("on %s: a click on lib's light goes there", m.cur().Name)
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
	for _, tb := range m.tabs {
		tb.info = tabInfo{at: time.Now(), wts: []wtInfo{{add: 120, del: 33}}}
		tb.info.wts[0].stats.Behind = 4
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
	for _, want := range []string{"+ new repo", "+ new worktree"} {
		if !strings.Contains(all, want) {
			t.Fatalf("no %q:\n%s", want, all)
		}
	}
	clickOn(t, m, "+ new repo", tea.MouseButtonLeft)
	if w, ok := m.dialog.(*wizard); !ok || w.step != stepNewRepo {
		t.Fatalf("+ new repo opened %T", m.dialog)
	}
}
