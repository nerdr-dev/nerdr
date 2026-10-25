package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/analysis"
)

// TestProjectIsReadAgainInTheBackground covers the refresh not walking the
// worktree on the UI's goroutine: the project arrives as a message, and
// replaces the one shown only when it is for the worktree still selected
// and its files changed.
func TestProjectIsReadAgainInTheBackground(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("export const x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.ts")
	shown := analysis.LoadProject(dir)
	m := Model{curWT: dir, project: shown, projectLoad: true}

	nm, _ := m.Update(projectMsg{wt: "elsewhere", project: analysis.LoadProject(dir)})
	if got := nm.(Model); got.project != shown || !got.projectLoad {
		t.Fatal("a load for another worktree was taken")
	}
	nm, _ = m.Update(projectMsg{wt: dir, project: analysis.LoadProject(dir)})
	if got := nm.(Model); got.project != shown || got.projectLoad {
		t.Fatal("an unchanged project replaced the one shown, or the load stayed pending")
	}
	write("b.ts")
	next := analysis.LoadProject(dir)
	nm, _ = m.Update(projectMsg{wt: dir, project: next})
	if got := nm.(Model); got.project != next {
		t.Fatal("a changed project was not taken")
	}
}

// TestAskWithNoWorktreeDoesNothing covers "?" on an empty worktree list,
// which used to index it to say what the change is compared with.
func TestAskWithNoWorktreeDoesNothing(t *testing.T) {
	m := Model{focus: focusRepos}
	if cmd := m.askSelected(false); cmd != nil {
		t.Fatal("asked about a worktree that is not there")
	}
}
