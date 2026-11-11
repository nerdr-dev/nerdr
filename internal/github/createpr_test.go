package github

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCreatePRRunsNothingInANestedRepository: gh pr create runs a git
// status of its own to count the uncommitted changes. With a folder of the
// worktree made a repository of its own by an agent, a filter in its
// config, a gitlink for it in the index and a .gitmodules that has git
// look inside, that status would run the filter as the user. CreatePR
// gives gh an index with no gitlink in it, so it does not.
func TestCreatePRRunsNothingInANestedRepository(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	git := func(in string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", in, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(dir, "init", "-q", "-b", "main")
	write(filepath.Join(dir, "f"), "f\n")
	git(dir, "add", "f")
	git(dir, "commit", "-q", "-m", "base")

	sub := filepath.Join(dir, "sub")
	write(filepath.Join(sub, "x"), "data\n")
	git(sub, "init", "-q", "-b", "main")
	git(sub, "add", "x")
	git(sub, "commit", "-q", "-m", "sub")
	git(dir, "add", "sub")
	git(sub, "config", "filter.x.clean", "sh -c 'touch "+marker+"; cat'")
	write(filepath.Join(sub, ".git", "info", "attributes"), "* filter=x\n")
	write(filepath.Join(dir, ".gitmodules"), "[submodule \"sub\"]\n\tpath = sub\n\turl = ./sub\n\tignore = none\n")

	// A gh that does what gh pr create does before it asks GitHub.
	bin := t.TempDir()
	script := "#!/bin/sh\ngit status --porcelain >/dev/null\necho https://github.com/o/r/pull/1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The file's time changes, its size does not: git must hash it again
	// to tell whether it changed, through the filter.
	dirty := func() {
		write(filepath.Join(sub, "x"), "datb\n")
		_ = os.Remove(marker)
	}
	dirty()
	if _, err := gh(dir, "pr", "create"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a plain status by gh did not run the filter: this test proves nothing")
	}
	dirty()
	url, err := CreatePR(dir, "https://github.com/o/r.git")
	if err != nil || url != "https://github.com/o/r/pull/1" {
		t.Fatalf("CreatePR = %q, %v", url, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gh's status ran the filter of the nested repository")
	}
}
