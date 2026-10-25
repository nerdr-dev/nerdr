package gitsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWithWorktree makes a main checkout and a linked worktree of it.
func repoWithWorktree(t *testing.T) (root, repo, wt string) {
	t.Helper()
	root = t.TempDir()
	repo, wt = filepath.Join(root, "main"), filepath.Join(root, "wt")
	run(t, root, "init", "-q", "-b", "main", repo)
	run(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	run(t, repo, "worktree", "add", "-q", "-b", "wt", wt)
	return root, repo, wt
}

// redirect does what an agent that may write only its worktree can: point
// the worktree's .git file at a repository of its own, whose config has
// git run a command that leaves marker behind.
func redirect(t *testing.T, root, repo, wt string) (marker string) {
	t.Helper()
	marker = filepath.Join(root, "PWNED")
	evil := filepath.Join(wt, "evil")
	run(t, root, "clone", "-q", "--bare", repo, evil)
	run(t, root, "--git-dir="+evil, "config", "core.bare", "false")
	run(t, root, "--git-dir="+evil, "config", "core.worktree", wt)
	run(t, root, "--git-dir="+evil, "config", "core.fsmonitor", "touch "+marker+"; false")
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+evil+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestRedirectedWorktreeIsNotObeyed(t *testing.T) {
	root, repo, wt := repoWithWorktree(t)
	marker := redirect(t, root, repo, wt)

	// Without gitsafe, the agent's command runs.
	run(t, wt, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the attack did not work without gitsafe, so the test shows nothing")
	}
	os.Remove(marker)

	Trust(repo, wt)
	out, err := Command(wt, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran the command from the agent's config")
	}
	// It is the real worktree git looked at: on its own branch, in the
	// main checkout's repository.
	b, _ := Command(wt, "symbolic-ref", "--short", "HEAD").Output()
	if got := strings.TrimSpace(string(b)); got != "wt" {
		t.Fatalf("branch = %q, want wt", got)
	}
	c, _ := Command(wt, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if got := strings.TrimSpace(string(c)); !sameDir(got, filepath.Join(repo, ".git")) {
		t.Fatalf("common dir = %q, want the main checkout's", got)
	}
}

func TestUnknownWorktreeFailsClosed(t *testing.T) {
	root, repo, _ := repoWithWorktree(t)
	// A folder the agent made to look like a worktree of repo, which repo
	// knows nothing of.
	fake := filepath.Join(root, "fake")
	run(t, root, "init", "-q", fake)
	Trust(repo, fake)
	if err := Command(fake, "status").Run(); err == nil {
		t.Fatal("git ran in a worktree the repository does not know")
	}
	if Belongs(repo, fake) {
		t.Fatal("Belongs took a folder the repository does not know")
	}
}

func TestBelongsAsksTheRepository(t *testing.T) {
	root, repo, wt := repoWithWorktree(t)
	if !Belongs(repo, wt) {
		t.Fatal("Belongs refused a real worktree")
	}
	redirect(t, root, repo, wt)
	if !Belongs(repo, wt) {
		t.Fatal("Belongs went by the .git file")
	}
	other := filepath.Join(root, "other")
	run(t, root, "init", "-q", other)
	if Belongs(other, wt) {
		t.Fatal("Belongs gave the worktree to another repository")
	}
}

func TestHooksOnlyWhenAsked(t *testing.T) {
	root, repo, _ := repoWithWorktree(t)
	marker := filepath.Join(root, "HOOK")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := Command(repo, "worktree", "add", "-q", "-b", "a", filepath.Join(root, "a")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook ran without Hooks")
	}
	cmd := Command(repo, "worktree", "add", "-q", "-b", "b", filepath.Join(root, "b"))
	cmd.Env = Hooks(repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the hook did not run with Hooks")
	}
}

func TestKeepsTheUsersConfigEnv(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "Someone")
	_, repo, _ := repoWithWorktree(t)
	out, err := Command(repo, "config", "user.name").Output()
	if err != nil || strings.TrimSpace(string(out)) != "Someone" {
		t.Fatalf("user.name = %q, %v", out, err)
	}
	out, _ = Command(repo, "config", "core.fsmonitor").Output()
	if strings.TrimSpace(string(out)) != "false" {
		t.Fatalf("core.fsmonitor = %q", out)
	}
}

// TestAskpassAndPromptAreNeutralized: a repository's askpass program must
// never run (nerdr runs git off its terminal), and git must never wait on
// a password nobody can type.
func TestAskpassAndPromptAreNeutralized(t *testing.T) {
	_, repo, _ := repoWithWorktree(t)
	run(t, repo, "config", "core.askpass", "touch /tmp/nerdr-askpass-pwned")
	out, err := Command(repo, "config", "core.askpass").Output()
	if err != nil {
		t.Fatalf("core.askpass: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != os.DevNull {
		t.Fatalf("core.askpass = %q, want %q (a repository's askpass must not run)", got, os.DevNull)
	}
	if !contains(Env(repo), "GIT_TERMINAL_PROMPT=0") {
		t.Fatal("GIT_TERMINAL_PROMPT=0 is not in the git environment")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestMainCheckoutByAnotherName: the main checkout named through a link,
// as git names /var/… on macOS (/private/var/…), is still the main
// checkout, not a worktree the repository does not know.
func TestMainCheckoutByAnotherName(t *testing.T) {
	root, repo, _ := repoWithWorktree(t)
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skip(err)
	}
	Trust(link, repo)
	if out, err := Command(repo, "status").CombinedOutput(); err != nil {
		t.Fatalf("git did not run in the main checkout: %v\n%s", err, out)
	}
}
