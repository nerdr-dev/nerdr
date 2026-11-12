package gitsafe

import (
	"os"
	"path/filepath"
	"testing"
)

// TestForgetSandboxed: a repository's sandbox history is told with where
// it is from, to its worktrees as well, and is gone once forgotten,
// however the repository was named; another keeps its own.
func TestForgetSandboxed(t *testing.T) {
	root := t.TempDir()
	repo, other := filepath.Join(root, "repo"), filepath.Join(root, "other")
	for _, r := range []string{repo, other} {
		if err := os.MkdirAll(filepath.Join(r, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	Trust(repo, wt)
	SandboxedRepoFrom(repo, "tab feat, "+wt)
	SandboxedRepoFrom(repo, "a later one")
	Sandboxed(wt)
	SandboxedRepo(other)

	if !RepoSandboxed(link) {
		t.Fatal("not sandboxed by its other name")
	}
	if r, from, ok := SandboxSource(repo); !ok || r != repo || from != "tab feat, "+wt {
		t.Fatalf("source = %q, %q, %v", r, from, ok)
	}
	if _, from, ok := SandboxSource(other); !ok || from != "" {
		t.Fatalf("a repository nobody gave a source: %q, %v", from, ok)
	}
	ForgetSandboxed(link)
	if RepoSandboxed(repo) || RepoSandboxed(link) {
		t.Fatal("history left after it was forgotten")
	}
	if _, _, ok := SandboxSource(repo); ok {
		t.Fatal("a source left after it was forgotten")
	}
	if !RepoSandboxed(other) {
		t.Fatal("another repository lost its history")
	}
}
