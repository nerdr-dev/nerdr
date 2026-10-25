package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nerdr/internal/github"
	"nerdr/internal/state"
	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// prLook is a look at the PRs as fetchPRs makes it: the repositories in
// answered answered with prs, those in failed did not.
func prLook(answered []workspace.Repo, failed []workspace.Repo, prs ...prEntry) prWatchMsg {
	msg := prWatchMsg{prs: prs, answered: map[string]bool{}, failed: map[string]string{},
		notGitHub: map[string]bool{}, skipped: map[string]bool{}}
	for _, r := range answered {
		msg.answered[r.Path] = true
	}
	for _, r := range failed {
		msg.failed[r.Path] = r.Name + ": could not connect"
	}
	return msg
}

func newsOf(m *Model) string {
	var whats []string
	for _, n := range m.prWatch.news {
		whats = append(whats, n.What)
	}
	return strings.Join(whats, "|")
}

// TestPRWatchTellsWhatChanged covers the watching of pull requests: the
// first look only learns how things stand; after it a check coming to
// fail and a review coming in are told, and stay marked until the list is
// opened. Your own reviews, and a repository that did not answer, are no
// news.
func TestPRWatchTellsWhatChanged(t *testing.T) {
	m := tabsModel(t)
	yes := true
	m.cfg.PRNotify = &yes
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	lib := workspace.Repo{Name: "lib", Path: "/r/lib"}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	pr := github.PR{Number: 7, Title: "Fix login", Author: "me", Head: "c1",
		Checks:  []github.Check{{Name: "build", Status: "FAILURE"}, {Name: "test", Status: "IN_PROGRESS"}},
		Reviews: []github.Review{{Author: "ann", State: "COMMENTED", SubmittedAt: t0}},
	}

	m.onPRWatch(prLook([]workspace.Repo{app}, nil, prEntry{repo: app, pr: pr}))
	if len(m.prWatch.news) != 0 || m.prBadge() != "" {
		t.Fatalf("the first look is no news: %+v", m.prWatch.news)
	}

	// The test fails, ann approves, and I answer a comment (a review of
	// my own); lib answers for the first time with a failing PR.
	pr.Checks[1].Status = "FAILURE"
	pr.Reviews = []github.Review{
		{Author: "ann", State: "APPROVED", SubmittedAt: t0.Add(time.Hour)},
		{Author: "bob", State: "CHANGES_REQUESTED", SubmittedAt: t0.Add(time.Hour)},
		{Author: "me", State: "COMMENTED", SubmittedAt: t0.Add(time.Hour)},
	}
	libPR := github.PR{Number: 3, Author: "me", Checks: []github.Check{{Name: "lint", Status: "FAILURE"}}}
	m.onPRWatch(prLook([]workspace.Repo{app, lib}, nil, prEntry{repo: app, pr: pr}, prEntry{repo: lib, pr: libPR}))
	if got, want := newsOf(m), "check failed: test|approved by ann|changes requested by bob"; got != want {
		t.Fatalf("news = %q, want %q", got, want)
	}
	if !strings.Contains(m.toastText, "app #7: check failed: test (and 2 more)") {
		t.Fatalf("toast = %q", m.toastText)
	}
	if !strings.Contains(m.prBadge(), "1 PR ") {
		t.Fatalf("badge = %q", m.prBadge())
	}

	// app does not answer: its PRs are still listed, and what is known of
	// it stands, so its answering again is no news.
	m.onPRWatch(prLook([]workspace.Repo{lib}, []workspace.Repo{app}, prEntry{repo: lib, pr: libPR}))
	if len(m.prWatch.entries) != 2 || len(m.prWatch.errs) != 1 {
		t.Fatalf("entries %d, errs %v", len(m.prWatch.entries), m.prWatch.errs)
	}
	m.onPRWatch(prLook([]workspace.Repo{app, lib}, nil, prEntry{repo: app, pr: pr}, prEntry{repo: lib, pr: libPR}))
	if len(m.prWatch.news) != 3 || len(m.prWatch.errs) != 0 {
		t.Fatalf("nothing changed, yet news %+v, errs %v", m.prWatch.news, m.prWatch.errs)
	}

	// Opening the list shows the news on its PR, and takes the mark away.
	m.showAllPRs()
	// An approval is the review column's ✓, not words before the title.
	if view := strings.Join(m.allPRsBody(130, 30), "\n"); !strings.Contains(view, "◆ check failed: test · changes requested by bob") || strings.Contains(view, "approved by ann") {
		t.Fatalf("list view:\n%s", view)
	}
	if m.prBadge() != "" {
		t.Fatal("the mark should go once the list is opened")
	}
}

// TestPRWatchGonePR: a tab's open PR no longer among yours, merged or
// closed, has the tab asked about at once; a PR still open, or one that
// did not answer, does not.
func TestPRWatchGonePR(t *testing.T) {
	m := testModel(t)
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	pr := github.PR{Number: 7, Author: "me"}
	tb := m.tabs[1]
	tb.info = tabInfo{at: time.Now(), wts: []wtInfo{{repo: "/r/app", hasPR: true, pr: github.Status{Number: 7, State: "OPEN"}}}}
	m.onPRWatch(prLook([]workspace.Repo{app}, nil, prEntry{repo: app, pr: pr}))
	m.onPRWatch(prLook(nil, []workspace.Repo{app}))
	if tb.info.stalePR {
		t.Fatal("still open, or not answered: no look asked for")
	}
	m.onPRWatch(prLook([]workspace.Repo{app}, nil))
	if !tb.info.stalePR || !tb.info.stale {
		t.Fatal("a PR gone from the open ones should have its tab looked at")
	}
}

// TestPRWatchChecks covers which failing checks are news: the same check
// failing again after a push is; one cancelled is not.
func TestPRWatchChecks(t *testing.T) {
	m := tabsModel(t)
	yes := true
	m.cfg.PRNotify = &yes
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	pr := github.PR{Number: 7, Author: "me", Head: "c1", Checks: []github.Check{{Name: "lint", Status: "FAILURE"}}}
	look := func() { m.onPRWatch(prLook([]workspace.Repo{app}, nil, prEntry{repo: app, pr: pr})) }
	look()

	pr.Head = "c2" // pushed, and lint fails again before the next look
	look()
	if got := newsOf(m); got != "check failed: lint" {
		t.Fatalf("news = %q", got)
	}

	m.prWatch.news = nil
	pr.Head, pr.Checks = "c3", []github.Check{{Name: "lint", Status: "CANCELLED"}}
	look()
	if got := newsOf(m); got != "" {
		t.Fatalf("a cancelled check is no news: %q", got)
	}
}

// TestPRWatchDropsStaleNews covers news not seen yet that no longer
// holds: a failing check that passes again, one pushed past, a PR merged
// or closed, and the news of a tab that is closed all go, the mark with
// them; a review stands, and a repository that did not answer keeps its
// news.
func TestPRWatchDropsStaleNews(t *testing.T) {
	m := tabsModel(t, state.Tab{Name: "login", Dir: t.TempDir()}, state.Tab{Name: "other", Dir: t.TempDir()})
	yes := true
	m.cfg.PRNotify = &yes
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	pr := github.PR{Number: 7, Author: "me", Head: "c1", Checks: []github.Check{{Name: "lint", Status: "SUCCESS"}}}
	look := func(prs ...prEntry) { m.onPRWatch(prLook([]workspace.Repo{app}, nil, prs...)) }
	look(prEntry{repo: app, pr: pr})

	pr.Checks[0].Status = "FAILURE"
	look(prEntry{repo: app, pr: pr})
	m.onPRWatch(prLook(nil, []workspace.Repo{app}))
	if got := newsOf(m); got != "check failed: lint" {
		t.Fatalf("news = %q", got)
	}
	pr.Checks[0].Status = "SUCCESS"
	look(prEntry{repo: app, pr: pr})
	if got := newsOf(m); got != "" || m.prBadge() != "" {
		t.Fatalf("a check passing again should take its news away: %q", got)
	}

	pr.Checks[0].Status = "FAILURE"
	look(prEntry{repo: app, pr: pr})
	pr.Head, pr.Checks[0].Status = "c2", "IN_PROGRESS"
	pr.Reviews = []github.Review{{Author: "ann", State: "APPROVED", SubmittedAt: time.Now()}}
	look(prEntry{repo: app, pr: pr})
	if got := newsOf(m); got != "approved by ann" {
		t.Fatalf("a push past a failing check should take its news away, not a review's: %q", got)
	}

	look()
	if got := newsOf(m); got != "" {
		t.Fatalf("a PR no longer open should take its news away: %q", got)
	}

	m.tellPRNews([]state.PRNews{{Key: prKey(app.Path, 8), Repo: "app", Number: 8, What: "approved by ann", Tab: "login"},
		{Key: prKey(app.Path, 9), Repo: "app", Number: 9, What: "approved by ann"}})
	m.closeTab(m.tabs[0], false, false, nil)
	if len(m.prWatch.news) != 1 || m.prWatch.news[0].Number != 9 {
		t.Fatalf("closing a tab should take its news away, and only its: %+v", m.prWatch.news)
	}
}

// TestPRWatchLeavesNonGitHubAlone covers a repository with no GitHub
// remote: it is not asked again for a while, and its error stays listed.
func TestPRWatchLeavesNonGitHubAlone(t *testing.T) {
	m := tabsModel(t)
	local := workspace.Repo{Name: "local", Path: "/r/local"}
	msg := prLook(nil, []workspace.Repo{local})
	msg.notGitHub[local.Path] = true
	m.onPRWatch(msg)
	if until := m.prWatch.skip[local.Path]; time.Until(until) < notGitHubFor-time.Minute {
		t.Fatalf("not left alone: %v", until)
	}
	skipped := prLook(nil, nil)
	skipped.skipped[local.Path] = true
	m.onPRWatch(skipped)
	if !strings.Contains(m.prWatch.errs[local.Path], "local:") {
		t.Fatalf("errs = %v", m.prWatch.errs)
	}
}

// TestPRWatchAcrossRestarts covers news that comes while nerdr is not
// running: how things stood is kept in the session, so the first look
// after a start tells of it; and news not seen is kept too.
func TestPRWatchAcrossRestarts(t *testing.T) {
	m := tabsModel(t)
	yes := true
	m.cfg.PRNotify = &yes
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	pr := github.PR{Number: 7, Author: "me", Head: "c1"}
	m.onPRWatch(prLook([]workspace.Repo{app}, nil, prEntry{repo: app, pr: pr}))

	saved := m.savedPRWatch()
	m2 := tabsModel(t)
	m2.cfg.PRNotify = &yes
	m2.loadPRWatch(saved)
	pr.Reviews = []github.Review{{Author: "ann", State: "APPROVED", SubmittedAt: time.Now()}}
	m2.onPRWatch(prLook([]workspace.Repo{app}, nil, prEntry{repo: app, pr: pr}))
	if got := newsOf(m2); got != "approved by ann" {
		t.Fatalf("news after a restart = %q", got)
	}

	m3 := tabsModel(t)
	m3.loadPRWatch(m2.savedPRWatch())
	if m3.prBadge() == "" {
		t.Fatal("news not seen should be kept across a restart")
	}
}

// TestPRWatchOff covers pr_notify = false: GitHub is not asked while no
// list of the PRs is shown.
func TestPRWatchOff(t *testing.T) {
	m := tabsModel(t)
	no := false
	m.cfg.PRNotify = &no
	if m.tickPRs(time.Now()) != nil {
		t.Fatal("watching while turned off")
	}
}

// TestAllPRsInPanel covers F2's second view: F2 again switches to all your
// PRs and tab back; news marks F2, going there shows all of them with the
// news, and the mark goes. The mark in the bar takes you there too.
func TestAllPRsInPanel(t *testing.T) {
	m := tabsModel(t, state.Tab{ID: "a", Name: "a", Dir: "/w/a"})
	m.reviewOpen = true
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	m.prWatch.at = time.Now() // known already: GitHub is not asked
	m.prWatch.entries = []prEntry{{repo: app, pr: github.PR{Number: 7, Title: "Fix login"}}}

	m.handleKey(keyMsg("f2"))
	if m.review.AllPRs() {
		t.Fatal("F2 first shows the branch's PR")
	}
	m.handleKey(keyMsg("f2"))
	if !m.panelAllPRs() || m.focus != focusReview {
		t.Fatal("F2 again should show all PRs, with the keyboard")
	}
	if view := strings.Join(m.panelLines(120, 30), "\n"); !strings.Contains(view, "#7") || !strings.Contains(view, "my PRs") {
		t.Fatalf("panel:\n%s", view)
	}
	m.handleKey(keyMsg("tab"))
	if m.review.AllPRs() {
		t.Fatal("tab should go back to the branch's PR")
	}

	// News while on F1: F2 is marked, and going there shows it.
	m.handleKey(keyMsg("f1"))
	m.tellPRNews([]state.PRNews{{Key: prKey(app.Path, 7), Repo: "app", Number: 7, What: "approved by ann"}})
	if menu := ansi.Strip(strings.Join(m.railLines(30), "\n")); !strings.Contains(menu, "github◆") {
		t.Fatalf("F2 not marked: %s", menu)
	}
	m.handleKey(keyMsg("f2"))
	if !m.review.AllPRs() {
		t.Fatal("F2 with news should show all PRs")
	}
	if view := strings.Join(m.panelLines(140, 30), "\n"); !strings.Contains(ansi.Strip(view), "◆  ") || strings.Contains(view, "approved by ann") || strings.Contains(strings.Split(view, "\n")[1], "◆") {
		t.Fatalf("panel:\n%s", view)
	}
	if m.prBadge() != "" {
		t.Fatal("news seen in the panel should take the mark away")
	}

	// The mark in the bar opens the same view.
	m.handleKey(keyMsg("f1"))
	m.setFocus(focusAgent)
	m.showAllPRs()
	if !m.panelAllPRs() || m.focus != focusReview {
		t.Fatal("the mark should show all PRs in the panel")
	}
}

// TestActingOnPRsFromPanel covers acting on a PR among all yours: f and a
// write the message for the agent of the tab on its branch into that
// tab's messages, not sent, and leave you where you are; enter goes to
// that tab with the panel on the PR. News names the tab.
func TestActingOnPRsFromPanel(t *testing.T) {
	wt := workspace.Worktree{Repo: "/r/app", Path: "/w/login", Branch: "fix/login"}
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "here", Dir: "/w/here"},
		state.Tab{ID: "b", Name: "login", Dir: "/w/login", Worktrees: []workspace.Worktree{wt}},
	)
	m.active = 0
	m.reviewOpen = true
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	m.prWatch.at = time.Now()
	m.prWatch.entries = []prEntry{
		{repo: app, pr: github.PR{Number: 7, Branch: "fix/login", Title: "Fix login"}},
		{repo: app, pr: github.PR{Number: 9, Branch: "nowhere", Title: "Elsewhere"}},
	}
	m.showAllPRs()
	login := m.tabs[1]

	if m.askAboutPR(true) == nil {
		t.Fatal("f on a PR with a tab should start making the message")
	}
	if m.askAboutPR(true) != nil {
		t.Fatal("a second f while the first is being made should do nothing")
	}
	m.onPRAsk(prAskMsg{t: login, key: prKey(app.Path, 7), what: "fix #7's build", text: "A CI check failed."})
	if len(login.Messages) != 1 || login.Messages[0].Auto {
		t.Fatalf("the message should wait in the tab's F3, not go by itself: %+v", login.Messages)
	}
	if m.cur().Name != "here" || !m.panelAllPRs() {
		t.Fatal("making the message should leave you where you are")
	}
	if !strings.Contains(m.toastText, "login's F3") {
		t.Fatalf("toast = %q", m.toastText)
	}

	m.prPanel.cursor = 1
	if m.askAboutPR(false) != nil || !strings.Contains(m.toastText, "no tab works on nowhere") {
		t.Fatalf("a PR with no tab: toast %q", m.toastText)
	}

	// login was left on F3: enter still shows the PR there, not F3.
	m.selectTab(1)
	m.handleKey(keyMsg("f3"))
	m.selectTab(0)
	m.showAllPRs()
	m.prPanel.cursor = 0
	m.allPRsKey(keyMsg("enter"))
	if view := strings.Join(m.panelLines(120, 30), "\n"); !strings.Contains(view, "this branch") {
		t.Fatalf("enter should show the PR, not the screen the tab was left on:\n%s", view)
	}
	if m.cur().Name != "login" || m.review.AllPRs() || m.focus != focusReview {
		t.Fatalf("enter should go to the tab, on its PR: tab %s, all %v, focus %v", m.cur().Name, m.review.AllPRs(), m.focus)
	}

	m.tellPRNews([]state.PRNews{{Key: prKey(app.Path, 7), Repo: "app", Number: 7, What: "approved by ann", Tab: "login"}})
	if !strings.HasPrefix(m.toastText, "login #7: approved by ann") {
		t.Fatalf("news should name the tab: %q", m.toastText)
	}
}

// TestAskRepo covers which repositories the search lets off asking: those
// on github.com with none of your PRs open, and those with no remote.
func TestAskRepo(t *testing.T) {
	withPRs := map[string]bool{"acme/nerdr": true}
	for _, c := range []struct {
		names     []string
		elsewhere bool
		want      bool
	}{
		{[]string{"acme/nerdr"}, false, true},
		{[]string{"acme/other"}, false, false},
		{[]string{"me/nerdr-fork", "acme/nerdr"}, false, true}, // a fork with upstream as a remote too
		{[]string{"acme/other"}, true, true},                   // also on a host the search does not cover
		{nil, false, false},                                    // no remote: no PRs
	} {
		if got := askRepo(c.names, c.elsewhere, withPRs); got != c.want {
			t.Errorf("askRepo(%v, %v) = %v, want %v", c.names, c.elsewhere, got, c.want)
		}
	}
}

// TestBranchesOnF3: the worktree's PR on F2 has the tab panel's look at
// the branches above it, with its push keys, which work there.
func TestBranchesOnF3(t *testing.T) {
	m := testModel(t)
	m.prWatch.at = time.Now() // known already: GitHub is not asked
	m.handleKey(keyMsg("f2"))
	if !m.panelBranchPR() {
		t.Fatal("F2 should show the branch's PR")
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"c commit", "p push", "P push + open PR"} {
		if !strings.Contains(view, want) {
			t.Fatalf("no %q on F2:\n%s", want, view)
		}
	}
	// The agent's messages are F3's, not F2's.
	for _, gone := range []string{"m message", "w F3 tasks"} {
		if strings.Contains(view, gone) {
			t.Fatalf("%q on F2:\n%s", gone, view)
		}
	}
	m.handleKey(keyMsg("m"))
	if _, ok := m.dialog.(*messageDialog); ok {
		t.Fatal("m on F2 should not open a message")
	}
}

// TestBranchLinesLeaveShownPR: F2 draws the PR of the worktree the panel
// shows below its keys, so the branches above leave it out; the others'
// PRs, and the tab panel, keep theirs.
func TestBranchLinesLeaveShownPR(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	tb.Worktrees = []workspace.Worktree{{Repo: "/r/a", Path: "/w/a", Branch: "x"}, {Repo: "/r/b", Path: "/w/b", Branch: "x"}}
	tb.info = tabInfo{at: time.Now(), wts: []wtInfo{
		{hasPR: true, pr: github.Status{Number: 3, State: "OPEN"}},
		{hasPR: true, pr: github.Status{Number: 4, State: "OPEN"}},
	}}
	p := &tabPanel{t: tb}
	f2 := strings.Join(p.branchLines(m, "/w/a/"), "\n")
	if strings.Contains(f2, "PR #3") || !strings.Contains(f2, "PR #4") {
		t.Errorf("F2 should leave out only the shown worktree's PR:\n%s", f2)
	}
	all := strings.Join(p.branchLines(m, ""), "\n")
	if !strings.Contains(all, "PR #3") || !strings.Contains(all, "PR #4") {
		t.Errorf("the tab panel should show every PR:\n%s", all)
	}
}

// TestIssuesOnF3: F2's third view is the issues, which tab goes on from,
// round to the branch's PR; esc gives the agent the keyboard.
func TestIssuesOnF3(t *testing.T) {
	m := testModel(t)
	m.prWatch.at = time.Now() // known already: GitHub is not asked
	m.issueWatch.at = time.Now()
	m.handleKey(keyMsg("f2"))
	m.handleKey(keyMsg("f2"))
	if !m.review.AllPRs() {
		t.Fatal("F2 again should show all your PRs")
	}
	m.handleKey(keyMsg("tab"))
	if !m.panelIssues() {
		t.Fatal("tab should go on to the issues")
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "issues assigned to me") || !strings.Contains(view, "a all open") {
		t.Fatalf("no issues on F2:\n%s", view)
	}
	m.handleKey(keyMsg("tab"))
	if !m.panelBranchPR() {
		t.Fatal("tab on the issues should go round to the branch's PR")
	}
	m.handleKey(keyMsg("shift+tab"))
	if !m.panelIssues() {
		t.Fatal("shift+tab should go back to the issues")
	}
	m.handleKey(keyMsg("esc"))
	if m.focus != focusAgent {
		t.Fatal("esc should give the agent the keyboard")
	}
}

// TestF3ListsTheReposBranches: a tab on several repositories has a row for
// each branch in F2, the one whose PR is drawn below marked. ] goes on to
// another; ← goes up to the list, where ↑ ↓ pick one, and ↓ past the last
// or enter go back down to the conversation.
func TestF3ListsTheReposBranches(t *testing.T) {
	m := testModel(t)
	m.prWatch.at = time.Now()
	m.issueWatch.at = time.Now()
	tb := m.cur()
	a, b := t.TempDir(), t.TempDir()
	tb.Worktrees = []workspace.Worktree{{Repo: "/r/front", Path: a, Branch: "x"}, {Repo: "/r/back", Path: b, Branch: "y"}}
	tb.info = tabInfo{at: time.Now(), wts: []wtInfo{
		{hasPR: true, pr: github.Status{Number: 3, State: "OPEN"}},
		{},
	}}
	m.pinReview()
	m.handleKey(keyMsg("f2"))
	if !m.panelBranchPR() {
		t.Fatal("F2 should show the branch's PR")
	}
	p := m.curBranchPanel()
	list := ansi.Strip(strings.Join(p.branchList(m, a), "\n"))
	if !strings.Contains(list, "▸ front") || !strings.Contains(list, "#3 open") || !strings.Contains(list, "  back") {
		t.Fatalf("a row for each branch, the shown one marked:\n%s", list)
	}
	if m.review.PinIdx() != 0 {
		t.Fatalf("shows %d, not the first", m.review.PinIdx())
	}
	m.handleKey(keyMsg("]"))
	if m.review.PinIdx() != 1 {
		t.Fatal("] should show the next repository's PR")
	}
	m.handleKey(keyMsg("["))
	m.handleKey(keyMsg("left"))
	if !m.review.OnRepoList() {
		t.Fatal("← should go up to the list of repositories")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "↑↓ repository · enter comments") {
		t.Fatalf("no hint for the list:\n%s", v)
	}
	m.handleKey(keyMsg("down"))
	if m.review.PinIdx() != 1 || !m.review.OnRepoList() {
		t.Fatal("↓ on the list should show the next repository's PR")
	}
	m.handleKey(keyMsg("up"))
	if m.review.PinIdx() != 0 {
		t.Fatal("↑ on the list should show the previous repository's PR")
	}
	m.handleKey(keyMsg("down"))
	m.handleKey(keyMsg("down"))
	if m.review.PinIdx() != 1 || m.review.OnRepoList() {
		t.Fatal("↓ from the last repository should go down to the conversation")
	}
	m.handleKey(keyMsg("up"))
	if !m.review.OnRepoList() {
		t.Fatal("↑ from the first block should go back up to the list")
	}
	m.handleKey(keyMsg("enter"))
	if m.review.OnRepoList() || m.review.PinIdx() != 1 {
		t.Fatal("enter on the list should go down to the shown repository's conversation")
	}
}

// TestFilesBesideWidensThePanel: F1's files put beside the diff in a
// narrow panel make it full width, and put back above, narrow again; in a
// panel wide enough they leave it be.
func TestFilesBesideWidensThePanel(t *testing.T) {
	m := testModel(t)
	m.onPanelLayout(true)
	if !m.reviewZoom {
		t.Fatal("beside, in a narrow panel, should make it full width")
	}
	m.onPanelLayout(false)
	if m.reviewZoom {
		t.Fatal("back above should give the agent its room back")
	}
	m.run("z z", keyMsg("z"))
	m.onPanelLayout(true)
	m.onPanelLayout(false)
	if !m.reviewZoom {
		t.Fatal("a panel made full width by hand should stay so")
	}
}

// TestSmallPanel: the panel's screens are a column on the right while it
// is small or closed, and a row over it while it is kept open; made small,
// an F-key opens it, the keyboard back with the agent folds it again, and
// « keeps it open.
func TestSmallPanel(t *testing.T) {
	m := testModel(t)
	m.reviewOpen = true
	m.togglePanelSmall()
	if m.reviewOpen || !m.railShown() {
		t.Fatal("» should fold the panel to its F-keys")
	}
	view := ansi.Strip(m.View())
	for _, f := range []string{"F1 changes", "F2 github", "F3 tasks", "F4 skills", "«"} {
		if !strings.Contains(view, f) {
			t.Fatalf("no %s on the small panel:\n%s", f, view)
		}
	}
	if aw, _ := m.agentArea(); aw != m.bodyW()-m.panelRailW()-1 {
		t.Errorf("the agent should have all but the column: %d of %d", aw, m.bodyW())
	}
	m.handleKey(keyMsg("f2"))
	if !m.reviewOpen || m.focus != focusReview {
		t.Fatal("an F-key should open the panel")
	}
	m.run("a a", keyMsg("a"))
	if m.reviewOpen || !m.railShown() {
		t.Fatal("back to the agent should fold it again")
	}
	m.togglePanelSmall()
	if !m.reviewOpen || m.panelSmall {
		t.Fatal("« should keep the panel open")
	}
	m.setFocus(focusAgent)
	if !m.reviewOpen {
		t.Fatal("a panel kept open stays open")
	}
	if m.railShown() || m.mainW() != m.bodyW() {
		t.Fatal("a panel kept open has its menu as a row, not the column")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "F3 tasks") || !strings.Contains(view, "F2") {
		t.Fatalf("the menu row should be over the panel, the column gone:\n%s", view)
	}
	clickOn(t, m, "F2", tea.MouseButtonLeft)
	if m.review.ScreenIndex() != 1 {
		t.Fatalf("a click on F2 in the menu row should show it, got F%d", m.review.ScreenIndex()+1)
	}
}

// TestEveryonesPRsInPanel covers m among all your PRs: everyone's open
// PRs in the repositories, those your review is asked for marked and
// first, with who opened each; m again goes back to yours. A fork's PR
// shows the fork's owner, and enter offers to fetch it rather than make a
// branch of its name.
func TestEveryonesPRsInPanel(t *testing.T) {
	m := tabsModel(t, state.Tab{ID: "a", Name: "a", Dir: "/w/a"})
	m.reviewOpen = true
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	m.prWatch.at = time.Now()
	m.prWatch.entries = []prEntry{{repo: app, pr: github.PR{Number: 7, Title: "Mine", Author: "me"}}}
	now := time.Now()
	m.prAll.repos = map[string]repoPRs{app.Path: {at: now, names: []string{"acme/app"}, entries: []prEntry{
		{repo: app, pr: github.PR{Number: 7, Title: "Mine", Author: "me", Branch: "mine", UpdatedAt: now}},
		{repo: app, pr: github.PR{Number: 8, Title: "Theirs", Author: "ann", Branch: "theirs", UpdatedAt: now.Add(-time.Hour)}},
		{repo: app, pr: github.PR{Number: 9, Title: "Forked", Author: "bob", Branch: "main", Fork: "bob", UpdatedAt: now.Add(-2 * time.Hour)}},
	}}}
	m.prAll.asked = map[string]bool{github.RequestKey("Acme/App", 9): true}

	m.handleKey(keyMsg("f2"))
	m.handleKey(keyMsg("f2"))
	if !m.panelAllPRs() {
		t.Fatal("F2 again should show your PRs")
	}
	m.handleKey(keyMsg("m"))
	m.prAll.busy = false // as if GitHub had answered
	m.applyPRs()
	d := m.prPanel
	if !d.all || len(d.entries) != 3 || d.entries[0].pr.Number != 9 || !d.entries[0].asked {
		t.Fatalf("everyone's, the one asking your review first: %+v", d.entries)
	}
	view := ansi.Strip(strings.Join(m.panelLines(160, 30), "\n"))
	for _, want := range []string{"all open pull requests (3, 1 asking your review)", "● review asked", "bob:main", "ann", "#8"} {
		if !strings.Contains(view, want) {
			t.Fatalf("no %q in:\n%s", want, view)
		}
	}

	d.cursor = 0
	m.handleKey(keyMsg("enter"))
	c, ok := m.dialog.(*confirm)
	if !ok || !strings.Contains(strings.Join(c.lines, " "), "fork") || !strings.Contains(strings.Join(c.lines, " "), "pr-9-main") {
		t.Fatalf("enter on a fork's PR should offer to fetch it: %#v", m.dialog)
	}
	m.dialog = nil

	m.handleKey(keyMsg("m"))
	if d.all || len(d.entries) != 1 || d.entries[0].pr.Number != 7 {
		t.Fatalf("m again should show only yours: %+v", d.entries)
	}
}

// TestPRListSaysWhatItIs covers the line above the list: whose pull
// requests, and in how many repositories under which folders — with
// everyone's, many more than one works in.
func TestPRListSaysWhatItIs(t *testing.T) {
	m := testModel(t)
	home, _ := os.UserHomeDir()
	m.root = filepath.Join(home, "develop")
	m.cfg.RepoDirs = []string{"/srv/repos"}
	m.prWatch.repos = map[string]bool{"/a": true}
	m.prAll.repos = map[string]repoPRs{"/a": {}, "/b": {}, "/c": {}}
	if got := ansi.Strip(m.prScope(false)); got != "Your open pull requests · in 1 repository under ~/develop, /srv/repos" {
		t.Fatalf("mine: %q", got)
	}
	if got := ansi.Strip(m.prScope(true)); got != "Everyone's open pull requests · in 3 repositories under ~/develop, /srv/repos" {
		t.Fatalf("all: %q", got)
	}
	m.showAllPRs()
	if view := ansi.Strip(strings.Join(m.allPRsBody(130, 30), "\n")); !strings.Contains(view, "Your open pull requests") {
		t.Fatalf("the list says what it is:\n%s", view)
	}
}
