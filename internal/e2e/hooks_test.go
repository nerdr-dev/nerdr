//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrationReports checks that a herdr integration inside a tab's
// agent reports to nerdr: its state wins over the screen, and the session
// it names is the one the tab resumes.
func TestIntegrationReports(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	stateDir := filepath.Join(scratch, "state")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+stateDir,
		"XDG_RUNTIME_DIR="+t.TempDir(),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"TERM=xterm-256color",
	)
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" ")
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-h\r")
	h.waitFor("Agent:", 3*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 15*time.Second)

	// The screen says idle; the integration says blocked, and wins.
	h.keys("hook-blocked\r")
	h.waitFor("reported blocked", 5*time.Second)
	h.keys(prefix + "f") // the switcher names each tab's state
	h.waitFor("claude · needs input", 3*time.Second)
	h.keys("\x1b")

	h.keys("hook-idle\r")
	h.waitFor("reported idle", 5*time.Second)
	h.keys(prefix + "f") // the switcher names each tab's state
	h.waitFor("claude · idle", 3*time.Second)
	h.keys("\x1b")

	// The session the integration named is the one saved for the tab.
	deadline := time.Now().Add(5 * time.Second)
	for {
		files, _ := filepath.Glob(filepath.Join(stateDir, "nerdr", "sessions", "*.json"))
		var all string
		for _, f := range files {
			data, _ := os.ReadFile(f)
			all += string(data)
		}
		if strings.Contains(all, `"session_id": "sess-idle"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session not saved from the report:\n%s", all)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
