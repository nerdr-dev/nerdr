package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// paneCore is a core with tab t1, its agent not running, where terminals
// and agents can be started.
func paneCore(t *testing.T) *Core {
	t.Helper()
	m := &Core{Client: NoClient{}, Spawner: &recorder{}, Root: t.TempDir(),
		Agents: []agents.Kind{agents.Claude, agents.Codex}, AgentsAt: time.Now()}
	m.Cfg.Shell = "cat"
	tab := &Tab{}
	tab.ID, tab.Name, tab.Dir, tab.Agent, tab.Light, tab.Num = "t1", "fix-login", t.TempDir(), agents.Claude, StIdle, 1
	m.Tabs = []*Tab{tab}
	t.Cleanup(func() {
		for _, p := range tab.Panes() {
			if tp := p.Term(); tp != nil {
				tp.Close()
			}
		}
	})
	return m
}

// TestAPINewTerminal: a terminal opens in the tab's worktree, quietly, by
// the ID state gives it.
func TestAPINewTerminal(t *testing.T) {
	m := paneCore(t)
	id, _, err := m.APINewTerminal("t1")
	if err != nil || id == "" {
		t.Fatalf("id %q, %v", id, err)
	}
	st := m.APIState()
	if ts := st.Tabs[0].Terminals; len(ts) != 1 || ts[0].ID != id {
		t.Fatalf("terminals %+v, want %s", ts, id)
	}
	if specs := m.Spawner.(*recorder).specs; len(specs) != 1 || specs[0].Dir != m.Tabs[0].Dir {
		t.Fatalf("started %+v", specs)
	}
	if _, _, err := m.APINewTerminal("nope"); err == nil || !strings.Contains(err.Error(), `no tab "nope"`) {
		t.Fatalf("unknown tab: %v", err)
	}
	m.Tabs[0].ReposBusy = true
	if _, _, err := m.APINewTerminal("t1"); err == nil || !strings.Contains(err.Error(), "repositories are being changed") {
		t.Fatalf("repositories busy: %v", err)
	}
}

// TestAPINewAgent: an agent more starts beside the tab's own, by the kind
// asked for, and is in state as one of the tab's agents.
func TestAPINewAgent(t *testing.T) {
	m := paneCore(t)
	for _, c := range []struct{ kind, model, want string }{
		{"", "", "name the agent"},
		{"nope", "", `no agent "nope"`},
	} {
		if _, _, err := m.APINewAgent("t1", c.kind, c.model, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c, err, c.want)
		}
	}
	id, _, err := m.APINewAgent("t1", "Codex", "", "write the tests")
	if err != nil || id == "" {
		t.Fatalf("id %q, %v", id, err)
	}
	ags := m.APIState().Tabs[0].Agents
	if len(ags) != 2 || ags[1].ID != id || ags[1].Kind != "codex" || ags[1].Num != 2 {
		t.Fatalf("agents %+v, want codex %s", ags, id)
	}
	if a := TabList(m.Tabs).Find(id); a == nil || len(a.Messages) != 1 || a.Messages[0].Text != "write the tests" {
		t.Fatalf("agent %+v", a)
	}
	// An agent's ID names its tab too.
	if _, _, err := m.APINewTerminal(id); err != nil {
		t.Fatalf("terminal in an agent's tab: %v", err)
	}
}

// TestAPIReviewChecksFirst: a review is refused at once for what the
// review would refuse later, with only a notice to tell.
func TestAPIReviewChecksFirst(t *testing.T) {
	m := paneCore(t)
	of := m.Tabs[0]
	if _, err := m.APIReview("nope", "codex", ""); err == nil || !strings.Contains(err.Error(), `no agent "nope"`) {
		t.Fatalf("unknown agent: %v", err)
	}
	if _, err := m.APIReview("t1", "gemini", ""); err == nil || !strings.Contains(err.Error(), `no agent "gemini"`) {
		t.Fatalf("unknown kind: %v", err)
	}
	of.Review = &ReviewRun{Phase: Fixing}
	if _, err := m.APIReview("t1", "codex", ""); err == nil || !strings.Contains(err.Error(), "on the findings") {
		t.Fatalf("fixing: %v", err)
	}
	r := &Tab{ReviewOf: of}
	r.ID, r.Agent = "t1-review-codex", agents.Codex
	of.Review = &ReviewRun{Phase: Reviewing, Seats: []*Seat{{R: r}}}
	of.PaneList = append(of.Panes(), &PaneTab{Kind: PaneReview, A: r, Of: of})
	if _, err := m.APIReview("t1", "codex", ""); err == nil || !strings.Contains(err.Error(), "reviewing already") {
		t.Fatalf("a second codex: %v", err)
	}
	if _, err := m.APIReview(r.ID, "claude", ""); err == nil || !strings.Contains(err.Error(), "is a reviewer") {
		t.Fatalf("a reviewer's work: %v", err)
	}
	of.Review, of.PaneList = nil, nil
	if cmd, err := m.APIReview("t1", "codex", ""); err != nil || cmd == nil {
		t.Fatalf("cmd %v, %v", cmd != nil, err)
	}
}

// TestAPIClosePane: a terminal and an agent more close by their IDs; the
// tab's first agent stays, and an ID that names nothing is refused.
func TestAPIClosePane(t *testing.T) {
	m := paneCore(t)
	term, _, err := m.APINewTerminal("t1")
	if err != nil {
		t.Fatal(err)
	}
	ag, _, err := m.APINewAgent("t1", "codex", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.APIClosePane(term); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	if ts := m.APIState().Tabs[0].Terminals; len(ts) != 0 {
		t.Fatalf("terminals %+v after closing", ts)
	}
	if _, err := m.APIClosePane(ag); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if ags := m.APIState().Tabs[0].Agents; len(ags) != 1 || ags[0].ID != "t1" {
		t.Fatalf("agents %+v after closing", ags)
	}
	if _, err := m.APIClosePane("t1"); err == nil || !strings.Contains(err.Error(), "the tab's first agent") || !strings.Contains(err.Error(), "close_tab") {
		t.Fatalf("first agent: %v", err)
	}
	if len(m.Tabs[0].Panes()) != 1 {
		t.Fatalf("panes %d, want the first agent's", len(m.Tabs[0].Panes()))
	}
	for _, id := range []string{"nope", term, ag} {
		if _, err := m.APIClosePane(id); err == nil || !strings.Contains(err.Error(), "no agent or terminal") {
			t.Errorf("%q: %v", id, err)
		}
	}
	m.Quitting = true
	if _, err := m.APIClosePane("t1"); !errors.Is(err, ErrAPIQuitting) {
		t.Errorf("quitting: %v", err)
	}
}

func TestAPIPanesWhileQuitting(t *testing.T) {
	m := paneCore(t)
	m.Quitting = true
	if _, _, err := m.APINewTerminal("t1"); !errors.Is(err, ErrAPIQuitting) {
		t.Errorf("terminal %v", err)
	}
	if _, _, err := m.APINewAgent("t1", "codex", "", ""); !errors.Is(err, ErrAPIQuitting) {
		t.Errorf("agent %v", err)
	}
	if _, err := m.APIReview("t1", "codex", ""); !errors.Is(err, ErrAPIQuitting) {
		t.Errorf("review %v", err)
	}
	if _, err := m.APIStartClose("t1", false); !errors.Is(err, ErrAPIQuitting) {
		t.Errorf("close %v", err)
	}
}

// TestAPICloseTabKeepsTheWorktree: without remove the tab goes and its
// worktree stays; an agent's ID is not a tab's to close.
func TestAPICloseTabKeepsTheWorktree(t *testing.T) {
	m := paneCore(t)
	id, _, err := m.APINewAgent("t1", "codex", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.APIStartClose(id, false); err == nil || !strings.Contains(err.Error(), "not a tab") {
		t.Fatalf("an agent's ID: %v", err)
	}
	c, err := m.APIStartClose("t1", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Look(); err != nil {
		t.Fatal(err)
	}
	cmd, err := m.APIClose(c)
	if err != nil || len(m.Tabs) != 0 {
		t.Fatalf("tabs %d, %v", len(m.Tabs), err)
	}
	if ev, ok := cmd().(TabClosedMsg); !ok || ev.Err != "" || !strings.Contains(ev.Note, "the worktree is kept") {
		t.Fatalf("event %+v", ev)
	}
	if _, err := m.APIClose(c); err == nil || !strings.Contains(err.Error(), "gone already") {
		t.Fatalf("closed twice: %v", err)
	}
}

// TestAPICloseTabRemovesOnlyWhatIsSafe: remove deletes the worktree only
// when nothing would be lost by it: no changes not committed, no commits
// on no remote. The branch is kept.
func TestAPICloseTabRemovesOnlyWhatIsSafe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	m := paneCore(t)
	root := t.TempDir()
	repo := gitRepo(t, root, "web")
	bare := filepath.Join(root, "web.git")
	gitIn(t, root, "clone", "-q", "--bare", repo.Path, bare)
	gitIn(t, repo.Path, "remote", "add", "origin", bare)
	gitIn(t, repo.Path, "fetch", "-q", "origin")
	plan, err := workspace.MakePlan(filepath.Join(root, "worktrees"), "fix-login", []workspace.Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	tab := m.Tabs[0]
	tab.Dir, tab.Worktrees = plan.Dir, wts
	wt := wts[0].Path

	look := func() error {
		t.Helper()
		c, err := m.APIStartClose("t1", true)
		if err != nil {
			t.Fatal(err)
		}
		return c.Look()
	}
	if err := os.WriteFile(filepath.Join(wt, "new.go"), []byte("package web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := look(); err == nil || !strings.Contains(err.Error(), "uncommitted changes in "+filepath.Base(wt)) {
		t.Fatalf("dirty: %v", err)
	}
	gitIn(t, wt, "add", "new.go")
	gitIn(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "new")
	if err := look(); err == nil || !strings.Contains(err.Error(), "commits not pushed") {
		t.Fatalf("unpushed: %v", err)
	}
	gitIn(t, wt, "push", "-q", "origin", "fix-login")
	c, err := m.APIStartClose("t1", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Look(); err != nil {
		t.Fatalf("pushed: %v", err)
	}
	cmd, err := m.APIClose(c)
	if err != nil {
		t.Fatal(err)
	}
	if ev, ok := cmd().(TabClosedMsg); !ok || ev.Err != "" || !strings.Contains(ev.Note, "deleted its worktree") {
		t.Fatalf("event %+v", ev)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still there: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo.Path, "rev-parse", "--verify", "-q", "fix-login").CombinedOutput(); err != nil {
		t.Fatalf("branch gone: %v %s", err, out)
	}
}

// TestAPICloseTabKeepsACommitMadeAfterTheLook: a commit the agent makes
// once the worktree was looked at, on a detached HEAD where no branch
// keeps it, stops the worktree being deleted: git would delete it, clean
// as it is, and the commit with it.
func TestAPICloseTabKeepsACommitMadeAfterTheLook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	m := paneCore(t)
	root := t.TempDir()
	repo := gitRepo(t, root, "web")
	bare := filepath.Join(root, "web.git")
	gitIn(t, root, "clone", "-q", "--bare", repo.Path, bare)
	gitIn(t, repo.Path, "remote", "add", "origin", bare)
	gitIn(t, repo.Path, "fetch", "-q", "origin")
	plan, err := workspace.MakePlan(filepath.Join(root, "worktrees"), "fix-login", []workspace.Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	m.Tabs[0].Dir, m.Tabs[0].Worktrees = plan.Dir, wts
	wt := wts[0].Path
	gitIn(t, wt, "checkout", "-q", "--detach")

	c, err := m.APIStartClose("t1", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Look(); err != nil {
		t.Fatalf("pushed: %v", err)
	}
	gitIn(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "late")
	late := gitIn(t, wt, "rev-parse", "HEAD")
	cmd, err := m.APIClose(c)
	if err != nil {
		t.Fatal(err)
	}
	if ev, ok := cmd().(TabClosedMsg); !ok || !strings.Contains(ev.Err, "kept the worktree") {
		t.Fatalf("event %+v", ev)
	}
	if got := gitIn(t, wt, "rev-parse", "HEAD"); got != late {
		t.Fatalf("worktree at %s, want the late commit %s", got, late)
	}
}

// TestAPICloseTabKeepsACommitMadeDuringTheLook: a commit made while Look
// checks the heads it read, on a detached HEAD or on the branch, keeps
// the worktree and the branch, even when GitHub says the new head is
// merged: what Look checked is the commit it read.
func TestAPICloseTabKeepsACommitMadeDuringTheLook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	for _, detach := range []bool{true, false} {
		m := paneCore(t)
		root := t.TempDir()
		repo := gitRepo(t, root, "web")
		bare := filepath.Join(root, "web.git")
		gitIn(t, root, "clone", "-q", "--bare", repo.Path, bare)
		gitIn(t, repo.Path, "remote", "add", "origin", bare)
		gitIn(t, repo.Path, "fetch", "-q", "origin")
		plan, err := workspace.MakePlan(filepath.Join(root, "worktrees"), "fix-login", []workspace.Repo{repo})
		if err != nil {
			t.Fatal(err)
		}
		wts, err := plan.Execute()
		if err != nil {
			t.Fatal(err)
		}
		m.Tabs[0].Dir, m.Tabs[0].Worktrees = plan.Dir, wts
		wt := wts[0].Path
		if detach {
			gitIn(t, wt, "checkout", "-q", "--detach")
		}
		var late string
		afterHeads = func() {
			gitIn(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "late")
			late = gitIn(t, wt, "rev-parse", "HEAD")
		}
		mergedHeads = func(wts []workspace.Worktree) map[string]string {
			return map[string]string{wt: workspace.Head(wts[0])} // the late commit
		}
		c, err := m.APIStartClose("t1", true)
		if err != nil {
			t.Fatal(err)
		}
		err = c.Look()
		afterHeads, mergedHeads = nil, mergedBranchHeads
		if err != nil {
			t.Fatalf("detached %v: %v", detach, err)
		}
		cmd, err := m.APIClose(c)
		if err != nil {
			t.Fatal(err)
		}
		if ev, ok := cmd().(TabClosedMsg); !ok || !strings.Contains(ev.Err, "kept the worktree") {
			t.Fatalf("detached %v: event %+v", detach, ev)
		}
		if got := gitIn(t, wt, "rev-parse", "HEAD"); got != late {
			t.Fatalf("detached %v: worktree at %s, want the late commit %s", detach, got, late)
		}
		if !detach {
			if got := gitIn(t, repo.Path, "rev-parse", "fix-login"); got != late {
				t.Fatalf("branch at %s, want the late commit %s", got, late)
			}
		}
	}
}
