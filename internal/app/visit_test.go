package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestVisitMarksFurtherEditsAndSurvivesCommit(t *testing.T) {
	dir := testRepo(t)
	m := tabsModel(t, state.Tab{ID: "a", Dir: dir, Worktrees: []workspace.Worktree{{Path: dir, Repo: dir}}})
	tb := m.Cur()
	writeRepoFile(t, dir, "a.txt", "already changed\n")
	v := &core.FocusVisit{}
	tb.FocusVisit = v
	before := core.CaptureFocus(tb, v, false)().(core.FocusFilesMsg)
	writeRepoFile(t, dir, "a.txt", "changed again\n")
	writeRepoFile(t, dir, "new.txt", "new\n")
	after := core.CaptureFocus(tb, v, true)().(core.FocusFilesMsg)
	// The return capture can finish before the departure capture.
	m.focusFiles(after)
	m.focusFiles(before)
	if !tb.Unseen[dir]["a.txt"] || !tb.Unseen[dir]["new.txt"] {
		t.Fatalf("markers: %v", tb.Unseen)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "work")
	v2 := &core.FocusVisit{Before: after.Files}
	tb.FocusVisit = v2
	m.focusFiles(core.CaptureFocus(tb, v2, true)().(core.FocusFilesMsg))
	if len(tb.Unseen[dir]) != 0 {
		t.Fatal("committing identical content should not mark files")
	}
	m.focusFiles(before)
	if len(tb.Unseen[dir]) != 0 {
		t.Fatal("a delayed result from another visit replaced the baseline")
	}
	if got := gitRun(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("snapshot modified the real index: %s", got)
	}
}

func TestMenusRemainReachableInSmallTerminal(t *testing.T) {
	m := testModel(t)
	m.height, m.width = 18, 60
	m.openMenu(0, 0)
	mu := m.dialog.(*menu)
	for range len(mu.items) {
		mu.key(m, keyMsg("down"))
	}
	view := ansi.Strip(mu.view(m, 60, 14))
	if !strings.Contains(view, "Quit") || len(strings.Split(view, "\n")) > 14 {
		t.Fatal(view)
	}
	m.dialog = nil
	m.startPrefix()
	seen := ""
	for range 30 {
		lines := m.prefixHint(60)
		if len(lines) > m.height-m.barRows() {
			t.Fatal("prefix help overflows screen")
		}
		seen += ansi.Strip(strings.Join(lines, "\n"))
		m.handleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	for _, g := range core.CommandGroups() {
		if !strings.Contains(seen, g.Title) {
			t.Fatalf("group inaccessible: %s", g.Title)
		}
	}
	m.handleKey(keyMsg("?"))
	if m.prefix {
		t.Fatal("normal command failed after paging")
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
