package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestVisitMarksFurtherEditsAndSurvivesCommit(t *testing.T) {
	dir := testRepo(t)
	m := tabsModel(t, state.Tab{ID: "a", Dir: dir, Worktrees: []workspace.Worktree{{Path: dir, Repo: dir}}})
	tb := m.cur()
	writeRepoFile(t, dir, "a.txt", "already changed\n")
	v := &focusVisit{}
	tb.focusVisit = v
	before := captureFocus(tb, v, false)().(focusFilesMsg)
	writeRepoFile(t, dir, "a.txt", "changed again\n")
	writeRepoFile(t, dir, "new.txt", "new\n")
	after := captureFocus(tb, v, true)().(focusFilesMsg)
	// The return capture can finish before the departure capture.
	m.focusFiles(after)
	m.focusFiles(before)
	if !tb.unseen[dir]["a.txt"] || !tb.unseen[dir]["new.txt"] {
		t.Fatalf("markers: %v", tb.unseen)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "work")
	v2 := &focusVisit{before: after.files}
	tb.focusVisit = v2
	m.focusFiles(captureFocus(tb, v2, true)().(focusFilesMsg))
	if len(tb.unseen[dir]) != 0 {
		t.Fatal("committing identical content should not mark files")
	}
	m.focusFiles(before)
	if len(tb.unseen[dir]) != 0 {
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
	for _, g := range commandGroups() {
		if !strings.Contains(seen, g.title) {
			t.Fatalf("group inaccessible: %s", g.title)
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
