package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// TestSandboxDecisions: who a report is taken from, with no client running.
func TestSandboxDecisions(t *testing.T) {
	p, err := term.Start([]string{"sleep", "30"}, t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	start := p.StartTime()
	outside := &Agent{Pane: p, PidStart: start}
	inside := &Agent{Pane: p, InSandbox: true, PidStart: start}
	const stray = 1 << 30
	under := hooks.Peer{Known: true, Chain: []int{stray, p.Pid(), 1}, Starts: []uint64{0, start, hooks.Started(1)}}
	stranger := hooks.Peer{Known: true, Chain: []int{stray, 1}}

	for _, c := range []struct {
		name               string
		a                  *Agent
		peer               hooks.Peer
		unvouched, refused bool
	}{
		{"under the agent", outside, under, false, false},
		{"a stranger with its secret", outside, stranger, true, true},
		{"no agent has the secret", nil, under, true, true},
		{"an agent with no program", &Agent{}, under, true, true},
		{"a stranger with a sandboxed agent's secret", inside, stranger, true, false},
		{"a system that cannot say", outside, hooks.Peer{}, false, false},
		{"an agent whose start is not known", &Agent{Pane: p}, under, true, true},
	} {
		r := Vouch(hooks.Report{Peer: c.peer}, c.a)
		if r.Unvouched != c.unvouched {
			t.Errorf("%s: Unvouched = %v", c.name, r.Unvouched)
		}
		if refused := Refusal(r, c.a) != ""; refused != c.refused {
			t.Errorf("%s: refused = %v", c.name, refused)
		}
	}

	for _, c := range []struct {
		name string
		r    hooks.Report
		a    *Agent
		kind bool
		want bool
	}{
		{"outside", hooks.Report{}, outside, false, false},
		{"started in a sandbox", hooks.Report{}, inside, false, true},
		{"its kind now in one", hooks.Report{}, outside, true, true},
		{"from a process it cannot place", hooks.Report{Unvouched: true}, outside, false, true},
	} {
		if got := Confined(c.r, c.a, c.kind); got != c.want {
			t.Errorf("%s: Confined = %v", c.name, got)
		}
	}
}

func TestRepolessSandboxHistoryDoesNotMigrateOnRepeatedRestore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := &Core{Root: root}
	c.MarkSandboxed(&Tab{AgentState: Agent{InSandbox: true, Tab: state.Tab{Dir: dir}}})
	for range 3 {
		c.SaveSession()
		sess, err := state.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if sess.SandboxHistoryVersion != 1 {
			t.Fatal("missing persisted format marker")
		}
		c = &Core{Root: root}
		c.restoreSandboxed(sess)
		if !c.SandboxedDirs[dir] || len(c.SandboxedRepos) != 0 || gitsafe.RepoSandboxed(repo) {
			t.Fatal("repoless session tainted unrelated repository")
		}
	}
}

func TestSandboxRepositoryHistoryDoesNotTaintUntouchedTabs(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := &Core{}
	touched := &Tab{AgentState: Agent{InSandbox: true, Tab: state.Tab{Dir: dir, Worktrees: []workspace.Worktree{{Repo: repo, Path: dir}}}}}
	c.MarkSandboxed(touched)
	if !touched.HasSandboxed() || !c.SandboxedRepos[repo] || !gitsafe.RepoSandboxed(repo) {
		t.Fatal("sandboxed worktree did not record both kinds of history")
	}
	untouched := &Tab{AgentState: Agent{Tab: state.Tab{Dir: t.TempDir(), Worktrees: []workspace.Worktree{{Repo: repo, Path: t.TempDir()}}}}}
	c.MarkSandboxed(untouched)
	if untouched.HasSandboxed() {
		t.Fatal("untouched tab inherited repository taint")
	}
	reopened := &Tab{AgentState: Agent{Tab: state.Tab{Dir: dir}}}
	c.MarkSandboxed(reopened)
	if !reopened.HasSandboxed() {
		t.Fatal("reopened worktree lost its individual history")
	}
}

func TestRestoreAssociatesMissingWorktreeBeforePruning(t *testing.T) {
	repo := t.TempDir()
	missing := filepath.Join(t.TempDir(), "removed")
	c := &Core{}
	c.restoreSandboxed(state.Session{
		SandboxHistoryVersion: 1,
		SandboxedDirs:         map[string]bool{missing: true},
		Tabs:                  []state.Tab{{Worktrees: []workspace.Worktree{{Repo: repo, Path: missing}}}},
	})
	if c.SandboxedDirs[missing] || !c.SandboxedRepos[repo] || !gitsafe.RepoSandboxed(repo) {
		t.Fatal("missing path was not pruned after preserving repository history")
	}
}

// TestLegacySandboxHistoryMarksOnlyItsRepositories: a session from before
// repositories were kept has one folder an agent ran in a sandbox in, a
// worktree of one repository among several. Only that repository is
// marked, with the folder as where its history is from; so is the one of
// a tab whose worktree is gone, which the tab names. The others are not.
func TestLegacySandboxHistoryMarksOnlyItsRepositories(t *testing.T) {
	root := t.TempDir()
	var repos []string
	for _, name := range []string{"one", "two", "three", "four"} {
		repos = append(repos, gitRepo(t, root, name).Path)
	}
	one, two, three, four := repos[0], repos[1], repos[2], repos[3]
	wts := t.TempDir()
	wt := filepath.Join(wts, "feat-one")
	// A tab of several repositories has their worktrees under its folder.
	group := filepath.Join(wts, "both")
	for _, argv := range [][]string{
		{"git", "-C", one, "worktree", "add", "-q", "-b", "feat", wt},
		{"git", "-C", four, "worktree", "add", "-q", "-b", "both", filepath.Join(group, "four")},
	} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	gone := filepath.Join(wts, "gone-three")
	c := &Core{Root: root}
	c.restoreSandboxed(state.Session{
		SandboxedDirs: map[string]bool{wt: true, gone: true, group: true, filepath.Join(wts, "nobody's"): true},
		Tabs:          []state.Tab{{Name: "old", Dir: gone, Worktrees: []workspace.Worktree{{Repo: three, Path: gone}}}},
	})
	if len(c.SandboxedRepos) != 3 || !c.SandboxedRepos[one] || !c.SandboxedRepos[three] || !c.SandboxedRepos[four] {
		t.Fatalf("marked %v, want only %s, %s and %s", c.SandboxedRepos, one, three, four)
	}
	if gitsafe.RepoSandboxed(two) || !gitsafe.RepoSandboxed(one) {
		t.Fatal("gitsafe was not told the same")
	}
	if !strings.Contains(c.SandboxSources[one], wt) || !strings.Contains(c.SandboxSources[three], "tab old") {
		t.Fatalf("sources = %v", c.SandboxSources)
	}
	if _, from, ok := gitsafe.SandboxSource(one); !ok || !strings.Contains(from, wt) {
		t.Fatalf("gitsafe's source = %q, %v", from, ok)
	}
}

// TestForgetSandboxed: the user has nerdr forget a repository's sandbox
// history. The repository, its folders and its tab lose theirs, and stay
// without it when the tab is looked at again; another repository keeps
// its own. While an agent runs in a sandbox there, it is refused.
func TestForgetSandboxed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	repo, other := gitRepo(t, root, "repo").Path, gitRepo(t, root, "other").Path
	dir, otherDir := t.TempDir(), t.TempDir()
	c := &Core{Root: root}
	tab := &Tab{AgentState: Agent{InSandbox: true, Tab: state.Tab{Name: "feat", Dir: dir, Worktrees: []workspace.Worktree{{Repo: repo, Path: dir}}}}}
	kept := &Tab{AgentState: Agent{InSandbox: true, Tab: state.Tab{Name: "kept", Dir: otherDir, Worktrees: []workspace.Worktree{{Repo: other, Path: otherDir}}}}}
	c.Tabs = []*Tab{tab, kept}
	c.MarkSandboxed(tab)
	c.MarkSandboxed(kept)
	if why := c.ForgetSandboxed(repo); !strings.Contains(why, "feat") || !c.SandboxedRepos[repo] {
		t.Fatalf("forgot under an agent in a sandbox: %q", why)
	}
	tab.InSandbox = false
	if why := c.ForgetSandboxed(repo); why != "" {
		t.Fatal(why)
	}
	c.MarkSandboxed(tab)
	if tab.HasSandboxed() || c.SandboxedRepos[repo] || c.SandboxedDirs[dir] || c.SandboxSources[repo] != "" || gitsafe.RepoSandboxed(repo) {
		t.Fatalf("history left: tab %v, repos %v, dirs %v", tab.HasSandboxed(), c.SandboxedRepos, c.SandboxedDirs)
	}
	if !c.SandboxedRepos[other] || !gitsafe.RepoSandboxed(other) || !kept.HasSandboxed() {
		t.Fatal("another repository lost its history")
	}
	// As saved, so restored.
	sess, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if sess.SandboxedRepos[repo] || !sess.SandboxedRepos[other] {
		t.Fatalf("saved %v", sess.SandboxedRepos)
	}
	// nerdr sandbox clear, on the saved session.
	sess.Tabs = []state.Tab{kept.Tab}
	if !ForgetSandboxedIn(&sess, other) || len(sess.SandboxedRepos) != 0 || len(sess.SandboxedDirs) != 0 || sess.Tabs[0].Sandboxed {
		t.Fatalf("session keeps %v, %v", sess.SandboxedRepos, sess.SandboxedDirs)
	}
	if ForgetSandboxedIn(&sess, other) {
		t.Fatal("found history to forget twice")
	}
}
