package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// commit writes file in dir and commits it, returning the commit's id.
func commit(t *testing.T, dir, file, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", file)
	run(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", file+" "+content)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestPickTakesCommitsInTheOrderTheyWereMade(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	root := t.TempDir()
	r := newRepo(t, root, "app")
	run(t, r.Path, "git", "switch", "-q", "-c", "big")
	first := commit(t, r.Path, "a.txt", "1")
	commit(t, r.Path, "other.txt", "not this one")
	second := commit(t, r.Path, "a.txt", "2") // builds on first
	run(t, r.Path, "git", "switch", "-q", "main")

	wt := filepath.Join(root, "wt")
	run(t, r.Path, "git", "worktree", "add", "-q", "-b", "part", wt, "main")
	w := Worktree{Repo: r.Path, Path: wt, Branch: "part"}

	n, err := Pick(w, []string{second, first, "0123456789abcdef0123456789abcdef01234567"})
	if err != nil || n != 2 {
		t.Fatalf("Pick = %d, %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(b) != "2" {
		t.Fatalf("a.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(wt, "other.txt")); err == nil {
		t.Fatal("a commit not asked for came along")
	}
}

func TestPickThatConflictsLeavesTheBranchAlone(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	root := t.TempDir()
	r := newRepo(t, root, "app")
	run(t, r.Path, "git", "switch", "-q", "-c", "big")
	commit(t, r.Path, "a.txt", "1")
	change := commit(t, r.Path, "a.txt", "2") // needs base, which is not picked
	run(t, r.Path, "git", "switch", "-q", "main")

	wt := filepath.Join(root, "wt")
	run(t, r.Path, "git", "worktree", "add", "-q", "-b", "part", wt, "main")
	commit(t, wt, "a.txt", "mine")
	head := Head(Worktree{Path: wt})
	if _, err := Pick(Worktree{Repo: r.Path, Path: wt}, []string{change}); err == nil {
		t.Fatal("a conflicting pick succeeded")
	}
	if Head(Worktree{Path: wt}) != head {
		t.Fatal("the branch moved")
	}
	if s, _ := exec.Command("git", "-C", wt, "status", "--porcelain").Output(); len(s) != 0 {
		t.Fatalf("left behind: %s", s)
	}
}

// TestUncommittedCountsFiles covers telling how much work is not in a
// commit: changed, untracked and renamed files, each once.
func TestUncommittedCountsFiles(t *testing.T) {
	r := newRepo(t, t.TempDir(), "app")
	w := Worktree{Repo: r.Path, Path: r.Path, Branch: "main"}
	if n := Uncommitted(w); n != 0 {
		t.Fatalf("a clean worktree has %d", n)
	}
	commit(t, r.Path, "a.txt", "one")
	commit(t, r.Path, "c.txt", "three")
	if err := os.WriteFile(filepath.Join(r.Path, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Path, "b.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, r.Path, "git", "mv", "c.txt", "d.txt")
	if n := Uncommitted(w); n != 3 {
		t.Fatalf("Uncommitted = %d, want 3", n)
	}
}

// Commits made in the same second, as a rebase makes them, are still
// picked parent first.
func TestPickOrdersByAncestryNotTime(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-01T00:00:00Z")
	root := t.TempDir()
	r := newRepo(t, root, "app")
	run(t, r.Path, "git", "switch", "-q", "-c", "big")
	first := commit(t, r.Path, "a.txt", "1")
	second := commit(t, r.Path, "a.txt", "2")
	run(t, r.Path, "git", "switch", "-q", "main")
	wt := filepath.Join(root, "wt")
	run(t, r.Path, "git", "worktree", "add", "-q", "-b", "part", wt, "main")
	w := Worktree{Repo: r.Path, Path: wt, Branch: "part"}
	for _, order := range [][]string{{first, second}, {second, first}} {
		run(t, wt, "git", "reset", "-q", "--hard", "main")
		if n, err := Pick(w, order); err != nil || n != 2 {
			t.Fatalf("Pick(%v) = %d, %v", order, n, err)
		}
		if b, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(b) != "2" {
			t.Fatalf("a.txt = %q", b)
		}
	}
}
