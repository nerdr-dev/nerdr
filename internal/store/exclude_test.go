package store

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestExcludeCreatesInfoAndKeepsExistingLines covers making info/exclude
// when it is missing, and adding to one that has lines (without a final
// newline) of its own.
func TestExcludeCreatesInfoAndKeepsExistingLines(t *testing.T) {
	repo := gitRepo(t)
	info := filepath.Join(repo, ".git", "info")
	if err := os.RemoveAll(info); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(repo, "/.nerdr/"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(info, "exclude"))
	if err != nil || !strings.HasSuffix(string(data), "/.nerdr/\n") {
		t.Fatalf("exclude = %q, %v", data, err)
	}

	file := filepath.Join(info, "exclude")
	if err := os.WriteFile(file, []byte("*.log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(repo, "/.nerdr/"); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(repo, "/.nerdr/"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(file)
	if want := "*.log\n# kept out of the change by nerdr\n/.nerdr/\n"; string(data) != want {
		t.Fatalf("exclude = %q, want %q", data, want)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the file's own 0600 kept", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(info, ".nerdr-tmp-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// TestExcludeRefusesALinkedExcludeFile covers an agent replacing
// info/exclude with a link to a file it may not write, such as git's
// config: nerdr must not append to the file the link names.
func TestExcludeRefusesALinkedExcludeFile(t *testing.T) {
	repo := gitRepo(t)
	target := filepath.Join(t.TempDir(), "protected")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{target, filepath.Join(repo, ".git", "config")} {
		before, _ := os.ReadFile(to)
		file := filepath.Join(repo, ".git", "info", "exclude")
		_ = os.Remove(file)
		if err := os.Symlink(to, file); err != nil {
			t.Fatal(err)
		}
		if err := Exclude(repo, "/.nerdr/"); err == nil {
			t.Errorf("a link to %s was accepted", to)
		}
		if after, _ := os.ReadFile(to); string(after) != string(before) {
			t.Errorf("%s was changed through the link:\n%s", to, after)
		}
	}
}

// TestExcludeRefusesALinkedInfoFolder covers info itself replaced with a
// link, out of the repository and onto the git directory.
func TestExcludeRefusesALinkedInfoFolder(t *testing.T) {
	repo := gitRepo(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "exclude"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(repo, ".git")
	for _, to := range []string{outside, gitDir} {
		info := filepath.Join(gitDir, "info")
		if err := os.RemoveAll(info); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(to, info); err != nil {
			t.Fatal(err)
		}
		if err := Exclude(repo, "/.nerdr/"); err == nil {
			t.Errorf("info linked to %s was accepted", to)
		}
		_ = os.Remove(info)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "exclude")); string(data) != "keep\n" {
		t.Errorf("the file outside was changed: %q", data)
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "exclude")); err == nil {
		t.Errorf("an exclude file was written into the git directory itself")
	}
}

// TestExcludeRefusesANamedPipe covers info/exclude replaced with a named
// pipe, or a link to /dev/zero: Exclude must neither hang nor read
// without end.
func TestExcludeRefusesANamedPipe(t *testing.T) {
	repo := gitRepo(t)
	file := filepath.Join(repo, ".git", "info", "exclude")
	_ = os.Remove(file)
	if err := syscall.Mkfifo(file, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- Exclude(repo, "/.nerdr/") }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a named pipe was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exclude blocked on a named pipe")
	}

	_ = os.Remove(file)
	if err := os.Symlink("/dev/zero", file); err != nil {
		t.Fatal(err)
	}
	go func() { done <- Exclude(repo, "/.nerdr/") }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a link to /dev/zero was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exclude read /dev/zero")
	}
}

// TestExcludeRefusesAHugeFile covers the bound on what is read.
func TestExcludeRefusesAHugeFile(t *testing.T) {
	repo := gitRepo(t)
	file := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.Truncate(file, maxExcludeBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(repo, "/.nerdr/"); err == nil {
		t.Error("a file over the limit was accepted")
	}
}
