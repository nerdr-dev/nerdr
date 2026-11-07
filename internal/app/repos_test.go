package app

import (
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestReposDialog covers picking a tab's repositories: the last one stays,
// a new one is ticked, and what git says of it is asked about before
// anything is done.
func TestReposDialog(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
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
	m.Update(repoCheckedMsg{d: d, change: &core.RepoChange{Nest: true, Add: workspace.Plan{Branch: "login", Dir: tb.Dir,
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
	tb := m.Cur()
	d := &repoDialog{t: tb, picked: map[string]bool{}, change: &core.RepoChange{Remove: tb.Worktrees, Dirty: []string{"login"}}}
	m.dialog = d
	if v := ansi.Strip(m.View()); !strings.Contains(v, "will be lost") || !strings.Contains(v, "Y go ahead anyway") {
		t.Fatalf("not warned:\n%s", v)
	}
	m.Update(keyMsg("y"))
	if m.dialog != d {
		t.Fatal("y deleted uncommitted changes")
	}
}

// TestReposOneChangeAtATime covers a second change asked for while one
// runs: refused, as is closing the tab, until the first is done.
func TestReposOneChangeAtATime(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	change := core.ChangeRepos{T: m.Ref(tb), C: core.RepoChange{Remove: tb.Worktrees}}
	if m.do(change) == nil {
		t.Fatal("the change did not start")
	}
	if m.do(change) != nil {
		t.Fatal("a second change started while the first ran")
	}
	m.Update(keyMsg("ctrl+q"))
	m.Update(keyMsg("i"))
	m.Update(keyMsg("r"))
	if m.dialog != nil {
		t.Fatalf("the dialog opened while the change ran: %T", m.dialog)
	}
	m.closeTab(tb, true, false, nil)
	if m.TabIndex(tb) < 0 {
		t.Fatal("the tab closed while its repositories changed")
	}
	m.Update(core.ReposChangedMsg{T: tb, Wts: tb.Worktrees})
	if tb.ReposBusy {
		t.Fatal("still held once the change was done")
	}
	m.Update(keyMsg("ctrl+q"))
	m.Update(keyMsg("i"))
	m.Update(keyMsg("r"))
	if _, ok := m.dialog.(*repoDialog); !ok {
		t.Fatalf("dialog = %T after the change", m.dialog)
	}
}
