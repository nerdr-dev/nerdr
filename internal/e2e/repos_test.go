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

// TestReposAddAndRemove adds a repository to a tab made with one, which
// moves its worktree into the tab's folder and starts its agent again, and
// takes it away again, which deletes its worktree and keeps its branch.
func TestReposAddAndRemove(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	agentLog := filepath.Join(scratch, "agent.log")
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
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+agentLog, "TERM=xterm-256color")
	h := start(t, bin, root, env)

	h.waitFor("Repositories in", 10*time.Second)
	h.keys("\r") // repoA
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-r\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)
	dir := filepath.Join(root, "worktrees", "feat-r-repoA")
	h.keys("work\r")
	h.waitFor("done", 5*time.Second)
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("the agent wrote nothing: %v", err)
	}

	// Add repoB.
	h.keys(prefix)
	h.keys("i")
	h.keys("r")
	h.waitFor("Repositories of feat-r", 2*time.Second)
	t.Logf("the dialog:\n%s", h.screen())
	h.keys("\x1b[B")
	h.keys(" ")
	h.waitFor("repoB  added", time.Second)
	h.keys("\r")
	h.waitFor("repoB: new branch from", 10*time.Second)
	t.Logf("asked:\n%s", h.screen())
	h.keys("y")
	h.waitGone("Repositories of feat-r", 2*time.Second)
	waitFile(t, filepath.Join(dir, "repoB", "README.md"), true)
	for _, f := range []string{"repoA/new.txt", "repoA/README.md", "repoB/README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	h.waitFor("Added repoB", 10*time.Second) // the agent is told
	t.Logf("added:\n%s", h.screen())
	if n := strings.Count(readFile(agentLog), "\n"); n < 2 {
		t.Fatalf("the agent was not started again:\n%s", readFile(agentLog))
	}

	// Take repoB away again.
	h.keys(prefix)
	h.keys("i")
	h.keys("r")
	h.waitFor("Repositories of feat-r", 2*time.Second)
	h.keys("\x1b[B")
	h.keys(" ")
	h.waitFor("repoB  removed", time.Second)
	h.keys("\r")
	h.waitFor("Deletes the worktree", 10*time.Second)
	t.Logf("asked:\n%s", h.screen())
	h.keys("y")
	h.waitGone("Repositories of feat-r", 2*time.Second)
	waitFile(t, filepath.Join(dir, "repoB"), false)
	out, _ := exec.Command("git", "-C", filepath.Join(root, "repoB"), "branch", "--list", "feat-r").Output()
	if !strings.Contains(string(out), "feat-r") {
		t.Fatal("repoB's branch was deleted")
	}
	// The agent is told, once it has taken up the note before.
	h.waitFor("Removed repoB", 20*time.Second)
	t.Logf("removed:\n%s", h.screen())
}

// waitFile waits for path to be there, or with there false, to be gone.
func waitFile(t *testing.T, path string, there bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := os.Stat(path)
		if (err == nil) == there {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: there %v, want %v", path, err == nil, there)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
