package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestBarWindow(t *testing.T) {
	ws := []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10} // 109 columns in all
	for _, c := range []struct {
		name              string
		cur, start, avail int
		wantLo, wantHi    int
	}{
		{"all fit", 3, 0, 200, 0, 10},
		{"from the start", 0, 0, 50, 0, 4},
		{"cur past the right edge scrolls just far enough", 5, 0, 50, 3, 6},
		{"cur back left scrolls to it, and fills back to the start", 1, 4, 50, 0, 4},
		{"inside the window it stays", 3, 2, 50, 2, 5},
		{"at the end no right marker", 9, 0, 50, 6, 10},
		{"too narrow still shows cur", 4, 0, 5, 4, 5},
	} {
		lo, hi := barWindow(ws, c.cur, c.start, c.avail)
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("%s: window %d–%d, want %d–%d", c.name, lo, hi, c.wantLo, c.wantHi)
		}
		if c.cur < lo || c.cur >= hi {
			t.Errorf("%s: cur %d not in %d–%d", c.name, c.cur, lo, hi)
		}
	}
}

// manyTabs is a model with ten tabs in one repository, drawn w wide.
func manyTabs(t *testing.T, w int) *Model {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	yes, no := true, false
	cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, Layout: "agent", TabDetails: &no, Mouse: &yes}
	var tabs []state.Tab
	for i := range 10 {
		n := "branch-number-" + strconv.Itoa(i+1)
		tabs = append(tabs, state.Tab{ID: strconv.Itoa(i), Name: n, Agent: "claude", Dir: "/w/" + n,
			Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/" + n, Branch: n}}})
	}
	m := New(cfg, t.TempDir(), nil, state.Session{Tabs: tabs})
	m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
	return m
}

func tabRowText(m *Model) string {
	return strings.Split(ansi.Strip(m.View()), "\n")[m.tabRow()]
}

func TestTabRowScrollsToTheActiveTab(t *testing.T) {
	m := manyTabs(t, 120)
	row := tabRowText(m)
	if !strings.Contains(row, "1:branch-number-1") || strings.Contains(row, "◂") {
		t.Fatalf("first tab active, nothing hidden on the left: %q", row)
	}
	if !strings.Contains(row, "▸") {
		t.Fatalf("tabs hidden on the right are counted: %q", row)
	}

	// Stepping right along the row brings each tab into view.
	for i := 2; i <= 10; i++ {
		m.stepInGroup(1)
		row = tabRowText(m)
		if !strings.Contains(row, strconv.Itoa(i)+":branch-number-"+strconv.Itoa(i)+" ") {
			t.Fatalf("tab %d active but not shown: %q", i, row)
		}
	}
	if !strings.Contains(row, "◂") || strings.Contains(row, "▸") {
		t.Fatalf("at the last tab, the hidden ones are on the left only: %q", row)
	}

	// Back to the middle: hidden on both sides.
	for range 5 {
		m.stepInGroup(-1)
	}
	row = tabRowText(m)
	if !strings.Contains(row, "5:branch-number-5 ") || !strings.Contains(row, "◂") || !strings.Contains(row, "▸") {
		t.Fatalf("tab 5 with tabs hidden on both sides: %q", row)
	}
}

func TestHiddenMarkerShowsTheKeenestLight(t *testing.T) {
	m := manyTabs(t, 120)
	m.tabs[8].status = stBlocked
	m.tabs[6].status = stWorking
	m.View()
	row := m.tabRows[m.curGroup()]
	if row.hidden == 0 {
		t.Fatal("no tabs hidden at 120 columns")
	}
	// The marker's tip names what the light is; a click goes to that tab.
	clickOn(t, m, "▸", tea.MouseButtonLeft)
	if m.active != 8 {
		t.Fatalf("a click on the right marker went to tab %d, want the blocked one, 8", m.active)
	}
	if !strings.Contains(tabRowText(m), "9:branch-number-9 ") {
		t.Fatalf("the tab gone to is in view: %q", tabRowText(m))
	}
}

func TestFullBarOffersToCloseMergedTabs(t *testing.T) {
	m := manyTabs(t, 120)
	m.View()
	if !m.barsFull() {
		t.Fatal("ten tabs at 120 columns: the bar is full")
	}
	if m.openWizard(); m.dialog == nil {
		t.Fatal("no dialog")
	}
	if _, ok := m.dialog.(*wizard); !ok {
		t.Fatalf("without merged tabs the new-tab dialog opens at once, got %T", m.dialog)
	}

	m.dialog = nil
	m.tabs[3].info = tabInfo{wts: []wtInfo{{hasPR: true}}}
	m.tabs[3].info.wts[0].pr.State = "MERGED"
	m.openWizard()
	mu, ok := m.dialog.(*menu)
	if !ok {
		t.Fatalf("with a merged tab and a full bar, the offer comes first, got %T", m.dialog)
	}
	if !strings.Contains(mu.items[0].label, "branch-number-4") {
		t.Fatalf("the offer names the merged tab: %q", mu.items[0].label)
	}
	// Keeping them goes on to the new-tab dialog, without asking again.
	mu.key(m, keyMsg("n"))
	if _, ok := m.dialog.(*wizard); !ok {
		t.Fatalf("n goes on to the new-tab dialog, got %T", m.dialog)
	}
	if m.roomAsked {
		t.Fatal("roomAsked left set")
	}
}

func TestRoomyBarDoesNotAsk(t *testing.T) {
	m := testModel(t)
	m.tabs[0].info = tabInfo{wts: []wtInfo{{hasPR: true}}}
	m.tabs[0].info.wts[0].pr.State = "MERGED"
	m.View()
	if m.barsFull() {
		t.Fatal("three short tabs at 120 columns fill the bar")
	}
	m.openWizard()
	if _, ok := m.dialog.(*wizard); !ok {
		t.Fatalf("got %T, want the new-tab dialog", m.dialog)
	}
}

func TestRepositoriesLeaveRoomForMessages(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	yes, no := true, false
	cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, Layout: "agent", TabDetails: &no, Mouse: &yes}
	var tabs []state.Tab
	for i := range 12 {
		n := "repository-" + strconv.Itoa(i+10)
		tabs = append(tabs, state.Tab{ID: strconv.Itoa(i), Name: "work", Agent: "claude", Dir: "/w/" + n,
			Worktrees: []workspace.Worktree{{Repo: "/r/" + n, Path: "/w/" + n, Branch: "work"}}})
	}
	m := New(cfg, t.TempDir(), nil, state.Session{Tabs: tabs})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.toast("making a-long-branch-name in the background: its tab opens beside yours when it is ready")
	row := strings.Split(ansi.Strip(m.View()), "\n")[0]
	at := strings.Index(row, " making ")
	if at < 0 {
		t.Fatalf("the message is not shown: %q", row)
	}
	if w := ansi.StringWidth(row[:at]); w > 120-msgRoom || w < 120/3 {
		t.Fatalf("the repositories take %d columns, want up to %d: %q", w, 120-msgRoom, row)
	}
	if !strings.Contains(row, "▸") || !strings.Contains(row, "≡ menu") {
		t.Fatalf("hidden repositories counted, menu kept: %q", row)
	}
	if m.groupRow.hidden == 0 {
		t.Fatal("twelve repositories in 70 columns, none hidden")
	}
}
