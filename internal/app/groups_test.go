package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/nerdr-dev/nerdr/internal/term"
)

func TestWalkingTabsKeepsThePrefix(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	right := tea.KeyMsg{Type: tea.KeyRight}

	// The arrows start on the tab row: ← → walk app's tabs, and stay in app.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if m.onGroups {
		t.Fatal("the arrows start on the tab row")
	}
	m.handleKey(right)
	if m.cur().Name != "logout" || !m.prefix {
		t.Fatalf("on %s, prefix %v: → goes to the next tab and keeps the prefix", m.cur().Name, m.prefix)
	}
	m.handleKey(right)
	if m.cur().Name != "login" {
		t.Fatalf("on %s: → wraps round within the group", m.cur().Name)
	}

	// ↑ to the group row: → goes to the next repository.
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m.handleKey(right)
	if !m.onGroups || m.cur().Name != "parse" {
		t.Fatalf("on %s: → on the group row goes to lib", m.cur().Name)
	}
	m.handleKey(right)
	if m.cur().Name != "login" {
		t.Fatalf("on %s: → wraps round the groups", m.cur().Name)
	}

	// ↓ back to the tab row, without going anywhere.
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.onGroups || m.cur().Name != "login" {
		t.Fatalf("↓ moves to the tab row without going anywhere: on %s", m.cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if m.cur().Name != "logout" {
		t.Fatalf("on %s: ← goes to the previous tab", m.cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.prefix || m.nav || m.cur().Name != "logout" {
		t.Fatalf("enter stays: on %s, prefix %v", m.cur().Name, m.prefix)
	}

	// A tab alone in its repository: the arrows go to the next repository
	// at once, and from the tab row too.
	m.selectTab(2) // parse
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !m.onGroups {
		t.Fatal("a lone tab starts on the group row")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m.handleKey(right)
	if m.cur().group() == "lib" || !m.onGroups {
		t.Fatalf("on %s: ← → from a lone tab go to the next repository", m.cur().Name)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.selectTab(0)

	// A command after walking runs for the tab walked to: x asks to close
	// it, without enter and the prefix again first.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	m.handleKey(keyMsg("x"))
	if m.prefix || m.nav || m.dialog == nil {
		t.Fatalf("x after walking closes the tab: prefix %v, %T", m.prefix, m.dialog)
	}
	m.dialog = nil
	m.selectTab(0)

	// So does a command in a group: i opens the group, i again the tab's
	// branches — for the tab walked to.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	walked := m.cur()
	m.handleKey(keyMsg("i"))
	if !m.prefix || m.keyGroup != "i" || m.nav {
		t.Fatalf("i after walking opens the group: prefix %v, group %q, nav %v", m.prefix, m.keyGroup, m.nav)
	}
	m.handleKey(keyMsg("i"))
	if m.cur() != walked || !m.panelBranchPR() || m.focus != focusReview || m.prefix {
		t.Fatalf("i i after walking: F2 %v, focus %v, prefix %v", m.panelBranchPR(), m.focus, m.prefix)
	}
	m.dialog = nil
	m.selectTab(0)

	// esc in a group goes back to the prefix's own keys; a key the group
	// does not have ends it, and is not typed to the agent.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("z"))
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.prefix || m.keyGroup != "" {
		t.Fatalf("esc in a group: prefix %v, group %q", m.prefix, m.keyGroup)
	}
	m.handleKey(keyMsg("a"))
	m.handleKey(keyMsg("y"))
	if m.prefix || m.keyGroup != "" || m.dialog != nil {
		t.Fatalf("a key the group lacks: prefix %v, group %q, %T", m.prefix, m.keyGroup, m.dialog)
	}

	// Any other key ends the walk and goes to the agent.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	m.handleKey(keyMsg("y"))
	if m.prefix || m.nav || m.dialog != nil {
		t.Fatalf("typing ends the walk: prefix %v, %T", m.prefix, m.dialog)
	}

	// The wheel over the tab row goes across groups too.
	m.selectTab(1) // logout
	m.Update(tea.MouseMsg{X: 5, Y: m.tabRow(), Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cur().Name != "parse" {
		t.Fatalf("on %s", m.cur().Name)
	}
}

// TestRepositoryRow covers the repository row: there with two
// repositories, gone with one, and put away and back with the prefix and z b.
func TestRepositoryRow(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	bars := func() []string { return strings.Split(ansi.Strip(m.View()), "\n")[:2] }
	if m.barRows() != 2 || !strings.Contains(bars()[0], "lib") || !strings.Contains(bars()[1], "login") {
		t.Fatalf("two repositories, two rows: %q", bars())
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("z"))
	m.handleKey(keyMsg("b"))
	row := bars()[0]
	if m.barRows() != 1 || !strings.HasPrefix(row, " app › ") || !strings.Contains(row, "login") || !strings.Contains(row, "? help") {
		t.Fatalf("put away: the tab row names the repository and has the messages: %q", row)
	}
	if w, h := m.agentArea(); h != m.height-1 || w != m.width-m.railCols() {
		t.Fatalf("the agent has the row: %dx%d", w, h)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("z"))
	m.handleKey(keyMsg("b"))
	if m.barRows() != 2 {
		t.Fatal("b again brings the row back")
	}

	// With one repository there is no row to show, nor a name to give.
	m.tabs = m.tabs[:2]
	row = bars()[0]
	if m.barRows() != 1 || !strings.HasPrefix(row, " ○ 1:login") || !strings.Contains(row, "? help") {
		t.Fatalf("one repository, one row: %q", row)
	}
}

// TestRepositoryRowBackground covers the repository row's own, darker
// background, which keeps it apart from the tab row under it.
func TestRepositoryRowBackground(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(was) })
	m := testModel(t) // login, logout in app; parse in lib
	rows := strings.SplitN(m.View(), "\n", 3)
	repo, tabs := "48;5;"+groupBarBg, "48;5;"+string(tabBarBg)
	if strings.Contains(rows[0], tabs) || !strings.Contains(rows[0], repo) {
		t.Fatalf("the repository row should be on its own background: %q", rows[0])
	}
	if !strings.Contains(rows[0], "48;5;"+string(groupActiveBg)) || !strings.Contains(rows[1], "48;5;"+string(tabActiveBg)) {
		t.Fatalf("the active repository and tab should be blue: %q", rows[:2])
	}
	if strings.Contains(rows[1], repo) {
		t.Fatalf("the tab row should keep the bars' background: %q", rows[1])
	}
}

func TestRepoListCountsTheLights(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	m.tabs[0].status, m.tabs[1].status = stDone, stWorking
	m.tabs[2].status = stIdle
	out := ansi.Strip(strings.Join(m.repoList(40, 8, false), "\n"))
	if !strings.Contains(out, "app (2 1"+lightGlyph(stWorking)+")") {
		t.Fatalf("a done tab's light hides one working, so the count shows it:\n%s", out)
	}
	if !strings.Contains(out, "lib (1)") {
		t.Fatalf("an idle group has only its count:\n%s", out)
	}
}

func TestPassingATabLeavesItDone(t *testing.T) {
	m := testModel(t) // login, logout in app; parse in lib
	m.tabs[1].status = stDone
	m.selectTab(1)
	m.selectTab(2)
	m.look()
	if m.tabs[1].status != stDone {
		t.Fatal("a tab only passed on the way to another is not seen")
	}
	if m.dwelt() {
		t.Fatal("the tab come to is not looked at before seenAfter")
	}

	m.selectTab(1)
	m.tabs[1].queuePaused = true
	m.landed = m.landed.Add(-seenAfter)
	m.blurred = true
	m.look()
	if m.tabs[1].status != stDone {
		t.Fatal("a tab is not seen while you are in another window")
	}
	m.blurred = false
	m.editor = &term.Pane{}
	m.look()
	if m.tabs[1].status != stDone {
		t.Fatal("a tab is not seen behind an editor")
	}
	m.editor = nil
	m.look()
	if m.tabs[1].status != stIdle || m.tabs[1].queuePaused {
		t.Fatalf("a tab stayed on is seen, its queue let go: %s, paused %v", m.tabs[1].status.label(), m.tabs[1].queuePaused)
	}
}
