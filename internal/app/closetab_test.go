package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestCloseWaitsForTheCheck covers the close dialog answered by git within
// closeWait: nothing is drawn until then, keys typed ahead are dropped, and
// then the whole dialog comes at once.
func TestCloseWaitsForTheCheck(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	m.openCloseFor(tb) // the check itself, git and gh, is not run here
	c := m.dialog.(*closeTab)
	if v := ansi.Strip(m.View()); strings.Contains(v, "Close tab") {
		t.Fatalf("drawn before the check or closeWait:\n%s", v)
	}
	for _, k := range []string{"y", "n", "b"} {
		m.Update(keyMsg(k))
		if m.dialog != c || m.tabIndex(tb) < 0 {
			t.Fatalf("%s typed ahead acted on a dialog not yet drawn", k)
		}
	}
	m.Update(closeInfoMsg{t: tb})
	v := ansi.Strip(m.View())
	for _, want := range []string{"Close tab", "branch login", "y delete the worktree",
		"k keep the worktree and the branch", "n cancel, the tab stays open"} {
		if !strings.Contains(v, want) {
			t.Fatalf("%q missing:\n%s", want, v)
		}
	}
	if strings.Contains(v, "checking") || strings.Contains(v, "delete the worktree and") {
		t.Fatalf("checked, and where the branch points not known:\n%s", v)
	}
	m.Update(closeShowMsg{c}) // closeWait passing after the answer changes nothing
	clickOn(t, m, "keep the worktree", tea.MouseButtonLeft)
	if m.dialog != nil || m.tabIndex(tb) >= 0 {
		t.Fatal("n should close the tab")
	}
}

// TestCloseShowsTheCheckWhenSlow covers a check that takes longer than
// closeWait (gh on a slow network): the dialog says it is checking, y still
// waits for it, and then deletes the branch too, merged on GitHub.
func TestCloseShowsTheCheckWhenSlow(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	m.openCloseFor(tb)
	c := m.dialog.(*closeTab)
	m.Update(closeShowMsg{c})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "checking for unsaved work") || strings.Contains(v, "y delete") {
		t.Fatalf("shown while checking:\n%s", v)
	}
	m.Update(keyMsg("y"))
	if m.dialog != c || m.tabIndex(tb) < 0 {
		t.Fatal("y before the check is done could lose work")
	}
	path := tb.Worktrees[0].Path
	m.Update(closeInfoMsg{t: tb, mergedHeads: map[string]string{path: "abc"}, heads: map[string]string{path: "abc"}})
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "y delete the worktree and the branch — merged on GitHub") ||
		!strings.Contains(v, "w delete the worktree, keep the branch") ||
		strings.Index(v, "y delete") > strings.Index(v, "w delete") {
		t.Fatalf("merged:\n%s", v)
	}
}

// TestCloseCancelsWhileWaiting covers esc, and a click, before the dialog is
// drawn, and a closeWait from a dialog already gone.
func TestCloseCancelsWhileWaiting(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	m.openCloseFor(tb)
	old := m.dialog.(*closeTab)
	m.Update(keyMsg("esc"))
	if m.dialog != nil || m.tabIndex(tb) < 0 {
		t.Fatal("esc should cancel and keep the tab")
	}

	m.openCloseFor(tb)
	m.Update(closeShowMsg{old})
	if c := m.dialog.(*closeTab); c.shown {
		t.Fatal("the first dialog's closeWait showed the second")
	}
	m.Update(tea.MouseMsg{X: 5, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.dialog != nil || m.tabIndex(tb) < 0 {
		t.Fatal("a click while waiting should cancel and keep the tab")
	}
}

// TestCloseDeletesAnUnmergedBranch covers y on a branch with no merged pull
// request: offered, and asked again with Y when commits are not pushed;
// enter keeps it.
func TestCloseDeletesAnUnmergedBranch(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	path := tb.Worktrees[0].Path
	m.openCloseFor(tb)
	c := m.dialog.(*closeTab)
	m.Update(closeInfoMsg{t: tb, unpushed: []string{"login"}, heads: map[string]string{path: "abc"}})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "y delete the worktree and the branch — not merged, commits not pushed") {
		t.Fatalf("not merged:\n%s", v)
	}
	if !strings.Contains(v, "keep the branch (enter)") {
		t.Fatalf("enter should keep a branch not merged:\n%s", v)
	}
	m.Update(keyMsg("y"))
	v = ansi.Strip(m.View())
	if m.dialog != c || !strings.Contains(v, "The commits not pushed will be lost") {
		t.Fatalf("y with unpushed commits should ask first:\n%s", v)
	}
	m.Update(keyMsg("n"))
	if m.dialog != c || c.ask != "" || m.tabIndex(tb) < 0 {
		t.Fatal("n should go back to the dialog")
	}
	m.Update(keyMsg("y"))
	m.Update(keyMsg("Y"))
	if m.dialog != nil || m.tabIndex(tb) >= 0 {
		t.Fatal("Y should close the tab")
	}
}

// TestEnterInTheCloseDialog: enter deletes the branch only when its pull
// request is merged with the head it has; otherwise the worktree alone,
// the branch kept.
func TestEnterInTheCloseDialog(t *testing.T) {
	for _, merged := range []bool{false, true} {
		m := testModel(t)
		tb := m.cur()
		path := tb.Worktrees[0].Path
		m.openCloseFor(tb)
		info := closeInfoMsg{t: tb, heads: map[string]string{path: "abc"}, dirty: []string{"login"}}
		if merged {
			info.mergedHeads = map[string]string{path: "abc"}
		}
		m.Update(info)
		c := m.dialog.(*closeTab)
		want := "keep the branch (enter)"
		if merged {
			want = "merged on GitHub (enter)"
		}
		if v := ansi.Strip(m.View()); !strings.Contains(v, want) {
			t.Fatalf("merged %v: no %q:\n%s", merged, want, v)
		}
		// Uncommitted changes make it ask first, which says what enter
		// was taken for.
		m.Update(keyMsg("enter"))
		wantAsk := askWorktree
		if merged {
			wantAsk = askBranch
		}
		if c.ask != wantAsk {
			t.Fatalf("merged %v: enter asked about %q, want %q", merged, c.ask, wantAsk)
		}
	}
}
