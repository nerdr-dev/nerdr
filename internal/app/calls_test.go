package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestCallsDialog: g counts git and gh by the tab whose worktree they
// ran in, and the rest as elsewhere.
func TestCallsDialog(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	tb.Worktrees = []workspace.Worktree{{Repo: "/r/a", Path: "/w/zz-a", Branch: "x"}}
	before, _ := proc.Counts()
	proc.Detach(exec.Command("git", "-C", "/w/zz-a/sub", "status"))
	proc.Detach(exec.Command("git", "-C", "/w/zz-a", "diff"))
	gh := exec.Command("gh", "pr", "view")
	gh.Dir = "/w/zz-a"
	proc.Detach(gh)
	proc.Detach(exec.Command("git", "-C", "/elsewhere/zz", "status"))
	calls, _ := proc.Counts()
	for c, n := range before {
		calls[c] -= n
	}
	var mine callRow
	for _, r := range m.callRows(calls) {
		if r.name == tb.Name {
			mine = r
		}
	}
	if mine.git != 2 || mine.gh != 1 {
		t.Fatalf("the tab's calls: %+v", mine)
	}
	m.run("g", keyMsg("g"))
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "git and gh calls") || !strings.Contains(view, "elsewhere") {
		t.Fatalf("no dialog:\n%s", view)
	}
}
