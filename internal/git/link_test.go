package git

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A link in the worktree is shown as git keeps it, where it points, and
// what it points to is not read: nerdr reads outside any sandbox the agent
// is in, and what it shows goes in walkthrough pages too.
func TestWorktreeLinksAreNotFollowed(t *testing.T) {
	dir := initRepoWithCommit(t, map[string]string{"a.txt": "a\n"})
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("secret-only-outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	diff, _ := NewFileContent(dir, "link")
	if strings.Contains(diff, "secret-only-outside") || !strings.Contains(diff, "+"+secret) || !strings.Contains(diff, "120000") {
		t.Errorf("link shown as:\n%s", diff)
	}
	if got, err := ReadWorktreeFile(dir, "out/secret"); err == nil {
		t.Errorf("read through a folder out of the worktree: %q", got)
	}
	if n, _ := fileLines(dir, "link"); n != 1 {
		t.Errorf("a link counts as %d lines, want 1", n)
	}
}

// A FIFO an agent left is not opened: it would wait for ever.
func TestWorktreeFIFOIsNotRead(t *testing.T) {
	dir := initRepoWithCommit(t, map[string]string{"a.txt": "a\n"})
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skip("no FIFO here:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadWorktreeFile(dir, "pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read as a file")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO waited")
	}
}

// A file whose name looks like a pattern has its own diff, not the
// diff of every file the pattern matches.
func TestDiffTakesTheNameAsItIs(t *testing.T) {
	dir := initRepoWithCommit(t, map[string]string{"*.txt": "a\n", "other.txt": "b\n"})
	for _, f := range []string{"*.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	diff, err := Diff(dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "other.txt") || !strings.Contains(diff, "a/*.txt") {
		t.Errorf("diff of *.txt:\n%s", diff)
	}
}
