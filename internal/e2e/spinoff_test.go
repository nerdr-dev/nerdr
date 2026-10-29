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

// TestSpinoff has an agent split its work into three parts: the first made
// under another name with its commit, the second written anew, the third
// skipped. The agent is then told what became of each.
func TestSpinoff(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
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
	env = append(env,
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"TERM=xterm-256color",
	)
	h := start(t, bin, root, env)

	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" ") // pick repoA
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("big\r")
	h.waitFor("Agent:", 3*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 15*time.Second)

	h.keys("spinoff\r")

	// Part 1 of 3: renamed, starting with its commit. The dialog covers
	// the agent's "sent 3 parts to nerdr", so it alone shows they arrived.
	h.waitFor("New tab 1 of 3", 10*time.Second)
	h.waitFor("Summary of part A", time.Second)
	h.waitFor("Start the branch with its 1 commit", time.Second)
	h.waitFor("Agent:", time.Second)
	h.keys("\x15") // ctrl+u: clear the proposed name
	h.keys("renamed-a\r")

	// Part 2 of 3: written anew.
	h.waitFor("New tab 2 of 3", 20*time.Second)
	h.waitFor("Summary of part B", time.Second)
	h.keys("\t")
	h.keys("\r")

	// Part 3 of 3: skipped.
	h.waitFor("New tab 3 of 3", 20*time.Second)
	h.keys("\x18") // ctrl+x
	h.waitGone("New tab", 2*time.Second)

	wts := filepath.Join(root, "worktrees")
	if _, err := os.Stat(filepath.Join(wts, "renamed-a-repoA", "part-a.txt")); err != nil {
		t.Fatalf("part A's commit is not on its branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wts, "renamed-a-repoA", "part-b.txt")); err == nil {
		t.Fatal("part B's commit came along with part A")
	}
	if _, err := os.Stat(filepath.Join(wts, "part-b-repoA")); err != nil {
		t.Fatalf("part B's worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wts, "part-b-repoA", "part-b.txt")); err == nil {
		t.Fatal("part B was to be written anew, but its commit is there")
	}
	if _, err := os.Stat(filepath.Join(wts, "part-c-repoA")); err == nil {
		t.Fatal("the skipped part got a worktree")
	}
	out, _ := exec.Command("git", "-C", filepath.Join(wts, "renamed-a-repoA"), "branch", "--show-current").Output()
	if strings.TrimSpace(string(out)) != "renamed-a" {
		t.Fatalf("branch %q", out)
	}
	h.waitTabBar("renamed-a", 2*time.Second)
	h.waitTabBar("part-b", 2*time.Second)

	// The agent that split its work is told, once it is idle.
	h.waitFor("nerdr: the user has gone through the parts", 15*time.Second)
	h.waitFor(`part-c:\ skipped\ by\ the\ user`, 5*time.Second)
}
