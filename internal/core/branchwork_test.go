package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
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

func TestExistingPRStillPushes(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	repo, remote := filepath.Join(root, "repo"), filepath.Join(root, "remote.git")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q", "-b", "main", repo)
	git("init", "-q", "--bare", remote)
	git("-C", repo, "commit", "-q", "--allow-empty", "-m", "base")
	git("-C", repo, "switch", "-qc", "feat")
	git("-C", repo, "remote", "add", "origin", remote)
	git("-C", repo, "push", "-q", "origin", "feat")
	git("-C", repo, "commit", "-q", "--allow-empty", "-m", "follow-up")
	gitsafe.Trust(repo, repo)
	wts := []workspace.Worktree{{Repo: repo, Path: repo, Branch: "feat", Origin: remote}}
	note, why := CreatePRs("feat", wts, map[string]bool{repo: true}, true)
	if why != "" || !strings.Contains(note, "pushed") {
		t.Fatalf("note %q, error %q", note, why)
	}
	if got, want := git("--git-dir="+remote, "rev-parse", "refs/heads/feat"), git("-C", repo, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote still at %s, want %s", got, want)
	}
	// An existing PR must not hide a failed push either.
	git("-C", repo, "remote", "set-url", "origin", filepath.Join(root, "missing.git"))
	if _, why := CreatePRs("feat", wts, map[string]bool{repo: true}, true); why == "" {
		t.Fatal("reported success after the push was refused")
	}
}
