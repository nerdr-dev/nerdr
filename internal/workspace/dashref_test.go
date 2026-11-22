package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBranchStatsWithADashBranch covers an agent pointing the worktree's
// HEAD at a branch named like an option of git diff
// (--output=<file>) in a repository with no main or master: it is not
// taken for the base, nor given to git as an option, and no file is
// written.
func TestBranchStatsWithADashBranch(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	w := wts[0]
	kept := filepath.Join(t.TempDir(), "kept")
	if err := os.WriteFile(kept, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := "refs/heads/--output=" + kept
	run(t, a.Path, "git", "branch", "-m", "main", "trunk")
	run(t, w.Path, "git", "update-ref", ref, "HEAD")
	run(t, w.Path, "git", "symbolic-ref", "HEAD", ref)

	if b := defaultBase(w.Path); b != "" {
		t.Errorf("defaultBase = %q", b)
	}
	if s, err := BranchStats(w); err == nil {
		t.Errorf("BranchStats found a base: %+v", s)
	}
	if b, err := os.ReadFile(kept); err != nil || string(b) != "precious\n" {
		t.Fatalf("%s was written: %q, %v", kept, b, err)
	}

	// The same name, given as a range all the same, is a ref.
	if n := revCount(w.Path, "--output="+kept+"..HEAD"); n != 0 {
		t.Errorf("revCount = %d", n)
	}
}
