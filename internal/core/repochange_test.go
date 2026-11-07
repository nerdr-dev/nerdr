package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// restored is a core with three tabs, as testModel has in the TUI: login
// and logout in /r/app, parse in /r/lib.
func restored(t *testing.T) *Core {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mk := func(id, name, repo string) state.Tab {
		return state.Tab{ID: id, Name: name, Agent: "claude", Dir: "/w/" + name,
			Worktrees: []workspace.Worktree{{Repo: repo, Path: "/w/" + name, Branch: name}}}
	}
	m := &Core{Cfg: config.Config{Prefix: "ctrl+q"}, Root: t.TempDir(), Client: NoClient{}}
	m.Restore(state.Session{Tabs: []state.Tab{mk("1", "login", "/r/app"), mk("2", "logout", "/r/app"), mk("3", "parse", "/r/lib")}}, time.Now())
	return m
}

// TestReposChanged covers the tab going from one repository to two and
// back: its group follows, and its agent is told each time.
func TestReposChanged(t *testing.T) {
	m := restored(t)
	tb := m.Tabs[0]
	app := workspace.Worktree{Repo: "/r/app", Path: "/w/login/app", Branch: "login"}
	lib := workspace.Worktree{Repo: "/r/lib", Path: "/w/login/lib", Branch: "login"}

	m.Handle(ReposChangedMsg{T: tb, Wts: []workspace.Worktree{app, lib}, Added: []workspace.Worktree{lib}, Nested: &app})
	if tb.GroupName() != MultiGroup || len(tb.Worktrees) != 2 {
		t.Fatalf("group %q, worktrees %+v", tb.GroupName(), tb.Worktrees)
	}
	if n := len(m.GroupTabs("app")); n != 1 {
		t.Fatalf("app has %d tabs", n)
	}
	if len(tb.Messages) != 1 || !strings.Contains(tb.Messages[0].Text, "Added lib") ||
		!strings.Contains(tb.Messages[0].Text, "moved to /w/login/app") {
		t.Fatalf("messages = %+v", tb.Messages)
	}
	if !tb.Info.Stale {
		t.Fatal("the branches are not looked at again")
	}

	m.Handle(ReposChangedMsg{T: tb, Wts: []workspace.Worktree{app}, Removed: []workspace.Worktree{lib}})
	if tb.GroupName() != "app" || tb.Group != "" || tb.Dir != "/w/login" {
		t.Fatalf("group %q (%q), dir %s", tb.GroupName(), tb.Group, tb.Dir)
	}
	if len(tb.Messages) != 2 || !strings.Contains(tb.Messages[1].Text, "Removed lib") {
		t.Fatalf("messages = %+v", tb.Messages)
	}
}

// gitRepo makes a repository with one commit on main under root.
func gitRepo(t *testing.T, root, name string) workspace.Repo {
	t.Helper()
	dir := filepath.Join(root, name)
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main", dir},
		{"git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	return workspace.Repo{Name: name, Path: dir}
}

// TestReposReplaceTheOnlyOne covers a tab with one repository given
// another in its place, in one change: the worktree let go of is deleted
// where nesting it put it.
func TestReposReplaceTheOnlyOne(t *testing.T) {
	m := restored(t)
	tb := m.Tabs[0]
	root := t.TempDir()
	a, b := gitRepo(t, root, "a"), gitRepo(t, root, "b")
	plan, err := workspace.MakePlan(filepath.Join(root, "worktrees"), "feat", []workspace.Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	tb.Dir, tb.Worktrees = plan.Dir, wts
	add, err := workspace.PlanAdd(plan.Dir, "feat", []workspace.Repo{b})
	if err != nil {
		t.Fatal(err)
	}
	cmd := m.ChangeRepos(tb, RepoChange{Nest: true, Add: add, Remove: wts})
	if !tb.ReposBusy {
		t.Fatal("the tab is not held while its repositories change")
	}
	msg := cmd().(ReposChangedMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	want := filepath.Join(plan.Dir, "b")
	if len(msg.Wts) != 1 || msg.Wts[0].Path != want {
		t.Fatalf("worktrees = %+v", msg.Wts)
	}
	if len(msg.Removed) != 1 || msg.Removed[0].Path != filepath.Join(plan.Dir, "a") {
		t.Fatalf("removed = %+v", msg.Removed)
	}
	if _, err := os.Stat(filepath.Join(plan.Dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("a's worktree is still there: %v", err)
	}
}

// TestReposTellEveryAgent covers a tab with another agent beside its own:
// both are told, and both have the worktrees as they are now.
func TestReposTellEveryAgent(t *testing.T) {
	m := restored(t)
	tb := m.Tabs[0]
	other := &Tab{AgentState: Agent{Tab: state.Tab{ID: "x", Name: tb.Name, Dir: tb.Dir, Agent: "codex", Worktrees: tb.Worktrees, Num: 2}}, PartOf: tb}
	tb.PaneList = append(tb.Panes(), &PaneTab{Kind: PaneAgent, A: other})
	lib := workspace.Worktree{Repo: "/r/lib", Path: "/w/login/lib", Branch: "login"}
	wts := []workspace.Worktree{tb.Worktrees[0], lib}
	m.Handle(ReposChangedMsg{T: tb, Wts: wts, Added: []workspace.Worktree{lib}})
	for _, a := range []*Tab{tb, other} {
		if len(a.Messages) != 1 || !strings.Contains(a.Messages[0].Text, "Added lib") || len(a.Worktrees) != 2 {
			t.Fatalf("agent %d: messages %+v, worktrees %+v", a.Num, a.Messages, a.Worktrees)
		}
	}
}
