//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F5 lists the user's plugins, asks before it starts one's screen, and
// runs the screen with the keyboard.
func TestPlugins(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr", "plugins", "echo-ui"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "plugins", "echo-ui", "plugin.toml"),
		[]byte("description = \"Echoes a line.\"\nui = [\"sh\", \"-c\", \"echo ready; read l; echo got-$l; sleep 60\"]\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr", "plugins", "hello-ui"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "plugins", "hello-ui", "plugin.toml"),
		[]byte("description = \"Says hello.\"\nui = [\"sh\", \"-c\", \"echo hello-there; sleep 60\"]\n"), 0o644)

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
	h.keys("pl\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	f5 := "\x1b[15~"
	h.keys(f5)
	h.waitFor("echo-ui", 3*time.Second)
	h.waitFor("hello-ui", time.Second)
	h.waitFor("Echoes a line.", time.Second)

	// A plugin is shown before it starts, the first time.
	h.keys("\r")
	h.waitFor("Allow echo-ui?", 3*time.Second)
	h.keys("y")
	h.waitFor("ready", 3*time.Second)
	h.keys("abc\r")
	h.waitFor("got-abc", 3*time.Second)
	// F5 goes back to the list; the screen keeps running.
	h.keys(f5)
	h.waitFor("runs · enter", 3*time.Second)

	// So is the next, with what is in it.
	h.keys("j")
	h.waitFor("not allowed", 3*time.Second)
	h.keys("\r")
	h.waitFor("Allow hello-ui?", 3*time.Second)
	h.waitFor("plugin.toml", time.Second)
	h.keys("y")
	h.waitFor("hello-there", 5*time.Second)

	if log, _ := os.ReadFile(filepath.Join(scratch, "a.log")); strings.Contains(string(log), "abc") {
		t.Fatal("what was typed in the plugin's screen reached the agent")
	}
}
