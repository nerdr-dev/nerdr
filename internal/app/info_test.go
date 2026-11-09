package app

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
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
	for _, tb := range m.Core.Tabs {
		m.Core.SetInfo(core.InfoMsg{Id: tb.ID, Info: core.LookAt(tb.Worktrees, tb.Info, false, false)})
	}
	if a := m.Core.Tabs[0]; a.Name != "new" || a.Worktrees[0].Branch != "new" {
		t.Fatalf("tab named after its branch: name %q branch %q", a.Name, a.Worktrees[0].Branch)
	}
	if b := m.Core.Tabs[1]; b.Name != "mine" || b.Worktrees[0].Branch != "new" {
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
	first := core.LookAt(wts, core.TabInfo{}, false, true)
	if first.FullAt.IsZero() || first.Stamp == "" {
		t.Fatal("the first look should run git, and keep a stamp")
	}
	before := gitCalls()
	again := core.LookAt(wts, first, false, true)
	if gitCalls() != before || again.FullAt != first.FullAt {
		t.Fatalf("nothing changed, yet %d git calls", gitCalls()-before)
	}
	time.Sleep(10 * time.Millisecond)
	gitIn("commit", "-q", "--allow-empty", "-m", "two")
	before = gitCalls()
	if after := core.LookAt(wts, again, false, true); gitCalls() == before || after.FullAt == first.FullAt {
		t.Fatal("a commit should have the branch looked at with git")
	}

	m := testModel(t)
	tb := m.Cur()
	tb.Info = core.TabInfo{At: time.Now(), FullAt: time.Now()}
	if skip, _ := m.Core.MaySkip(tb, time.Now()); !skip {
		t.Fatal("a quiet tab, looked at just now, may skip")
	}
	tb.Light = core.StWorking
	if skip, _ := m.Core.MaySkip(tb, time.Now()); skip {
		t.Fatal("a working agent edits files no watch reports")
	}
	tb.Light = core.StIdle
	tb.Info.Stale = true
	if skip, _ := m.Core.MaySkip(tb, time.Now()); skip {
		t.Fatal("a look asked for should run git")
	}
	tb.Info.Stale = false
	if skip, _ := m.Core.MaySkip(tb, time.Now().Add(core.FullInfoEvery)); skip {
		t.Fatal("git should run after fullInfoEvery anyway")
	}
}

// The lines added and deleted are shown on the active tab only; the rest
// of a tab's details on every tab.
func TestLineCountsOnTheActiveTabOnly(t *testing.T) {
	m := testModel(t)
	m.showDetails = true
	for _, x := range m.Core.Tabs[:2] {
		x.Info = core.TabInfo{At: time.Now(), Wts: []core.WtInfo{{Repo: "/r/app", Add: 12, Del: 3}}}
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
		wi           core.WtInfo
		push, openPR bool
	}{
		{"all pushed, no PR", core.WtInfo{Stats: workspace.Stats{Ahead: 2}}, false, true},
		{"unpushed, open PR", core.WtInfo{Stats: workspace.Stats{Ahead: 2}, Unpushed: 1, Pr: open, HasPR: true}, true, false},
		{"nothing ahead", core.WtInfo{}, false, false},
		{"merged PR, new commits", core.WtInfo{Stats: workspace.Stats{Ahead: 1}, Unpushed: 1, Pr: github.Status{State: "MERGED"}, HasPR: true}, true, true},
	}
	for _, c := range cases {
		in := core.TabInfo{Wts: []core.WtInfo{c.wi}, At: at}
		if in.CanPush() != c.push || in.CanOpenPR() != c.openPR {
			t.Errorf("%s: push %v, open PR %v; want %v, %v", c.name, in.CanPush(), in.CanOpenPR(), c.push, c.openPR)
		}
	}
	if in := (core.TabInfo{}); !in.CanPush() || !in.CanOpenPR() {
		t.Error("before the branches are looked at, p and c should not be greyed")
	}
}

func TestGitHubEveryScalesWithTheSetting(t *testing.T) {
	m := &Model{}
	m.Core.Client = m
	if got := m.Core.GithubEvery(core.IdlePREvery); got != core.IdlePREvery {
		t.Fatalf("unset, the interval is %v, not %v", got, core.IdlePREvery)
	}
	m.Core.Cfg.GitHubRefresh = config.DefaultGitHubRefresh
	if got := m.Core.GithubEvery(core.PrEvery); got != core.PrEvery {
		t.Fatalf("at the default, the tab on screen is asked every %v, not %v", got, core.PrEvery)
	}
	m.Core.Cfg.GitHubRefresh = 180
	if got := m.Core.GithubEvery(core.PrWatchEvery); got != 2*core.PrWatchEvery {
		t.Fatalf("at 180, the watch asks every %v, not %v", got, 2*core.PrWatchEvery)
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
	prev := core.LookAt(wts, core.TabInfo{}, false, false)
	prev.Wts[0].Pr = github.Status{Number: 16, State: "MERGED", HeadOID: head, Base: "main"}
	prev.Wts[0].HasPR = true
	if !prev.Merged() {
		t.Fatal("the branch at its merged PR's head should be merged")
	}
	if again := core.LookAt(wts, prev, false, false); !again.Merged() {
		t.Fatal("a look with nothing changed took the merged PR away")
	}
	gitIn("commit", "-q", "--allow-empty", "-m", "more work")
	now := core.LookAt(wts, prev, false, false)
	if now.Merged() || !now.Wts[0].HasPR || now.Wts[0].Pr.After != 1 {
		t.Fatalf("after a commit: merged %v, PR %+v %v", now.Merged(), now.Wts[0].Pr, now.Wts[0].HasPR)
	}
}
