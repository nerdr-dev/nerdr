//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPanelShellAndSwitchAgent covers terminals beside the agent, in panes
// of their own, and switching a tab's agent.
func TestPanelShellAndSwitchAgent(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\nshell = \"/bin/sh\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n[agents.opencode]\ncommand = [\"env\", \"FAKE_NO_HANDOFF=1\", \""+fake+"\"]\n"), 0o644)
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
	h.keys("sw\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	// t t opens a terminal beside the agent, and it has the keyboard; a
	// row over the two names them.
	h.keys(prefix + "t")
	h.keys("t")
	h.waitFor("─ terminal ─", 3*time.Second)
	h.keys("echo pan$((6*7))\r")
	h.waitFor("pan42", 3*time.Second)
	// Back to the agent: typing goes there again, right after the step,
	// with the group still on for more steps.
	h.keys(prefix + "t")
	h.keys("\x1b[D") // ←
	h.keys("back\r")
	h.waitFor("got: back", 3*time.Second)
	// A second terminal, and the first closed: exit closes its pane.
	h.keys(prefix + "t")
	h.keys("t")
	h.waitFor("terminal 2", 3*time.Second)
	h.keys("exit\r")
	h.waitGone("terminal 2", 3*time.Second)
	h.keys(prefix + "t")
	h.keys("\x1b[C") // → to the first terminal
	h.keys(prefix + "t")
	h.keys("x")
	h.waitGone("pan42", 3*time.Second)
	h.waitGone("─ terminal ─", 3*time.Second)

	// Every PR is in F2, with the keyboard; esc gives it back.
	h.keys(prefix + "i")
	h.keys("p")
	h.waitFor("my pull requests", 3*time.Second)
	h.waitFor("tab: open in a tab", 3*time.Second)
	h.keys("\x1b")
	h.waitGone("review · esc", 3*time.Second)

	// Switch to opencode, asking claude for a handoff first: it writes
	// one, and opencode is given it.
	h.keys(prefix + "a")
	h.keys("s")
	h.waitFor("Switch sw to", 2*time.Second)
	h.keys("1")
	h.waitFor("Ask claude for a handoff first", 2*time.Second)
	h.keys("2")
	h.waitTabBar("sw opencode", 15*time.Second)
	h.waitFor("given the handoff", 15*time.Second)

	// And back to claude's conversation. This opencode cannot write a
	// handoff: once it has answered without one, claude is told to read
	// its conversation.
	h.keys(prefix + "a")
	h.keys("s")
	h.waitFor("continues its conversation", 2*time.Second)
	h.keys("1")
	h.waitFor("Ask opencode for a handoff first", 2*time.Second)
	h.keys("2")
	h.waitTabBar("sw claude", 20*time.Second)
	h.waitFor("resumed conversation", 5*time.Second)
	h.waitFor("told to read the old conversation", 15*time.Second)
}
