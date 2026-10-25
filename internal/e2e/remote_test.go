//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBranchOnOrigin covers typing a branch name origin already has: the
// dialog says so and whether it is merged, and enter makes a new branch
// from main rather than checking out origin's.
func TestBranchOnOrigin(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	repo := filepath.Join(root, "repoA")
	git(t, repo, "checkout", "-q", "-b", "old")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "old work")
	git(t, repo, "push", "-q", "origin", "old")
	git(t, repo, "checkout", "-q", "main")
	git(t, repo, "branch", "-q", "-D", "old")
	git(t, repo, "fetch", "-q", "origin")

	h := start(t, bin, root, branchEnv(scratch))
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" \r")
	h.keys("old\r")
	h.waitFor("origin/old already exists", 5*time.Second)
	h.waitFor("1 commit not in origin/main", time.Second)
	h.waitFor("Continue origin/old", time.Second)
	h.keys("\r")
	h.waitFor("repoA: new branch from origin/main", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	wt := filepath.Join(root, "worktrees", "old-repoA")
	head, _ := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
	main, _ := exec.Command("git", "-C", repo, "rev-parse", "origin/main").Output()
	if string(head) != string(main) {
		t.Fatalf("new branch at %s, want origin/main at %s", head, main)
	}
}

// branchEnv is the environment for these tests: the fake agent, and
// nerdr's config and state in scratch.
func branchEnv(scratch string) []string {
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") {
			env = append(env, kv)
		}
	}
	return append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "a.log"), "TERM=xterm-256color")
}

// TestLocalBranch covers typing a branch name there is a local branch of
// already, left behind while main moved on. Merged, the dialog says so and
// enter makes a new branch from origin's main in its place; with work of
// its own, enter continues it.
func TestLocalBranch(t *testing.T) {
	bin := build(t)
	for _, c := range []struct {
		name   string
		commit bool
		see    []string
	}{
		{"merged", false, []string{"Branch old already exists here", "1 commit behind origin/main",
			"New branch old from origin/main", "repoA: new branch from origin/main, in place of the merged local one"}},
		{"unmerged", true, []string{"Branch old already exists here", "1 commit not in origin/main",
			"would throw away", "repoA: existing branch old"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, scratch := setup(t)
			repo := filepath.Join(root, "repoA")
			git(t, repo, "branch", "-q", "old")
			if c.commit {
				git(t, repo, "checkout", "-q", "old")
				git(t, repo, "commit", "-q", "--allow-empty", "-m", "old work")
				git(t, repo, "checkout", "-q", "main")
			}
			git(t, repo, "commit", "-q", "--allow-empty", "-m", "main moves on")
			git(t, repo, "push", "-q", "origin", "main")
			want, _ := exec.Command("git", "-C", repo, "rev-parse", "old").Output()
			if !c.commit {
				want, _ = exec.Command("git", "-C", repo, "rev-parse", "origin/main").Output()
			}

			h := start(t, bin, root, branchEnv(scratch))
			h.waitFor("Repositories in", 10*time.Second)
			h.keys(" \r")
			h.keys("old\r")
			for i, s := range c.see {
				if i == len(c.see)-1 {
					h.keys("\r")
				}
				h.waitFor(s, 5*time.Second)
			}
			h.keys("\r")
			h.waitFor("? for shortcuts", 10*time.Second)

			wt := filepath.Join(root, "worktrees", "old-repoA")
			head, _ := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
			if string(head) != string(want) {
				t.Fatalf("worktree at %s, want %s", head, want)
			}
		})
	}
}
