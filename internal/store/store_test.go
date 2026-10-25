package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "myrepo")
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

// TestDirFollowsTheSetting covers the three places notes can be kept.
func TestDirFollowsTheSetting(t *testing.T) {
	repo := gitRepo(t)
	t.Cleanup(func() { SetDataDir("") })

	SetDataDir("")
	if got, want := Dir(repo), filepath.Join(repo, ".git", "nerdr"); got != want {
		t.Errorf("default: %s, want %s", got, want)
	}
	SetDataDir(".nerdr")
	if got, want := Dir(repo), filepath.Join(repo, ".nerdr"); got != want {
		t.Errorf("relative: %s, want %s", got, want)
	}
	SetDataDir("/srv/notes")
	if got, want := Dir(repo), "/srv/notes/myrepo"; got != want {
		t.Errorf("absolute: %s, want %s (named after the repository)", got, want)
	}
	home, _ := os.UserHomeDir()
	SetDataDir("~/notes")
	if got, want := Dir(repo), filepath.Join(home, "notes", "myrepo"); got != want {
		t.Errorf("~: %s, want %s", got, want)
	}
}

// TestExcludeIsWrittenOnce covers keeping nerdr's files out of git without
// piling up the same line on every start.
func TestExcludeIsWrittenOnce(t *testing.T) {
	repo := gitRepo(t)
	for range 3 {
		if err := Exclude(repo, "/.nerdr/"); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if n := strings.Count(string(data), "/.nerdr/\n"); n != 1 {
		t.Fatalf("the pattern is there %d times:\n%s", n, data)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".nerdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, ".nerdr", "why.jsonl"), []byte("{}\n"), 0o644)
	if out, _ := exec.Command("git", "-C", repo, "status", "--porcelain").Output(); len(out) != 0 {
		t.Fatalf("git still sees it: %s", out)
	}
}

// TestBranchCreatedOnlyFromTheFirstLine covers telling when a branch was
// made: only a reflog that still starts at the branch's creation says.
func TestBranchCreatedOnlyFromTheFirstLine(t *testing.T) {
	repo := gitRepo(t)
	for _, args := range [][]string{
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"branch", "feature/x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if c, ok := BranchCreated(repo, "feature/x"); !ok || time.Since(c) > time.Minute {
		t.Fatalf("created %v ok=%v", c, ok)
	}
	if _, ok := BranchCreated(repo, "nope"); ok {
		t.Fatal("a branch with no reflog has no known creation")
	}
	if _, ok := BranchCreated(repo, "../../HEAD"); ok {
		t.Fatal("a name outside refs/heads must not be read")
	}

	// Its first lines expired: the oldest line left is not the creation.
	log := filepath.Join(repo, ".git", "logs", "refs", "heads", "feature", "x")
	data, _ := os.ReadFile(log)
	expired := strings.Replace(string(data), strings.Repeat("0", 40), strings.Repeat("1", 40), 1)
	if err := os.WriteFile(log, []byte(expired), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := BranchCreated(repo, "feature/x"); ok {
		t.Fatal("an expired reflog must not pass for the creation")
	}
}

// A line too long to be a record is passed over, and the records after
// it are still read.
func TestReadLinesPassesOverALineTooLong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	data := "first\n" + strings.Repeat("x", maxLine+10) + "\nlast\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	ReadLines(path, func(b []byte) { got = append(got, string(b)) })
	if len(got) != 2 || got[0] != "first" || got[1] != "last" {
		t.Errorf("read %d lines, %.20q", len(got), got)
	}
}
