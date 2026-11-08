package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// TestPullRequestNeedsCommits covers P on a branch whose work is not
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
	wts := []workspace.Worktree{{Repo: dir, Path: dir, Branch: "feat"}}
	_, err := CreatePRs("feat", wts, nil, true)
	if !strings.Contains(err, "no commits ahead of main") ||
		!strings.Contains(err, "1 file changed but not committed") {
		t.Fatalf("got %q", err)
	}
}
