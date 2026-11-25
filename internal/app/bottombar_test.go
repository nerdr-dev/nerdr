package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// footModel is testModel with the bottom bar on, and login, the active
// tab, with changes, commits not pushed, a pull request and a task.
func footModel(t *testing.T) *Model {
	t.Helper()
	m := testModel(t)
	m.showDetails = true
	m.bottomBar = true
	m.resize()
	tb := m.home().Core.Tabs[0]
	tb.Info = core.TabInfo{At: time.Now(), Wts: []core.WtInfo{{Repo: "/r/app", Branch: "login",
		Add: 12, Del: 3, Uncommitted: 4, Unpushed: 2,
		HasPR: true, Pr: github.Status{Number: 7, State: "OPEN", CI: "pass"}}}}
	tb.Messages = []state.Message{{Text: "later"}}
	m.selectTab(0)
	return m
}

// lastLine is the bottom row of the screen.
func lastLine(m *Model) string {
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	return lines[len(lines)-1]
}

func TestBottomBarShowsTheActiveTab(t *testing.T) {
	m := footModel(t)
	foot := lastLine(m)
	for _, want := range []string{"login", "●4", "⇡2", "+12−3", "#7 open ✓", "✉ 1 task", "★ prompts"} {
		if !strings.Contains(foot, want) {
			t.Fatalf("the bottom bar lacks %q: %q", want, foot)
		}
	}
	top := strings.Join(strings.Split(ansi.Strip(m.View()), "\n")[:m.barRows()], "\n")
	if strings.Contains(top, "+12−3") || strings.Contains(top, "#7") {
		t.Fatalf("the active tab's details are the bottom bar's, not the top's too:\n%s", top)
	}
	if _, h := m.agentArea(); h != m.height-m.barRows()-1 {
		t.Fatalf("the agent should give the bottom bar a row: height %d", h)
	}
	if len(strings.Split(m.View(), "\n")) != m.height {
		t.Fatalf("the screen should be %d rows", m.height)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("l"))
	m.handleKey(keyMsg("g"))
	if m.footRows() != 0 || strings.Contains(lastLine(m), "★ prompts") {
		t.Fatal("the prefix and l g should hide the bottom bar")
	}
	if _, h := m.agentArea(); h != m.height-m.barRows() {
		t.Fatalf("the agent should have the row back: height %d", h)
	}
	if !strings.Contains(ansi.Strip(m.View()), "+12−3") {
		t.Fatal("with the bottom bar hidden, the details are back at the top")
	}
}

func TestBottomBarButtons(t *testing.T) {
	m := footModel(t)
	clickOn(t, m, "✉ 1 task", tea.MouseButtonLeft)
	if !m.panelMessages() || m.saved.on || m.focus != core.FocusReview {
		t.Fatal("a click on the tasks should open F3's tasks")
	}

	m = footModel(t)
	clickOn(t, m, "★ prompts", tea.MouseButtonLeft)
	if !m.panelMessages() || !m.saved.on {
		t.Fatal("a click on the prompts should open F3's saved prompts")
	}

	m = footModel(t)
	clickOn(t, m, "●4", tea.MouseButtonLeft)
	if _, ok := m.dialog.(*commitDialog); !ok {
		t.Fatalf("a click on the files not committed: dialog %T, want the commit dialog", m.dialog)
	}

	m = footModel(t)
	clickOn(t, m, "#7 open", tea.MouseButtonLeft)
	if m.focus != core.FocusReview {
		t.Fatal("a click on the pull request should open it in F2")
	}
}

func TestBottomBarAllTabs(t *testing.T) {
	m := footModel(t)
	m.home().Core.Tabs[1].Info = core.TabInfo{At: time.Now(), Wts: []core.WtInfo{{HasPR: true, Pr: github.Status{Number: 8, State: "OPEN", CI: "fail"}}}}
	m.home().Core.Tabs[2].Info = core.TabInfo{At: time.Now(), Wts: []core.WtInfo{{HasPR: true, Pr: github.Status{Number: 9, State: "MERGED"}}}}
	foot := lastLine(m)
	if !strings.Contains(foot, "✗ 1 CI") || !strings.Contains(foot, "✓ 1 merged") {
		t.Fatalf("the bottom bar should count the failing and the merged: %q", foot)
	}
	clickOn(t, m, "✗ 1 CI", tea.MouseButtonLeft)
	mu, ok := m.dialog.(*menu)
	if !ok || len(mu.items) != 2 || !strings.HasPrefix(mu.items[1].label, "logout") {
		t.Fatalf("a click on the failing CI should list logout: %#v", m.dialog)
	}
	mu.items[1].run(m)
	if m.Cur().Name != "logout" {
		t.Fatalf("picked, the tab is gone to: active %q", m.Cur().Name)
	}
}

// A tab of more worktrees, drawn before git has been asked about them.
func TestBottomBarBeforeGitWithWorktrees(t *testing.T) {
	m := footModel(t)
	tb := m.Cur()
	tb.Info = core.TabInfo{}
	tb.Worktrees = append(tb.Worktrees, workspace.Worktree{Repo: "/r/lib", Path: "/w/login-lib", Branch: "login-lib"})
	if foot := lastLine(m); !strings.Contains(foot, "login +1") || !strings.Contains(foot, "looking…") {
		t.Fatalf("the branch and its worktrees, and looking: %q", foot)
	}
}

// The bottom bar's buttons do nothing through a dialog: a click on ●4
// while a commit message is typed leaves the dialog and its text.
func TestBottomBarUnderADialog(t *testing.T) {
	m := footModel(t)
	m.openCommit(m.Cur())
	d := m.dialog.(*commitDialog)
	d.input.SetValue("half a message")
	clickOn(t, m, "●4", tea.MouseButtonLeft)
	if m.dialog != d || d.input.Value() != "half a message" {
		t.Fatalf("a click on the bottom bar under the commit dialog: dialog %T", m.dialog)
	}
}
