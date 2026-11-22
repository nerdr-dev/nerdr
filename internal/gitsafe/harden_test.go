package gitsafe

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// within fails t when f takes longer than a few seconds: what it reads
// was a named pipe nerdr would have waited on forever.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s hung", what)
	}
}

// plantInstead replaces the file at path with what an agent that may
// write the folder it is in could put there: a named pipe, a link to
// /dev/zero, a link out of the repository, or a file too big to hold.
func plantInstead(t *testing.T, path, kind, outside string) {
	t.Helper()
	_ = os.Remove(path)
	var err error
	switch kind {
	case "fifo":
		err = syscall.Mkfifo(path, 0o644)
	case "zero":
		err = os.Symlink("/dev/zero", path)
	case "outside":
		err = os.Symlink(outside, path)
	case "huge":
		err = os.WriteFile(path, []byte(strings.Repeat("x", maxAdminFile+1)), 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
}

var plantKinds = []string{"fifo", "zero", "outside", "huge"}

// A worktree's gitdir file, under .git/worktrees, is the agent's to
// write: neither pointsAt nor worktreesOf may hang on it, read it whole
// or follow a link out of the repository to find a worktree there.
func TestGitdirIsReadSafely(t *testing.T) {
	root, repo, wt := repoWithWorktree(t)
	common := filepath.Join(repo, ".git")
	admin := filepath.Join(common, "worktrees", "wt")
	if !pointsAt(admin, wt) {
		t.Fatal("the real gitdir does not point at the worktree")
	}
	// What the outside link names would point at the worktree, were it
	// followed.
	outside := filepath.Join(root, "gitdir")
	if err := os.WriteFile(outside, []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range plantKinds {
		plantInstead(t, filepath.Join(admin, "gitdir"), kind, outside)
		within(t, kind, func() {
			if pointsAt(admin, wt) {
				t.Errorf("%s: pointsAt took it as the worktree's", kind)
			}
			for _, r := range worktreesOf(repo, common) {
				if r == wt {
					t.Errorf("%s: worktreesOf read it", kind)
				}
			}
		})
	}

	// The worktree's folder under .git/worktrees made a link is not
	// followed either.
	elsewhere := filepath.Join(root, "admin")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(admin); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, admin); err != nil {
		t.Fatal(err)
	}
	if pointsAt(admin, wt) {
		t.Error("pointsAt followed a link to the worktree's folder")
	}
	for _, r := range worktreesOf(repo, common) {
		if r == wt {
			t.Error("worktreesOf followed a link to the worktree's folder")
		}
	}
}

// ReadGitFile, which reads a worktree's HEAD for workspace, refuses the
// same.
func TestReadGitFileRefusesWhatIsNotASmallFile(t *testing.T) {
	_, repo, _ := repoWithWorktree(t)
	admin := filepath.Join(repo, ".git", "worktrees", "wt")
	if b, err := ReadGitFile(admin, "HEAD"); err != nil || !strings.HasPrefix(string(b), "ref: refs/heads/wt") {
		t.Fatalf("HEAD: %q %v", b, err)
	}
	outside := filepath.Join(t.TempDir(), "HEAD")
	if err := os.WriteFile(outside, []byte("ref: refs/heads/evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range plantKinds {
		plantInstead(t, filepath.Join(admin, "HEAD"), kind, outside)
		within(t, kind, func() {
			if b, err := ReadGitFile(admin, "HEAD"); err == nil {
				t.Errorf("%s: read %d bytes", kind, len(b))
			}
		})
	}
}

// config.worktree is the agent's to write too: worktreeKeys reads it
// itself and gives git what it read, so a pipe, a link or a huge file is
// refused at once, and refusal names it rather than passing over it.
func TestWorktreeKeysRefusesWhatIsNotASmallFile(t *testing.T) {
	root, repo, _ := repoWithWorktree(t)
	common := filepath.Join(repo, ".git")
	f := filepath.Join(common, "worktrees", "wt", "config.worktree")
	if err := os.WriteFile(f, []byte("[diff]\n\texternal = x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if keys, ok := worktreeKeys(f); !ok || len(keys) != 1 || keys[0] != "diff.external" {
		t.Fatalf("keys %v, %v", keys, ok)
	}
	outside := filepath.Join(root, "outside.config")
	if err := os.WriteFile(outside, []byte("[diff]\n\texternal = y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range plantKinds {
		plantInstead(t, f, kind, outside)
		within(t, kind, func() {
			if keys, ok := worktreeKeys(f); ok {
				t.Errorf("%s: keys %v", kind, keys)
			}
			if r := refusal(common); !strings.Contains(r, f+" is not a small regular file") {
				t.Errorf("%s: refusal does not name it: %s", kind, r)
			}
		})
	}
}

// A git config that does not finish is killed after configTimeout: the
// core is not held up by it.
func TestConfigCommandTimesOut(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := configTimeout
	configTimeout = 200 * time.Millisecond
	t.Cleanup(func() { configTimeout = old })
	common := t.TempDir()
	f := filepath.Join(common, "config.worktree")
	if err := os.WriteFile(f, []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	within(t, "worktreeKeys", func() {
		if _, ok := worktreeKeys(f); ok {
			t.Error("a git that never finished gave keys")
		}
	})
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}
