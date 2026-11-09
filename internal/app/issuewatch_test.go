package app

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// issueLook is a look at the issues assigned to you as fetchMyIssues makes
// it: entries are those here, others those in repositories not here.
func issueLook(others []string, entries ...core.IssueEntry) core.IssueWatchMsg {
	msg := core.IssueWatchMsg{Entries: entries, Keys: others}
	for _, e := range entries {
		msg.Keys = append(msg.Keys, core.IssueKey(e.Issue.Repo, e.Issue.Number))
	}
	return msg
}

// TestIssueWatchTellsNewlyAssigned covers the watching of issues: the
// first look only learns which are assigned to you; after it one newly
// assigned in a repository here is told, and stays marked until the list
// is opened. One in a repository not here, and a look that failed, are no
// news, and the session keeps what was learned.
func TestIssueWatchTellsNewlyAssigned(t *testing.T) {
	m := tabsModel(t)
	yes := true
	m.home().Core.Cfg.IssueNotify = &yes
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	old := core.IssueEntry{Repo: app, Issue: github.Issue{Number: 3, Title: "Old", Repo: "me/app"}}
	fresh := core.IssueEntry{Repo: app, Issue: github.Issue{Number: 9, Title: "Login fails", Repo: "me/app"}}

	m.home().Core.OnIssueWatch(issueLook([]string{"me/far#1"}, old))
	if len(m.home().Core.IssueWatch.News) != 0 || m.issueBadge() != "" {
		t.Fatalf("the first look should only learn: %+v", m.home().Core.IssueWatch.News)
	}

	m.home().Core.OnIssueWatch(core.IssueWatchMsg{Err: "could not connect"})
	m.home().Core.OnIssueWatch(issueLook([]string{"me/far#1", "me/far#2"}, old, fresh))
	if len(m.home().Core.IssueWatch.News) != 1 || m.home().Core.IssueWatch.News[0].Number != 9 {
		t.Fatalf("news = %+v", m.home().Core.IssueWatch.News)
	}
	if !strings.Contains(m.toastText, "app #9 assigned to you: Login fails") {
		t.Fatalf("toast = %q", m.toastText)
	}
	if !strings.Contains(m.issueBadge(), "1 issue ") {
		t.Fatalf("badge = %q", m.issueBadge())
	}

	m2 := tabsModel(t)
	m2.home().Core.LoadIssueWatch(m.home().Core.SavedIssueWatch())
	if m2.issueBadge() == "" || !m2.home().Core.IssueWatch.Known["me/far#2"] {
		t.Fatal("the session should keep the news and what is known")
	}

	m.showIssues()
	d := m.issuePanel
	if !d.news["me/app#9"] || m.issueBadge() != "" {
		t.Fatalf("the list should show the news and clear the mark: %v", d.news)
	}
}

// TestIssueWatchDropsStaleNews: news of an issue not seen yet goes, the
// mark with it, once the issue is no longer assigned to you or is closed;
// a look that failed keeps it.
func TestIssueWatchDropsStaleNews(t *testing.T) {
	m := tabsModel(t)
	yes := true
	m.home().Core.Cfg.IssueNotify = &yes
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	a := core.IssueEntry{Repo: app, Issue: github.Issue{Number: 3, Title: "A", Repo: "me/app"}}
	b := core.IssueEntry{Repo: app, Issue: github.Issue{Number: 9, Title: "B", Repo: "me/app"}}

	m.home().Core.OnIssueWatch(issueLook(nil))
	m.home().Core.OnIssueWatch(issueLook(nil, a, b))
	m.home().Core.OnIssueWatch(core.IssueWatchMsg{Err: "could not connect"})
	if len(m.home().Core.IssueWatch.News) != 2 {
		t.Fatalf("news = %+v", m.home().Core.IssueWatch.News)
	}
	m.home().Core.OnIssueWatch(issueLook(nil, b))
	if len(m.home().Core.IssueWatch.News) != 1 || m.home().Core.IssueWatch.News[0].Number != 9 {
		t.Fatalf("an issue no longer assigned should take its news away: %+v", m.home().Core.IssueWatch.News)
	}
	m.home().Core.OnIssueWatch(issueLook(nil))
	if m.issueBadge() != "" {
		t.Fatalf("badge = %q", m.issueBadge())
	}
}

// TestIssueWatchOff covers issue_notify = false: GitHub is not asked while
// the list is not shown.
func TestIssueWatchOff(t *testing.T) {
	m := tabsModel(t)
	no := false
	m.home().Core.Cfg.IssueNotify = &no
	if m.home().Core.TickIssues(time.Now()) != nil {
		t.Fatal("issues should not be asked for when not watched or shown")
	}
}

// TestIssueListFilters covers the list's two keys: all open issues instead
// of those assigned to you, and the current tab's repositories only.
func TestIssueListFilters(t *testing.T) {
	m := tabsModel(t, state.Tab{ID: "a", Name: "a", Dir: "/w/a",
		Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/a", Branch: "x"}}})
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	lib := workspace.Repo{Name: "lib", Path: "/r/lib"}
	m.home().Core.IssueWatch.Mine = []core.IssueEntry{{Repo: app, Issue: github.Issue{Number: 1}}, {Repo: lib, Issue: github.Issue{Number: 2}}}
	m.home().Core.IssueWatch.All = map[string]core.RepoIssues{
		"/r/app": {Entries: []core.IssueEntry{{Repo: app, Issue: github.Issue{Number: 1}}, {Repo: app, Issue: github.Issue{Number: 5}}}, At: time.Now()},
	}
	d := &issueList{scope: []workspace.Repo{app, lib}}
	d.fill(m)
	if len(d.entries) != 2 {
		t.Fatalf("assigned to me, everywhere: %d", len(d.entries))
	}
	d.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if !d.here || len(d.entries) != 1 || d.entries[0].Issue.Number != 1 || !strings.Contains(d.title(), "in a") {
		t.Fatalf("assigned to me, in the tab: %+v %q", d.entries, d.title())
	}
	d.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(d.entries) != 2 || d.entries[1].Issue.Number != 5 {
		t.Fatalf("all open, in the tab: %+v", d.entries)
	}
}

// TestPickingAnIssue covers enter on an issue: to the tab working on it,
// or the new-tab steps in its repository with the branch filled in and the
// issue being looked up for the task.
func TestPickingAnIssue(t *testing.T) {
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "a", Dir: "/w/a", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/a", Branch: "main-work"}}},
		state.Tab{ID: "b", Name: "b", Dir: "/w/b", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/b", Branch: "12-login-fails"}}},
	)
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	m.goToIssue(core.IssueEntry{Repo: app, Issue: github.Issue{Number: 12, Title: "Login fails"}})
	if m.dialog != nil || m.Cur().ID != "b" {
		t.Fatalf("an issue with a tab should go to it: dialog %T, tab %s", m.dialog, m.Cur().ID)
	}
	if m.tabOnIssue("/r/app", 1) != nil {
		t.Fatal("issue 1 is not issue 12")
	}

	here := filepath.Join(m.home().Core.Root, "app")
	if out, err := exec.Command("git", "init", "-q", here).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	m.goToIssue(core.IssueEntry{Repo: workspace.Repo{Name: "app", Path: here}, Issue: github.Issue{Number: 40, Title: "Crash on start"}})
	w, ok := m.dialog.(*wizard)
	if !ok || w.step != stepBranch {
		t.Fatalf("should start the new-tab steps at the branch: %#v", m.dialog)
	}
	if got := w.branch.Value(); got != "40-crash-on-start" || !strings.Contains(w.busy, "issue #40") {
		t.Fatalf("branch = %q, busy = %q", got, w.busy)
	}
}
