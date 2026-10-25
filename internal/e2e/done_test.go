//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDoneOnlyWhenDone covers an agent looking done on screen while it is
// not: a pause no rule knows as working, and — with claude's hooks — a
// still, idle-looking screen in the middle of a turn. Neither may turn the
// tab done (and notify) before the agent is.
func TestDoneOnlyWhenDone(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "NERDR_") {
			env = append(env, kv)
		}
	}
	log := filepath.Join(scratch, "a.log")
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+log, "TERM=xterm-256color")
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" \r")
	h.keys("dn\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	if args, _ := os.ReadFile(log); !strings.Contains(string(args), "--settings") ||
		!strings.Contains(string(args), "hook claude") {
		t.Fatalf("claude should be started with nerdr's hooks: %s", args)
	}

	// status is the tab's state as the tab switcher lists it.
	status := func() string {
		h.keys(prefix + "f")
		h.waitFor("claude · ", 2*time.Second)
		s := h.screen()
		h.keys("\x1b")
		for _, l := range []string{"working", "done", "idle", "needs input"} {
			if strings.Contains(s, "claude · "+l) {
				return l
			}
		}
		return "?"
	}

	h.keys("\x1b[O") // not looked at: finishing makes it done
	h.keys("gap\r")
	time.Sleep(3 * time.Second) // well past the old 1.2 s
	if s := status(); s != "working" {
		t.Fatalf("mid-pause the tab is %q, want working", s)
	}
	h.waitFor("gap over", 8*time.Second)
	time.Sleep(4 * time.Second)
	if s := status(); s != "done" {
		t.Fatalf("after the pause the tab is %q, want done", s)
	}

	h.keys("\x1b[I")
	h.keys("\x1b[O")
	h.keys("hooked\r")
	h.waitFor("thinking quietly", 3*time.Second)
	time.Sleep(3 * time.Second) // still, idle-looking: the hook says working
	if s := status(); s != "working" {
		t.Fatalf("mid-turn the tab is %q, want working", s)
	}
	h.waitFor("hooked over", 5*time.Second)
	time.Sleep(time.Second)
	if s := status(); s != "done" {
		t.Fatalf("after Stop the tab is %q, want done", s)
	}
}
