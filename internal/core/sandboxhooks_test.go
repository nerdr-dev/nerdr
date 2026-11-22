package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// bareGit makes a main checkout whose .git has a config and no hooks, as
// git init --template= leaves it.
func bareGit(t *testing.T) (repo, config, hooks string) {
	t.Helper()
	repo = t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	config, hooks = filepath.Join(repo, ".git", "config"), filepath.Join(repo, ".git", "hooks")
	if err := os.WriteFile(config, []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, config, hooks
}

func tabIn(repo string) *Tab {
	tb := &Tab{}
	tb.Worktrees = []workspace.Worktree{{Repo: repo, Path: repo}}
	return tb
}

// A main checkout without .git/hooks has it made before the check, so
// that a sandbox's rule for it has something to hold, and the check
// tries it as it tries one that was there; .git/config is left as it was.
func TestSandboxWritesMakeMissingHooks(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, config, hooks := bareGit(t)
	before, _ := os.Lstat(config)
	writes := (&Core{}).sandboxWrites(tabIn(repo))
	if !slices.Contains(writes, hooks) || !slices.Contains(writes, config) {
		t.Fatalf("%v does not have .git/hooks and .git/config", writes)
	}
	if st, err := os.Lstat(hooks); err != nil || !st.IsDir() {
		t.Fatalf(".git/hooks was not made: %v %v", st, err)
	}
	// With no sandbox, both can be written: the check says so.
	r := sandboxcheck.Probe(sandboxcheck.Target{Nonce: "n", Writes: writes})
	if !slices.Contains(r.Writes, hooks) || !slices.Contains(r.Writes, config) {
		t.Fatalf("writes = %q", r.Writes)
	}
	after, _ := os.Lstat(config)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal(".git/config was changed")
	}
	if left, _ := os.ReadDir(hooks); len(left) != 0 {
		t.Fatalf("the check left %v in .git/hooks", left)
	}
}

// When .git/hooks cannot be made — nerdr may not write .git, as a
// sandbox would deny it — nothing is made, and the check finds it cannot
// be made either: the agent is not refused for it.
func TestSandboxWritesWhenHooksCannotBeMade(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes what is read only")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, _, hooks := bareGit(t)
	git := filepath.Dir(hooks)
	if err := os.Chmod(git, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(git, 0o755) })
	writes := (&Core{}).sandboxWrites(tabIn(repo))
	if _, err := os.Lstat(hooks); !os.IsNotExist(err) {
		t.Fatalf(".git/hooks: %v", err)
	}
	if r := sandboxcheck.Probe(sandboxcheck.Target{Nonce: "n", Writes: writes}); slices.Contains(r.Writes, hooks) {
		t.Fatalf("a .git/hooks that cannot be made was found writable: %q", r.Writes)
	}
}

// .git/hooks is made from the main checkout without following a link: a
// .git that is a link, or a hooks that is one, has nothing made where it
// points.
func TestSandboxWritesMakeNoHooksThroughALink(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	elsewhere := t.TempDir()
	repo := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	(&Core{}).sandboxWrites(tabIn(repo))
	if left, _ := os.ReadDir(elsewhere); len(left) != 0 {
		t.Fatalf("made through a .git link: %v", left)
	}

	repo2, _, hooks := bareGit(t)
	target := filepath.Join(t.TempDir(), "made-here")
	if err := os.Symlink(target, hooks); err != nil {
		t.Fatal(err)
	}
	(&Core{}).sandboxWrites(tabIn(repo2))
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("made through a hooks link: %v", err)
	}
}
