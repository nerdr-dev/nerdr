//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFeatures drives the tab features: a worktree set up from
// .nerdr.toml, once it is allowed, a first task typed in for the agent, queued messages, the
// shell, renaming and finding tabs, the details in the tab bar, pushing
// from the tab panel, the warning when two tabs change the same file, and
// one message to several tabs.
func TestFeatures(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")

	// What a new repoA worktree needs: the untracked .env, and a setup
	// command.
	repoA := filepath.Join(root, "repoA")
	_ = os.WriteFile(filepath.Join(repoA, ".env"), []byte("SECRET=1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repoA, ".nerdr.toml"),
		[]byte("copy = [\".env\"]\nsetup = \"echo ok > setup-ran\"\n"), 0o644)

	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\nshell = \"/bin/sh\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)

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
		"TERM=xterm-256color",
	)
	h := start(t, bin, root, env)

	// A tab on repoA with a first task.
	newTab := func(branch, task string, allow bool) {
		h.waitFor("Repositories in", 10*time.Second)
		h.keys(" ") // pick repoA
		h.keys("\r")
		h.waitFor("branch:", 2*time.Second)
		h.keys(branch)
		if task != "" {
			h.keys("\t")
			h.keys(task)
		}
		h.keys("\r")
		h.waitFor("Agent:", 3*time.Second)
		h.keys("\r")
		if allow {
			// The first time, .nerdr.toml is shown to be allowed.
			h.waitFor("setup: echo ok > setup-ran", 3*time.Second)
			h.keys("y")
		}
		h.waitFor("? for shortcuts", 15*time.Second)
	}
	newTab("feat-q", "hello task", true)

	wt := filepath.Join(root, "worktrees", "feat-q-repoA")
	if data, err := os.ReadFile(filepath.Join(wt, ".env")); err != nil || string(data) != "SECRET=1\n" {
		t.Fatalf(".env not copied into the worktree: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "setup-ran")); err != nil {
		t.Fatalf("setup did not run: %v", err)
	}

	// The task is typed in once the agent is idle.
	h.waitFor(`got: hello\ task`, 10*time.Second)

	// A queued message goes in the same way.
	h.keys(prefix + "m")
	h.waitFor("Task", 2*time.Second)
	h.keys("second\t") // tick automatic delivery before saving
	h.keys("\r")
	h.waitFor("got: second", 10*time.Second)

	// A terminal runs in the worktree beside the agent, and goes away
	// again.
	h.keys(prefix + "t")
	h.keys("t")
	h.waitFor("─ terminal ─", 3*time.Second)
	h.keys("echo shell-$((1+2)) $(basename $PWD)\r")
	h.waitFor("shell-3 feat-q", 3*time.Second)
	h.keys(prefix + "t")
	h.keys("x")
	h.waitGone("─ terminal ─", 3*time.Second)
	h.waitFor("got: second", time.Second)

	// Rename the tab.
	h.keys(prefix + "r")
	h.waitFor("Rename tab", 2*time.Second)
	h.keys("\x15") // ctrl+u: clear
	h.keys("renamed\r")
	h.waitTabBar("1:renamed", 2*time.Second)

	// The agent changes a file: the tab bar shows it.
	h.keys("work\r")
	h.waitFor("done", 8*time.Second)
	// .env and setup-ran are new files too: 1 line each, and new.txt.
	h.waitTabBar("+3−0", 30*time.Second)

	// The tab panel pushes the branch.
	h.keys(prefix + "i")
	h.keys("i")
	h.waitFor("this branch", 2*time.Second)
	h.waitFor("3 files changed", 10*time.Second)
	h.keys("p")
	h.waitFor("Push renamed?", 2*time.Second) // asked first
	h.keys("y")
	h.waitFor("pushed renamed", 30*time.Second)
	h.keys("\x1b")
	h.waitGone("review · esc", 2*time.Second)

	// A second tab changing the same file: both are warned.
	h.keys(prefix + "c")
	newTab("feat-r", "", false)
	h.keys("work\r")
	h.waitFor("done", 8*time.Second)
	h.waitTabBar("⚠", 40*time.Second)
	h.waitTabBar("also changed in renamed", time.Second)

	// Find the first tab by name.
	h.keys(prefix + "f")
	h.waitFor("Go to tab", 2*time.Second)
	h.keys("ren")
	h.keys("\r")
	h.waitGone("Go to tab", 2*time.Second)
	h.waitTabBar("also changed in feat-r", 5*time.Second)

	// One message to both tabs of the group.
	h.keys(prefix + "M")
	h.waitFor("Task to several tabs", 2*time.Second)
	h.keys("bcast")
	h.keys("\r")
	h.waitFor("got: bcast", 10*time.Second)
	h.keys(prefix + "n\r")
	h.waitFor("got: bcast", 10*time.Second)
}
