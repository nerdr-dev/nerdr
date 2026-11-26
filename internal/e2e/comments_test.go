//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReviewComments covers comments on lines in F1: r on a line of the
// diff writes one, shown above it and kept in nerdr's state folder, and R
// sends them all to the agent in one message, after which they are gone.
func TestReviewComments(t *testing.T) {
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
	state := filepath.Join(scratch, "state")
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+state,
		"FAKE_LOG="+filepath.Join(scratch, "a.log"), "TERM=xterm-256color")
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" \r")
	h.keys("lc\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	readmes, _ := filepath.Glob(filepath.Join(root, "worktrees", "*", "README.md"))
	if len(readmes) != 1 {
		t.Fatalf("no worktree under %s: %v", root, readmes)
	}
	if err := os.WriteFile(readmes[0], []byte("# repoA\nCHANGEDLINE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// F1, the file's diff, and the added line: @@, # repoA, CHANGEDLINE.
	h.keys("\x1bOP")
	h.waitFor("README.md", 5*time.Second)
	h.waitFor("+1", 5*time.Second)
	h.keys("\r")
	h.waitFor("CHANGEDLINE", 3*time.Second)
	h.keys("j")
	h.keys("j")
	h.keys("r")
	h.waitFor("README.md:2: ", 3*time.Second)
	h.keys("FAKECOMMENT\r")
	h.waitFor("» FAKECOMMENT", 3*time.Second)
	h.waitFor("R send 1 comment", 3*time.Second)
	kept := filepath.Join(state, "nerdr", "review-comments.json")
	if data, _ := os.ReadFile(kept); !strings.Contains(string(data), "FAKECOMMENT") {
		t.Fatalf("the comment should be kept in %s: %q", kept, data)
	}

	// R: one message, to read before it goes, then queued for the agent.
	h.keys("R")
	h.waitFor("left a comment on it", 5*time.Second)
	h.keys("\r")
	// F3 opens over the agent, with the task in it until it is sent.
	h.waitFor("tasks", 3*time.Second)
	h.waitFor("No tasks yet", 10*time.Second)
	h.keys("\x1b")
	h.waitFor("got: FAKECOMMENT", 5*time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(kept)
		if !strings.Contains(string(data), "FAKECOMMENT") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a comment sent should be gone: %s", data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
