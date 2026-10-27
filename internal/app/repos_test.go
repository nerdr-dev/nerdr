package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestReposDialog covers picking a tab's repositories: the last one stays,
// a new one is ticked, and what git says of it is asked about before
// anything is done.
func TestReposDialog(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	m.Update(keyMsg("ctrl+q"))
	m.Update(keyMsg("i"))
	m.Update(keyMsg("r"))
	d, ok := m.dialog.(*repoDialog)
	if !ok {
		t.Fatalf("dialog = %T", m.dialog)
	}
	if len(d.repos) != 1 || d.repos[0].Path != "/r/app" || !d.picked["/r/app"] {
		t.Fatalf("repos = %+v, picked %v", d.repos, d.picked)
	}
	m.Update(keyMsg(" "))
	if !d.picked["/r/app"] || !strings.Contains(ansi.Strip(m.View()), "at least one repository") {
		t.Fatalf("the last repository was let go of:\n%s", ansi.Strip(m.View()))
	}

	d.repos = append(d.repos, workspace.Repo{Name: "lib", Path: "/r/lib"})
	m.Update(keyMsg("down"))
	m.Update(keyMsg(" "))
	if !d.picked["/r/lib"] || !strings.Contains(ansi.Strip(m.View()), "lib  added") {
		t.Fatalf("lib not ticked:\n%s", ansi.Strip(m.View()))
	}
	m.Update(keyMsg("enter"))
	if d.busy == "" {
		t.Fatal("enter did not ask git")
	}
	lib := workspace.Repo{Name: "lib", Path: "/r/lib"}
	m.Update(repoCheckedMsg{d: d, change: &repoChange{nest: true, add: workspace.Plan{Branch: "login", Dir: tb.Dir,
		Items: []workspace.PlanItem{{Repo: lib, Path: "/w/login/lib", Mode: "new", Base: "main"}}}}})
	v := ansi.Strip(m.View())
	for _, want := range []string{"moves into", "lib: new branch from main", "y go ahead"} {
		if !strings.Contains(v, want) {
			t.Fatalf("%q missing:\n%s", want, v)
		}
	}
	m.Update(keyMsg("n"))
	if d.change != nil || m.dialog != d {
		t.Fatal("n did not go back to the list")
	}
}

// TestReposDirtyAsksAgain covers letting go of a worktree with
// uncommitted changes: y does not do it, Y does.
func TestReposDirtyAsksAgain(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	d := &repoDialog{t: tb, picked: map[string]bool{}, change: &repoChange{remove: tb.Worktrees, dirty: []string{"login"}}}
	m.dialog = d
	if v := ansi.Strip(m.View()); !strings.Contains(v, "will be lost") || !strings.Contains(v, "Y go ahead anyway") {
		t.Fatalf("not warned:\n%s", v)
	}
	m.Update(keyMsg("y"))
	if m.dialog != d {
		t.Fatal("y deleted uncommitted changes")
	}
}

// TestReposChanged covers the tab going from one repository to two and
// back: its group follows, and its agent is told each time.
func TestReposChanged(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	app := workspace.Worktree{Repo: "/r/app", Path: "/w/login/app", Branch: "login"}
	lib := workspace.Worktree{Repo: "/r/lib", Path: "/w/login/lib", Branch: "login"}

	m.Update(reposChangedMsg{t: tb, wts: []workspace.Worktree{app, lib}, added: []workspace.Worktree{lib}, nested: &app})
	if tb.group() != multiGroup || len(tb.Worktrees) != 2 {
		t.Fatalf("group %q, worktrees %+v", tb.group(), tb.Worktrees)
	}
	if n := len(m.groupTabs("app")); n != 1 {
		t.Fatalf("app has %d tabs", n)
	}
	if len(tb.Messages) != 1 || !strings.Contains(tb.Messages[0].Text, "Added lib") ||
		!strings.Contains(tb.Messages[0].Text, "moved to /w/login/app") {
		t.Fatalf("messages = %+v", tb.Messages)
	}
	if !tb.info.stale {
		t.Fatal("the branches are not looked at again")
	}

	m.Update(reposChangedMsg{t: tb, wts: []workspace.Worktree{app}, removed: []workspace.Worktree{lib}})
	if tb.group() != "app" || tb.Group != "" || tb.Dir != "/w/login" {
		t.Fatalf("group %q (%q), dir %s", tb.group(), tb.Group, tb.Dir)
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
	m := testModel(t)
	tb := m.cur()
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
	cmd := m.changeRepos(tb, &repoChange{nest: true, add: add, remove: wts})
	if !tb.reposBusy {
		t.Fatal("the tab is not held while its repositories change")
	}
	msg := cmd().(reposChangedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	want := filepath.Join(plan.Dir, "b")
	if len(msg.wts) != 1 || msg.wts[0].Path != want {
		t.Fatalf("worktrees = %+v", msg.wts)
	}
	if len(msg.removed) != 1 || msg.removed[0].Path != filepath.Join(plan.Dir, "a") {
		t.Fatalf("removed = %+v", msg.removed)
	}
	if _, err := os.Stat(filepath.Join(plan.Dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("a's worktree is still there: %v", err)
	}
}

// TestReposTellEveryAgent covers a tab with another agent beside its own:
// both are told, and both have the worktrees as they are now.
func TestReposTellEveryAgent(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	other := &tab{Tab: state.Tab{ID: "x", Name: tb.Name, Dir: tb.Dir, Agent: "codex", Worktrees: tb.Worktrees, Num: 2}, partOf: tb}
	tb.paneList = append(tb.panes(), &paneTab{kind: paneAgent, a: other})
	lib := workspace.Worktree{Repo: "/r/lib", Path: "/w/login/lib", Branch: "login"}
	wts := []workspace.Worktree{tb.Worktrees[0], lib}
	m.Update(reposChangedMsg{t: tb, wts: wts, added: []workspace.Worktree{lib}})
	for _, a := range []*tab{tb, other} {
		if len(a.Messages) != 1 || !strings.Contains(a.Messages[0].Text, "Added lib") || len(a.Worktrees) != 2 {
			t.Fatalf("agent %d: messages %+v, worktrees %+v", a.Num, a.Messages, a.Worktrees)
		}
	}
}

// TestReposOneChangeAtATime covers a second change asked for while one
// runs: refused, as is closing the tab, until the first is done.
func TestReposOneChangeAtATime(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	if m.changeRepos(tb, &repoChange{remove: tb.Worktrees}) == nil {
		t.Fatal("the change did not start")
	}
	if m.changeRepos(tb, &repoChange{remove: tb.Worktrees}) != nil {
		t.Fatal("a second change started while the first ran")
	}
	m.Update(keyMsg("ctrl+q"))
	m.Update(keyMsg("i"))
	m.Update(keyMsg("r"))
	if m.dialog != nil {
		t.Fatalf("the dialog opened while the change ran: %T", m.dialog)
	}
	m.closeTab(tb, true, false, nil)
	if m.tabIndex(tb) < 0 {
		t.Fatal("the tab closed while its repositories changed")
	}
	m.Update(reposChangedMsg{t: tb, wts: tb.Worktrees})
	if tb.reposBusy {
		t.Fatal("still held once the change was done")
	}
	m.Update(keyMsg("ctrl+q"))
	m.Update(keyMsg("i"))
	m.Update(keyMsg("r"))
	if _, ok := m.dialog.(*repoDialog); !ok {
		t.Fatalf("dialog = %T after the change", m.dialog)
	}
}
