package gitsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantWorktreeConfig does what a sandboxed agent that may write its own
// folder under .git/worktrees can: give the worktree a config.worktree
// that has git run a command, through diff.external and a clean filter,
// that leaves marker behind. The main checkout has the extension on, as
// "git sparse-checkout" turns it on.
func plantWorktreeConfig(t *testing.T, root, repo, wt string) (marker string) {
	t.Helper()
	marker = filepath.Join(root, "PWNED")
	if err := os.WriteFile(filepath.Join(wt, "f"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "add", "f")
	run(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "f")
	run(t, repo, "config", "extensions.worktreeConfig", "true")
	gitDir := run(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")
	cfg := "[core]\n\tsparseCheckout = false\n" +
		"[diff]\n\texternal = touch " + marker + ";\n" +
		"[filter \"x\"]\n\tclean = touch " + marker + "; cat\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config.worktree"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "f"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

// lookAt runs what nerdr runs in a tab on every refresh.
func lookAt(dir string) {
	_ = Command(dir, "status", "--porcelain").Run()
	_ = Command(dir, "diff", "HEAD").Run()
}

func resetWorktreeConfig(t *testing.T) {
	t.Cleanup(func() {
		wcMu.Lock()
		defer wcMu.Unlock()
		sandboxedPaths = map[string]bool{}
		refusals = map[string]string{}
		reported = map[string]string{}
	})
}

func TestWorktreeConfigOfASandboxedRepositoryDoesNotRun(t *testing.T) {
	resetWorktreeConfig(t)
	root, repo, wt := repoWithWorktree(t)
	marker := plantWorktreeConfig(t, root, repo, wt)

	// Pinning GIT_DIR alone does not stop it: git reads config.worktree
	// from the pinned folder.
	Trust(repo, wt)
	if out, err := Command(wt, "status").CombinedOutput(); err != nil {
		t.Fatalf("git did not run with no sandboxed agent: %v\n%s", err, out)
	}
	lookAt(wt)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the attack did not work without the guard, so the test shows nothing")
	}
	os.Remove(marker)

	Sandboxed(wt)
	lookAt(wt)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran the command from the worktree's config.worktree")
	}
	why := Refusals()[repo]
	if !strings.Contains(why, "extensions.worktreeConfig") || !strings.Contains(why, "diff.external") {
		t.Fatalf("refusal = %q, want it to name the extension and the setting", why)
	}
	if again := Refusals(); len(again) != 0 {
		t.Fatalf("the same refusal was handed out twice: %v", again)
	}
	if _, _, ok := Dirs(wt); !ok {
		t.Fatal("Dirs lost the worktree; it is only git that must not run there")
	}

	// The main checkout and the other worktrees of the repository are
	// refused too: the agent may be able to write their config.worktree.
	other := filepath.Join(root, "other")
	run(t, repo, "worktree", "add", "-q", "-b", "other", other)
	Trust(repo, other)
	if err := Command(other, "status").Run(); err == nil {
		t.Fatal("git ran in another worktree of the sandboxed repository")
	}
	if err := Command(repo, "status").Run(); err == nil {
		t.Fatal("git ran in the main checkout of the sandboxed repository")
	}
	if again := Refusals(); len(again) != 0 {
		t.Fatalf("one repository gave more than one notice: %v", again)
	}

	// With the extension off, git does not read config.worktree, and nerdr
	// runs git again.
	run(t, repo, "config", "--unset", "extensions.worktreeConfig")
	if out, err := Command(wt, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("git did not run with the extension off: %v\n%s", err, out)
	}
	lookAt(wt)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran the command with the extension off")
	}
}

func TestWorktreeConfigLeftAloneWithoutSandbox(t *testing.T) {
	resetWorktreeConfig(t)
	root, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	run(t, wt, "sparse-checkout", "set", "--cone", "x")
	// A repository no sandboxed agent has run in is as git makes it.
	if out, err := Command(wt, "status").CombinedOutput(); err != nil {
		t.Fatalf("git did not run in a sparse worktree: %v\n%s", err, out)
	}
	// A sandboxed agent in another repository changes nothing here.
	_, repo2, wt2 := repoWithWorktree(t)
	Trust(repo2, wt2)
	Sandboxed(wt2)
	if out, err := Command(wt, "status").CombinedOutput(); err != nil {
		t.Fatalf("git did not run: %v\n%s", err, out)
	}
	// In the sandboxed one, with the extension off, git runs.
	if out, err := Command(wt2, "status").CombinedOutput(); err != nil {
		t.Fatalf("git did not run with the extension off: %v\n%s", err, out)
	}
	if r := Refusals(); len(r) != 0 {
		t.Fatalf("refusals = %v, want none", r)
	}
	_ = root
}

func TestSparseOnlyWorktreeConfigIsNamed(t *testing.T) {
	resetWorktreeConfig(t)
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	run(t, wt, "sparse-checkout", "set", "--cone", "x")
	Sandboxed(wt)
	if err := Command(wt, "status").Run(); err == nil {
		t.Fatal("git ran in a sandboxed repository with the extension on")
	}
	why := Refusals()[repo]
	if why == "" || strings.Contains(why, " sets ") {
		t.Fatalf("refusal = %q: sparse-checkout's own settings were named as suspect", why)
	}
}
