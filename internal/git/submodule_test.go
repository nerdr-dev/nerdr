package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// nestedRepoWorktree does what an agent that may write only its worktree
// can: make a folder of the worktree a repository of its own, whose config
// has a filter for every file that leaves marker behind, put it in the
// worktree's index as a gitlink, and write a .gitmodules that asks for the
// submodule's dirty content to be looked at. Any git that runs status
// inside the folder runs the filter.
func nestedRepoWorktree(t *testing.T) (repo, wt, sub, marker string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo, wt = filepath.Join(root, "main"), filepath.Join(root, "wt")
	marker = filepath.Join(root, "PWNED")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(root, "init", "-q", "-b", "main", repo)
	write(filepath.Join(repo, "f"), "a\n")
	git(repo, "add", "f")
	git(repo, "commit", "-q", "-m", "init")
	git(repo, "worktree", "add", "-q", "-b", "wt", wt)
	gitsafe.Trust(repo, wt)

	sub = filepath.Join(wt, "sub")
	git(wt, "init", "-q", sub)
	write(filepath.Join(sub, "x"), "x\n")
	git(sub, "add", "x")
	git(sub, "commit", "-q", "-m", "sub")
	git(sub, "config", "filter.x.clean", "touch "+marker+"; cat")
	write(filepath.Join(sub, ".git", "info", "attributes"), "* filter=x\n")
	git(wt, "update-index", "--add", "--cacheinfo", "160000,"+git(sub, "rev-parse", "HEAD")+",sub")
	write(filepath.Join(wt, ".gitmodules"), "[submodule \"sub\"]\n\tpath = sub\n\turl = ./sub\n\tignore = none\n")
	git(wt, "add", ".gitmodules")
	git(wt, "commit", "-q", "-m", "sub")
	write(filepath.Join(wt, "f"), "b\n")
	return repo, wt, sub, marker
}

// dirty makes the nested repository's file look changed to it, so that a
// status inside it reads the file again, through the filter.
func dirty(t *testing.T, sub, marker string) {
	t.Helper()
	os.Remove(marker)
	later := time.Now().Add(time.Duration(time.Now().UnixNano()%1000+1) * time.Second)
	if err := os.Chtimes(filepath.Join(sub, "x"), later, later); err != nil {
		t.Fatal(err)
	}
}

func TestNestedRepositoryRunsNothing(t *testing.T) {
	_, wt, sub, marker := nestedRepoWorktree(t)

	// Without nerdr's guards, the agent's filter runs.
	dirty(t, sub, marker)
	plain := exec.Command("git", "-C", wt, "status", "--porcelain")
	plain.Env = gitsafe.Env(wt)
	if out, err := plain.CombinedOutput(); err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the filter did not run without the guards, so the test shows nothing")
	}

	steps := []struct {
		name string
		do   func() error
	}{
		{"ChangedFiles", func() error { _, err := ChangedFiles(wt); return err }},
		{"Diff", func() error { _, err := Diff(wt, "f"); return err }},
		{"Diff of the gitlink", func() error { _, err := Diff(wt, "sub"); return err }},
		{"ChangedFilesBase", func() error { _, err := ChangedFilesBase(wt, "main"); return err }},
		{"DiffBase", func() error { _, err := DiffBase(wt, "main", "sub"); return err }},
		{"NumStat", func() error { NumStat(wt, "HEAD"); return nil }},
		{"ShortStat", func() error { ShortStat(wt); return nil }},
		{"WorkTree", func() error { _, err := WorkTree(wt); return err }},
		{"StageAll", func() error { return StageAll(context.Background(), wt) }},
	}
	for _, s := range steps {
		dirty(t, sub, marker)
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s ran the nested repository's filter", s.name)
		}
	}

	// StageAll staged the ordinary change and left the gitlink alone.
	out, err := gitsafe.Command(wt, "diff", "--cached", "--name-only").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "f" {
		t.Fatalf("staged %q, want f", got)
	}
}

// A submodule moved to another commit still shows as changed: only what
// is inside its folder is not looked at.
func TestMovedSubmoduleStillShows(t *testing.T) {
	_, wt, sub, _ := nestedRepoWorktree(t)
	cmd := exec.Command("git", "-C", sub, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "moved")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	files, err := ChangedFiles(wt)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range files {
		found = found || f.Path == "sub"
	}
	if !found {
		t.Fatalf("the moved submodule is not among %v", files)
	}
}

// AddAll adds what git add -A would, gitlinks aside, from a subfolder too,
// with names that are not patterns.
func TestAddAllAddsTheRest(t *testing.T) {
	_, wt, _, _ := nestedRepoWorktree(t)
	for _, name := range []string{"new *.txt", ":colon", filepath.Join("d", "deep")} {
		path := filepath.Join(wt, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("n\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(wt, ".gitmodules")); err != nil {
		t.Fatal(err)
	}
	if err := AddAll(context.Background(), filepath.Join(wt, "d")); err != nil {
		t.Fatal(err)
	}
	out, err := gitsafe.Command(wt, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "D  .gitmodules\nA  :colon\nA  d/deep\nM  f\nA  \"new *.txt\""
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("status after AddAll:\n%s\nwant:\n%s", got, want)
	}
}
