package core

import (
	"os"
	"path/filepath"
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
