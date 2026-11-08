package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// TestRepoScan covers core's look at the repositories: it finds them, a
// look done after one that looked later is dropped, and the groups of
// worktrees offered are those no tab has open.
func TestRepoScan(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main", filepath.Join(root, "app"))
	m := &Core{Root: root, WtRoot: filepath.Join(root, ".wt")}
	old := m.ScanRepos()
	m.OnRepoScan(m.ScanRepos()().(RepoScanMsg))
	if len(m.Scan.Repos) != 1 || m.Scan.Repos[0].Name != "app" || m.Scan.At.IsZero() {
		t.Fatalf("scan %+v", m.Scan)
	}
	at := m.Scan.At
	msg := old().(RepoScanMsg)
	msg.Scan.At, msg.Scan.Repos = at.Add(-time.Second), nil
	m.OnRepoScan(msg)
	if len(m.Scan.Repos) != 1 || m.Scan.At != at {
		t.Fatalf("an older look took the place of a newer one: %+v", m.Scan)
	}

	m.Scan.Groups = []workspace.Group{{Name: "a", Dir: "/w/a"}, {Name: "b", Dir: "/w/b"}}
	m.Tabs = []*Tab{{AgentState: Agent{Tab: state.Tab{ID: "1", Dir: "/w/a"}}}}
	if g := m.ClosedGroups(); len(g) != 1 || g[0].Name != "b" {
		t.Fatalf("closed groups %+v", g)
	}
}

// TestCheckRepoChange covers the check of a change to a tab's
// repositories: a worktree to delete with changes not committed says so.
func TestCheckRepoChange(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	gitIn(t, root, "init", "-q", "-b", "main", repo)
	gitIn(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	writeIn(t, repo, "a.txt", "new\n")
	wt := workspace.Worktree{Repo: repo, Path: repo, Branch: "main"}
	m := &Core{Root: root}
	m.Tabs = []*Tab{{AgentState: Agent{Tab: state.Tab{ID: "1", Dir: root, Worktrees: []workspace.Worktree{wt, {Repo: "/r/lib", Path: "/w/lib", Branch: "main"}}}}}}
	cmd := CheckRepoChange{Job: "j", T: m.Ref(m.Tabs[0]), Remove: []workspace.Worktree{wt}}.Do(m)
	got, ok := cmd().(Reply).Ev.(RepoCheckedMsg)
	if !ok || got.Job != "j" || got.Err != "" || got.Change == nil || len(got.Change.Dirty) != 1 {
		t.Fatalf("got %+v", got)
	}
}
