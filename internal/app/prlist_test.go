package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func tabsModel(t *testing.T, tabs ...state.Tab) *Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // the session saved is not the user's
	no, yes := false, true
	cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, Layout: "agent", TabDetails: &no, Mouse: &yes}
	m := New(cfg, t.TempDir(), nil, state.Session{Tabs: tabs})
	m.width, m.height = 140, 40
	return m
}

// TestPickingAPRGoesWhereItsBranchIs covers the three places a PR's branch
// can be: in a tab (go there), checked out with no tab (offer to open
// it), or nowhere (offer to make a worktree for it).
func TestPickingAPRGoesWhereItsBranchIs(t *testing.T) {
	onlyGit(t)
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "a", Dir: "/w/a", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/a", Branch: "main-work"}}},
		state.Tab{ID: "b", Name: "b", Dir: "/w/b", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/b", Branch: "fix/login"}}},
	)
	repo := workspace.Repo{Name: "app", Path: "/r/app"}

	replies(m, m.goToPR(core.PREntry{Repo: repo, PR: github.PR{Number: 3, Branch: "fix/login"}, Wt: "/w/b"}))
	if m.dialog != nil || m.Cur().ID != "b" {
		t.Fatalf("a PR with a tab should go to it: dialog %T, tab %s", m.dialog, m.Cur().ID)
	}

	replies(m, m.goToPR(core.PREntry{Repo: repo, PR: github.PR{Number: 4, Branch: "feat/x"}, Wt: "/elsewhere/x"}))
	w, ok := m.dialog.(*wizard)
	if !ok || w.step != stepAgent || w.group == nil || w.group.Dir != "/elsewhere/x" ||
		w.group.Worktrees[0].Branch != "feat/x" || w.group.Worktrees[0].Repo != "/r/app" {
		t.Fatalf("a checked-out branch should be offered for opening: %#v", m.dialog)
	}

	replies(m, m.goToPR(core.PREntry{Repo: repo, PR: github.PR{Number: 5, Branch: "feat/y"}}))
	c, ok := m.dialog.(*confirm)
	if !ok || !strings.Contains(strings.Join(c.lines, " "), "No worktree has feat/y") {
		t.Fatalf("a branch nowhere should ask to make a worktree: %#v", m.dialog)
	}
}

// onlyGit leaves git alone on the PATH: no agent is found, so the wizard
// stops at picking one rather than starting it.
func onlyGit(t *testing.T) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	if err := os.Symlink(gitBin, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestPRToItsWorktree: a PR whose branch is checked out in a worktree
// made since GitHub was last asked opens that worktree, as git says now;
// esc there closes the dialog, and does not go on to the repositories.
func TestPRToItsWorktree(t *testing.T) {
	onlyGit(t)
	repo, wt := t.TempDir(), filepath.Join(t.TempDir(), "wt")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "-b", "feat/z", wt},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	m := tabsModel(t)
	replies(m, m.goToPR(core.PREntry{Repo: workspace.Repo{Name: "app", Path: repo}, PR: github.PR{Number: 9, Branch: "feat/z"}}))
	w, ok := m.dialog.(*wizard)
	// Compared as the folders they are: on a Mac the temporary folder is
	// under /var, which git gives as /private/var.
	if !ok || w.group == nil || !sameDir(w.group.Worktrees[0].Path, wt) {
		t.Fatalf("the worktree with the branch should be opened: %#v", m.dialog)
	}
	w.key(m, keyMsg("esc"))
	if m.dialog != nil {
		t.Fatalf("esc should close the dialog, not go to the repositories: step %d", w.step)
	}
}

// TestAgentRequestOpensAMessageForTheTab covers what the review panel asks
// of an agent: a message for the tab working in that worktree, written in
// for the user to read and send, not sent behind their back.
func TestAgentRequestOpensAMessageForTheTab(t *testing.T) {
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "a", Dir: "/w/a", Worktrees: []workspace.Worktree{{Path: "/w/a"}}},
		state.Tab{ID: "b", Name: "b", Dir: "/w/b", Worktrees: []workspace.Worktree{{Path: "/w/b/app"}}},
	)
	m.askAgent(review.AgentMsg{Dir: "/w/b/app", Text: "About `x` in a.go:3: why?"})
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		t.Fatalf("expected the message dialog, got %T", m.dialog)
	}
	if m.Cur().ID != "b" || !d.picked[core.TabRef{Tab: "b"}] || d.input.Value() != "About `x` in a.go:3: why?" {
		t.Fatalf("tab %s picked %v text %q", m.Cur().ID, d.picked, d.input.Value())
	}
	if len(m.Tabs[1].Messages) != 0 {
		t.Fatal("nothing may be queued before the user sends it")
	}
	d.key(m, keyMsg("enter"))
	if q := m.Tabs[1].Messages; len(q) != 1 || !q[0].Auto || !strings.Contains(q[0].Text, "why?") {
		t.Fatalf("enter should queue it for the tab: %v", q)
	}
}

// TestPanelScreenIsKeptPerTab: the F key chosen in one tab is not carried
// to the next; each tab comes back to the screen it was left on.
func TestPanelScreenIsKeptPerTab(t *testing.T) {
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "a", Dir: "/w/a"},
		state.Tab{ID: "b", Name: "b", Dir: "/w/b"},
	)
	m.selectTab(0)
	m.handleKey(keyMsg("f2"))
	m.selectTab(1)
	if m.review.Screen() != (review.Screen{}) {
		t.Fatalf("a tab not yet given a screen should start on F1, got %+v", m.review.Screen())
	}
	m.handleKey(keyMsg("f3"))
	if !m.review.Messages() {
		t.Fatal("F3 should show the messages")
	}
	m.selectTab(0)
	if m.review.Messages() || m.review.Screen() == (review.Screen{}) {
		t.Fatalf("tab a should be back on F2, got %+v", m.review.Screen())
	}
	m.selectTab(1)
	if !m.review.Messages() {
		t.Fatalf("tab b should be back on F3, got %+v", m.review.Screen())
	}
}

// TestPanelScreenSurvivesOpeningAndClosingTabs: a tab opened does not take
// the screen of the tab left, and closing a tab does not write its screen
// on the tab you land on.
func TestPanelScreenSurvivesOpeningAndClosingTabs(t *testing.T) {
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "a", Dir: "/w/a"},
		state.Tab{ID: "b", Name: "b", Dir: "/w/b"},
	)
	m.selectTab(0)
	m.handleKey(keyMsg("f2"))
	f2 := m.review.Screen()

	// Opened as addTab does: active first, then selected.
	c := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "c", Name: "c", Dir: "/w/c"}}}
	m.Tabs = append(m.Tabs, c)
	m.Active = len(m.Tabs) - 1
	m.selectTab(m.Active)
	if m.review.Screen() != (review.Screen{}) {
		t.Fatalf("a new tab should start on F1, got %+v", m.review.Screen())
	}
	m.handleKey(keyMsg("f3"))

	// Closing c lands on b, which was never given a screen.
	m.closeTab(c, false, false, nil)
	if m.Cur().Name != "b" || m.review.Screen() != (review.Screen{}) {
		t.Fatalf("b should be on F1 after c closed, got %s on %+v", m.Cur().Name, m.review.Screen())
	}
	m.selectTab(0)
	if m.review.Screen() != f2 {
		t.Fatalf("a should still be on F2, got %+v", m.review.Screen())
	}
}

// sameDir reports whether a and b are the same folder, links followed.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// TestPRListPointsAtTheTabsPR covers the list opened from a tab: the
// cursor goes to that tab's pull request, also when the list comes later,
// and stays where it is moved to.
func TestPRListPointsAtTheTabsPR(t *testing.T) {
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "a", Dir: "/w/a", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/a", Branch: "fix/login"}}},
	)
	repo := workspace.Repo{Name: "app", Path: "/r/app"}
	entries := []core.PREntry{
		{Repo: repo, PR: github.PR{Number: 1, Branch: "other"}},
		{Repo: repo, PR: github.PR{Number: 2, Branch: "fix/login"}},
		{Repo: repo, PR: github.PR{Number: 3, Branch: "more"}},
	}
	d := &prList{}
	d.pointAt(m.Cur())
	d.apply(entries, nil)
	if d.cursor != 1 {
		t.Fatalf("the cursor should be on the tab's PR once the list comes, is on %d", d.cursor)
	}
	d.key(m, keyMsg("down"))
	d.apply(entries, nil)
	if d.cursor != 2 {
		t.Fatalf("a refresh should leave the cursor where it was moved, is on %d", d.cursor)
	}
	d.pointAt(m.Cur())
	if d.cursor != 1 {
		t.Fatalf("coming back should point at the tab's PR again, is on %d", d.cursor)
	}
}

// TestClosedTabLeavesNothingBehind: what the TUI keeps per tab goes with the
// tab, active or not, and moving the panel off it does not put it back.
func TestClosedTabLeavesNothingBehind(t *testing.T) {
	m := testModel(t)
	for range 2 {
		for i := range m.Tabs {
			m.Active = i
			m.pinReview()
		}
	}
	active, other := m.Tabs[len(m.Tabs)-1], m.Tabs[0]
	m.paneBars = map[*core.Tab]*barRow{active: {}, other: {}}
	_, hasActive := m.panelScreens[active]
	_, hasOther := m.panelScreens[other]
	if !hasActive || !hasOther {
		t.Fatal("no screens kept to begin with")
	}
	for _, closed := range []*core.Tab{active, other} {
		m.closeTab(closed, false, false, nil)
		m.pinReview()
		if _, ok := m.panelScreens[closed]; ok {
			t.Errorf("%s: its screen is kept", closed.Name)
		}
		if _, ok := m.paneBars[closed]; ok {
			t.Errorf("%s: its row is kept", closed.Name)
		}
		if m.screenTab == closed {
			t.Errorf("%s: the panel is still on it", closed.Name)
		}
	}
}

// TestLatePRAnswerIsDropped covers core's answer about a PR picked in F2
// coming after the user has gone on: to another PR, or to a dialog. It
// does nothing, rather than ask about the PR over what they do now.
func TestLatePRAnswerIsDropped(t *testing.T) {
	onlyGit(t)
	m := tabsModel(t,
		state.Tab{ID: "b", Name: "b", Dir: "/w/b", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/b", Branch: "fix/login"}}},
	)
	repo := workspace.Repo{Name: "app", Path: "/r/app"}

	late := m.goToPR(core.PREntry{Repo: repo, PR: github.PR{Number: 5, Branch: "feat/y"}})
	replies(m, m.goToPR(core.PREntry{Repo: repo, PR: github.PR{Number: 3, Branch: "fix/login"}}))
	replies(m, late)
	if m.dialog != nil || m.Cur().ID != "b" {
		t.Fatalf("an answer about a PR picked before another should be dropped: dialog %T", m.dialog)
	}

	late = m.goToPR(core.PREntry{Repo: repo, PR: github.PR{Number: 5, Branch: "feat/y"}})
	other := &confirm{title: "something else"}
	m.dialog = other
	replies(m, late)
	if m.dialog != other {
		t.Fatalf("an answer that comes once a dialog is open should leave it: %T", m.dialog)
	}

	// A fork's PR fetched once the user has gone on: it is said, and the
	// branch kept, rather than the new-tab dialog opened over theirs.
	m.dialog = nil
	m.prPick, m.prPickOn = "fork", nil
	m.dialog = other
	fork := core.PREntry{Repo: repo, PR: github.PR{Number: 9, Branch: "main", Fork: "bob"}}
	m.Update(core.Reply{Ev: core.ForkFetchedMsg{Job: "fork", E: fork}})
	if m.dialog != other || !strings.Contains(m.toastText, "fetched #9") {
		t.Fatalf("a fork fetched late should be told, not opened: %T %q", m.dialog, m.toastText)
	}
}
