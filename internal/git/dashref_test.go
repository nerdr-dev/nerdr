package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// dashRepo is a repository with a commit and refs whose names an agent
// may give the form of an option: refs/heads/--output=<file> and
// origin's refs/remotes/origin/--output=<file>, which origin/HEAD
// points at. kept is a file that must not be truncated, fresh one that
// must not be made.
func dashRepo(t *testing.T) (repo, kept, fresh string) {
	t.Helper()
	repo = initRepoWithCommit(t, map[string]string{"a.txt": "a\n"})
	out := t.TempDir()
	kept = filepath.Join(out, "kept")
	fresh = filepath.Join(out, "fresh")
	if err := os.WriteFile(kept, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	for _, f := range []string{kept, fresh} {
		git("update-ref", "refs/heads/--output="+f, "HEAD")
		git("update-ref", "refs/remotes/origin/--output="+f, "HEAD")
	}
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/--output="+kept)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, kept, fresh
}

func checkUntouched(t *testing.T, kept, fresh string) {
	t.Helper()
	if b, err := os.ReadFile(kept); err != nil || string(b) != "precious\n" {
		t.Errorf("%s was written: %q, %v", kept, b, err)
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Errorf("%s was made", fresh)
	}
}

// TestDashRefIsNoBase covers origin/HEAD and a branch's nerdrBase naming
// a ref that starts with a dash: neither is taken for the base.
func TestDashRefIsNoBase(t *testing.T) {
	repo, kept, fresh := dashRepo(t)
	if b := DefaultBranch(repo); b != "" {
		t.Errorf("DefaultBranch = %q", b)
	}
	if b := DetectBase(repo); !SafeRefName(b) {
		t.Errorf("DetectBase = %q", b)
	}
	branch := CurrentBranch(repo)
	if err := SetStackedBase(repo, branch, "--output="+fresh); err != nil {
		t.Fatal(err)
	}
	if b := StackedBase(repo); b != "" {
		t.Errorf("StackedBase = %q", b)
	}
	if b := DetectBase(repo); !SafeRefName(b) {
		t.Errorf("DetectBase with the stacked base = %q", b)
	}
	checkUntouched(t, kept, fresh)
}

// TestDashRefIsARefNotAnOption covers such a ref given as the base all
// the same: every call takes it for the ref it is, and writes no file.
func TestDashRefIsARefNotAnOption(t *testing.T) {
	repo, kept, fresh := dashRepo(t)
	for _, f := range []string{kept, fresh} {
		base := "--output=" + f
		c := Comparison{Worktree: repo, Base: base}
		files, err := c.Files()
		if err != nil || len(files) != 1 || files[0].Path != "a.txt" {
			t.Errorf("Files: %v, %v", files, err)
		}
		if d, err := c.Diff("a.txt", false); err != nil || d == "" {
			t.Errorf("Diff: %q, %v", d, err)
		}
		if n, add, del := c.ShortStat(); n != 1 || add != 1 || del != 1 {
			t.Errorf("ShortStat: %d +%d -%d", n, add, del)
		}
		if s := c.NumStat(); s["a.txt"] != (LineCount{Add: 1, Del: 1}) {
			t.Errorf("NumStat: %v", s)
		}
		if ref := c.OldRef(); ref == base {
			t.Errorf("MergeBase found none for %s", base)
		}
		if src, err := ReadRefFile(repo, base, "a.txt"); err != nil || src != "a\n" {
			t.Errorf("ReadRefFile: %q, %v", src, err)
		}
		if tree := TreeOf(repo, base); tree == "" {
			t.Error("TreeOf found no tree")
		}
	}
	checkUntouched(t, kept, fresh)
}
