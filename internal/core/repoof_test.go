package core

import (
	"path/filepath"
	"testing"
)

// TestRepoOf covers the repository of a tab's folder, for F4's context
// list: a worktree's is its main checkout, and a folder in none has none.
func TestRepoOf(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	gitIn(t, root, "init", "-q", "-b", "main", main)
	gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat", wt)
	main, _ = filepath.EvalSymlinks(main)
	for dir, want := range map[string]string{main: main, wt: main, filepath.Join(wt, "."): main} {
		if got, _ := filepath.EvalSymlinks(RepoOf(dir)); got != want {
			t.Errorf("RepoOf(%s) = %q, want %q", dir, got, want)
		}
	}
	if got := RepoOf(t.TempDir()); got != "" {
		t.Errorf("a folder in no repository has %q", got)
	}
}
