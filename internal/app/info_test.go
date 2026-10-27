package app

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestTabFollowsItsWorktreesBranch covers a worktree switched to another
// branch after its tab was made: the tab takes the new branch, and its
// name with it unless the user renamed the tab.
func TestTabFollowsItsWorktreesBranch(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "old"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"switch", "-q", "-c", "new"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wts := []workspace.Worktree{{Repo: dir, Path: dir, Branch: "old"}}
	m := tabsModel(t,
		state.Tab{ID: "a", Name: "old", Dir: dir, Worktrees: wts},
		state.Tab{ID: "b", Name: "mine", Dir: dir, Worktrees: append([]workspace.Worktree(nil), wts...)},
	)
	for _, tb := range m.tabs {
		m.setInfo(infoMsg{id: tb.ID, info: lookAt(tb.Worktrees, tb.info, false, false)})
	}
	if a := m.tabs[0]; a.Name != "new" || a.Worktrees[0].Branch != "new" {
		t.Fatalf("tab named after its branch: name %q branch %q", a.Name, a.Worktrees[0].Branch)
	}
	if b := m.tabs[1]; b.Name != "mine" || b.Worktrees[0].Branch != "new" {
		t.Fatalf("renamed tab: name %q branch %q", b.Name, b.Worktrees[0].Branch)
	}
}

// TestLookSkipsGitWhenNothingChanged: a look that may skip runs no git
// while the branch's git files are as they were, and looks again with git
// once a commit changes them; and what lets a look skip.
func TestLookSkipsGitWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	gitIn := func(args ...string) {
		t.Helper()
		args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q", "-b", "main")
	gitIn("commit", "-q", "--allow-empty", "-m", "init")
	wts := []workspace.Worktree{{Repo: dir, Path: dir, Branch: "main"}}
	gitCalls := func() int {
		calls, _ := proc.Counts()
		n := 0
		for c, k := range calls {
			if c.Program == "git" && c.Dir == dir {
				n += k
			}
		}
		return n
	}
	first := lookAt(wts, tabInfo{}, false, true)
	if first.fullAt.IsZero() || first.stamp == "" {
		t.Fatal("the first look should run git, and keep a stamp")
	}
	before := gitCalls()
	again := lookAt(wts, first, false, true)
	if gitCalls() != before || again.fullAt != first.fullAt {
		t.Fatalf("nothing changed, yet %d git calls", gitCalls()-before)
	}
	time.Sleep(10 * time.Millisecond)
	gitIn("commit", "-q", "--allow-empty", "-m", "two")
	before = gitCalls()
	if after := lookAt(wts, again, false, true); gitCalls() == before || after.fullAt == first.fullAt {
		t.Fatal("a commit should have the branch looked at with git")
	}

	m := testModel(t)
	tb := m.cur()
	tb.info = tabInfo{at: time.Now(), fullAt: time.Now()}
	if skip, _ := m.maySkip(tb, time.Now()); !skip {
		t.Fatal("a quiet tab, looked at just now, may skip")
	}
	tb.status = stWorking
	if skip, _ := m.maySkip(tb, time.Now()); skip {
		t.Fatal("a working agent edits files no watch reports")
	}
	tb.status = stIdle
	tb.info.stale = true
	if skip, _ := m.maySkip(tb, time.Now()); skip {
		t.Fatal("a look asked for should run git")
	}
	tb.info.stale = false
	if skip, _ := m.maySkip(tb, time.Now().Add(fullInfoEvery)); skip {
		t.Fatal("git should run after fullInfoEvery anyway")
	}
}

// The lines added and deleted are shown on the active tab only; the rest
// of a tab's details on every tab.
func TestLineCountsOnTheActiveTabOnly(t *testing.T) {
	m := testModel(t)
	m.showDetails = true
	for _, x := range m.tabs[:2] {
		x.info = tabInfo{at: time.Now(), wts: []wtInfo{{repo: "/r/app", add: 12, del: 3}}}
		x.Messages = []state.Message{{Text: "later"}}
	}
	bar := ansi.Strip(m.View())
	if strings.Count(bar, "+12−3") != 1 || strings.Count(bar, "✉1") != 2 {
		t.Fatalf("+12−3 once, on login; ✉1 on both:\n%s", strings.Join(strings.Split(bar, "\n")[:4], "\n"))
	}
}

// p and c are greyed when they have nothing to do: nothing on no remote,
// or every branch with commits already has its open pull request.
func TestPushAndOpenPRWhenThereIsSomethingToDo(t *testing.T) {
	at := time.Now()
	open := github.Status{State: "OPEN"}
	cases := []struct {
		name         string
		wi           wtInfo
		push, openPR bool
	}{
		{"all pushed, no PR", wtInfo{stats: workspace.Stats{Ahead: 2}}, false, true},
		{"unpushed, open PR", wtInfo{stats: workspace.Stats{Ahead: 2}, unpushed: 1, pr: open, hasPR: true}, true, false},
		{"nothing ahead", wtInfo{}, false, false},
		{"merged PR, new commits", wtInfo{stats: workspace.Stats{Ahead: 1}, unpushed: 1, pr: github.Status{State: "MERGED"}, hasPR: true}, true, true},
	}
	for _, c := range cases {
		in := tabInfo{wts: []wtInfo{c.wi}, at: at}
		if in.canPush() != c.push || in.canOpenPR() != c.openPR {
			t.Errorf("%s: push %v, open PR %v; want %v, %v", c.name, in.canPush(), in.canOpenPR(), c.push, c.openPR)
		}
	}
	if in := (tabInfo{}); !in.canPush() || !in.canOpenPR() {
		t.Error("before the branches are looked at, p and c should not be greyed")
	}
}

func TestGitHubEveryScalesWithTheSetting(t *testing.T) {
	m := &Model{}
	if got := m.githubEvery(idlePREvery); got != idlePREvery {
		t.Fatalf("unset, the interval is %v, not %v", got, idlePREvery)
	}
	m.cfg.GitHubRefresh = config.DefaultGitHubRefresh
	if got := m.githubEvery(prEvery); got != prEvery {
		t.Fatalf("at the default, the tab on screen is asked every %v, not %v", got, prEvery)
	}
	m.cfg.GitHubRefresh = 180
	if got := m.githubEvery(prWatchEvery); got != 2*prWatchEvery {
		t.Fatalf("at 180, the watch asks every %v, not %v", got, 2*prWatchEvery)
	}
}

// TestLocalCommitUnmergesCachedPR: a commit on a branch whose PR is
// merged makes the tab not merged at the next look at the branch, before
// GitHub is asked again.
func TestLocalCommitUnmergesCachedPR(t *testing.T) {
	dir := t.TempDir()
	gitIn := func(args ...string) string {
		t.Helper()
		args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn("init", "-q", "-b", "main")
	gitIn("commit", "-q", "--allow-empty", "-m", "init")
	gitIn("update-ref", "refs/remotes/origin/main", "main")
	gitIn("switch", "-q", "-c", "brew")
	gitIn("commit", "-q", "--allow-empty", "-m", "work")
	head := gitIn("rev-parse", "HEAD")
	wts := []workspace.Worktree{{Repo: dir, Path: dir, Branch: "brew"}}
	prev := lookAt(wts, tabInfo{}, false, false)
	prev.wts[0].pr = github.Status{Number: 16, State: "MERGED", HeadOID: head, Base: "main"}
	prev.wts[0].hasPR = true
	if !prev.merged() {
		t.Fatal("the branch at its merged PR's head should be merged")
	}
	if again := lookAt(wts, prev, false, false); !again.merged() {
		t.Fatal("a look with nothing changed took the merged PR away")
	}
	gitIn("commit", "-q", "--allow-empty", "-m", "more work")
	now := lookAt(wts, prev, false, false)
	if now.merged() || !now.wts[0].hasPR || now.wts[0].pr.After != 1 {
		t.Fatalf("after a commit: merged %v, PR %+v %v", now.merged(), now.wts[0].pr, now.wts[0].hasPR)
	}
}
