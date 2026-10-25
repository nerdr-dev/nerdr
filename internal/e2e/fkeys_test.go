//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFunctionKeys(t *testing.T) {
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
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" \r")
	h.keys("fk\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)
	// F2/F1 reach the review panel while the agent has the keyboard,
	// in both encodings terminals use, and do not reach the agent.
	for _, seq := range []string{"\x1bOQ", "\x1b[12~"} {
		h.keys(seq)
		h.waitFor("pull request · ", 3*time.Second)
		h.keys("\x1bOP")
		h.waitGone("pull request · ", 3*time.Second)
	}
	h.keys("\x1bOP")
	// The F-keys give the panel the keyboard; the prefix and a a take it back.
	h.waitFor("review · esc → agent", 3*time.Second)
	h.keys(prefix + "a")
	h.keys("a")
	h.waitGone("review · esc → agent", 3*time.Second)
	h.keys("hello\r")
	h.waitFor("got: hello", 3*time.Second)

	// The prefix twice only cancels: nothing reaches the agent.
	h.keys(prefix)
	h.waitFor("New tab", 2*time.Second)
	h.keys(prefix)
	h.waitGone("Close tab", 2*time.Second)
	h.keys("after\r")
	h.waitFor("got: after", 3*time.Second)
	if strings.Contains(h.screen(), "\\a") {
		t.Fatal("the prefix reached the agent")
	}

	// The prefix shows what can follow it, and esc takes it away.
	h.keys(prefix)
	h.waitFor("New tab", 2*time.Second)
	h.waitFor("Close tab", time.Second)
	h.keys("\x1b")
	h.waitGone("Close tab", 2*time.Second)

	// Finished while the window was out of focus: a filled light. Once the
	// window has focus again: a hollow one.
	h.keys("\x1b[O") // herdr takes focus away
	h.keys("work\r")
	h.waitTabBar("● 1:fk", 3*time.Second)
	h.waitFor("done", 5*time.Second)
	time.Sleep(2 * time.Second) // past the idle debounce
	h.waitTabBar("● 1:fk", time.Second)
	h.keys("\x1b[I")
	h.waitTabBar("○ 1:fk", 2*time.Second)
}
