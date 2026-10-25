package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nerdr/internal/state"
	"nerdr/internal/workspace"
)

// TestPullRequestNeedsCommits covers c on a branch whose work is not
// committed yet: it says so, rather than pushing nothing and passing on
// gh's refusal.
func TestPullRequestNeedsCommits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", dir},
		{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", dir, "switch", "-q", "-c", "feat"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tb := &tab{Tab: state.Tab{Name: "feat"}}
	wts := []workspace.Worktree{{Repo: dir, Path: dir, Branch: "feat"}}
	msg := createPRs(tb, wts, nil)
	if !strings.Contains(msg.err, "no commits ahead of main") ||
		!strings.Contains(msg.err, "1 file changed but not committed") {
		t.Fatalf("got %+v", msg)
	}
}
