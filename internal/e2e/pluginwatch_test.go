//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"nerdr/internal/plugin"
)

// An agent starts a plugin that watches: run returns at once, what the
// plugin prints comes to the agent as messages while it goes on, and stop
// ends it, its program with it.
func TestPluginWatch(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	dir := filepath.Join(cfgDir, "nerdr", "plugins", "logwatch")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "plugin.toml"),
		[]byte("description = \"Watches the log for errors.\"\nrun = [\"./run.sh\"]\nwatch = true\n"), 0o644)
	pidFile := filepath.Join(scratch, "watch.pid")
	_ = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho $$ > "+pidFile+
		"\nsleep 1\necho ERROR-one\nsleep 1\necho ERROR-two\nwhile :; do sleep 1; done\n"), 0o755)
	hello := filepath.Join(cfgDir, "nerdr", "plugins", "hello")
	_ = os.MkdirAll(hello, 0o755)
	_ = os.WriteFile(filepath.Join(hello, "plugin.toml"),
		[]byte("description = \"Says hello.\"\nrun = [\"sh\", \"-c\", \"echo hello-from-$NERDR_PLUGIN; exit 3\"]\n"), 0o644)
	stateDir := filepath.Join(scratch, "state")
	for _, d := range []string{dir, hello} {
		p, key, err := plugin.Fresh(plugin.Load(d))
		if err != nil {
			t.Fatal(err)
		}
		if err := plugin.Allow(plugin.AllowedFile(filepath.Join(stateDir, "nerdr", "sessions")), p, key); err != nil {
			t.Fatal(err)
		}
	}

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") {
			env = append(env, kv)
		}
	}
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+stateDir,
		"FAKE_LOG="+filepath.Join(scratch, "a.log"), "TERM=xterm-256color")
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" \r")
	h.keys("pl\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	// nerdr runs it, and the agent's command prints what it printed.
	h.keys("plugin-run-hello\r")
	h.waitFor("hello-from-hello", 5*time.Second)
	h.waitFor("plugin-exit-3", 5*time.Second)

	h.keys("plugin-run-logwatch\r")
	h.waitFor("Started logwatch", 5*time.Second)
	h.waitFor("watches · ", 3*time.Second)
	// What it printed is typed in for the agent, idle at its prompt.
	h.waitFor("ERROR-two", 10*time.Second)
	b, _ := os.ReadFile(pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal("the plugin's program did not start:", err)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the plugin's program does not go on")
	}

	// The fake agent reads what it is typed a line at a time: the end of
	// the message is ended first.
	h.keys("\r")
	time.Sleep(300 * time.Millisecond)
	h.keys("plugin-stop-logwatch\r")
	h.waitFor("Stopping logwatch", 5*time.Second)
	for deadline := time.Now().Add(10 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("stop did not end the plugin's program")
		}
	}
}
