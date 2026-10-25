//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGroups(t *testing.T) {
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
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "a.log"), "TERM=xterm-256color")
	h := start(t, bin, root, env)

	newTab := func(repoDown int, branch string) {
		h.waitFor("Repositories in", 10*time.Second)
		for i := 0; i < repoDown; i++ {
			h.keys("\x1b[B")
		}
		h.keys("\r")
		h.waitFor("branch:", 2*time.Second)
		h.keys(branch + "\r")
		h.waitFor("Agent:", 2*time.Second)
		h.keys("\r")
		h.waitTabBar(":"+branch+" claude", 10*time.Second)
	}
	newTab(0, "a")
	h.keys(prefix + "c")
	newTab(0, "b")
	h.keys(prefix + "c")
	newTab(1, "c")

	h.waitTabBar("repoA (2)", time.Second)
	h.waitTabBar("repoB (1)", time.Second)
	h.waitTabBar("1:c claude", time.Second)
	t.Logf("bars:\n%s", h.bars())

	// Into repoA: back to the tab last active there, b; its tabs are 1 and 2.
	h.keys(prefix + "[\r")
	h.waitTabBar("2:b claude", 2*time.Second)
	h.waitTabBar("1:a", time.Second)
	h.keys(prefix + "1\r")
	h.waitTabBar("1:a claude", 2*time.Second)
	h.keys(prefix + "\x1b[C\r")
	h.waitTabBar("2:b claude", 2*time.Second)
	h.keys(prefix + "\x1b[C\r") // wraps within the group
	h.waitTabBar("1:a claude", 2*time.Second)
	if strings.Contains(strings.Split(h.bars(), "\n")[1], ":c") {
		t.Fatalf("repoB's tab shown among repoA's:\n%s", h.bars())
	}

	// Closing a tab stays in its group.
	h.keys(prefix + "x")
	h.waitFor("Close tab", 2*time.Second)
	// The delete key is ignored until the asynchronous dirty check finishes.
	h.waitFor("y delete the worktree", 5*time.Second)
	h.keys("y")
	h.waitTabBar("repoA (1)", 5*time.Second)
	h.waitTabBar("1:b claude", 2*time.Second)

	h.keys(prefix + "q")
	h.keys("y")
	<-h.p.Done()
}
