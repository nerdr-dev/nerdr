//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCodeReview checks the code review going round by itself: the tab's
// agent changes the worktree, another agent reviews it and sends findings,
// the agent fixes them, and the reviewer looks again and approves.
func TestCodeReview(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"), []byte(`worktrees_dir = "worktrees"
tab_bar = "top"
install = "off"
[agents.claude]
command = ["`+fake+`"]
[agents.codex]
command = ["`+fake+`"]
detect = "claude"
[review]
auto = true
agent = "codex"
when = "turn"
send_findings = "auto"
`), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"XDG_RUNTIME_DIR="+t.TempDir(),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"TERM=xterm-256color",
	)
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" ")
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-r\r")
	h.waitFor("Agent:", 3*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 15*time.Second)

	// The agent changes the worktree; once it is done, codex reviews, in
	// a pane beside it.
	h.keys("work\r")
	h.waitFor("codex reviews claude", 15*time.Second)
	h.waitFor("codex: approved", 40*time.Second)
	h.waitGone("codex reviews claude", 5*time.Second)
	wt := filepath.Join(root, "worktrees")
	matches, _ := filepath.Glob(filepath.Join(wt, "*", "new.txt"))
	if len(matches) != 1 {
		t.Fatalf("no new.txt in a worktree under %s", wt)
	}
	if got, _ := os.ReadFile(matches[0]); string(got) != "hi\nmore\n" {
		t.Fatalf("the agent did not fix what the review found: new.txt is %q", got)
	}

	data, _ := os.ReadFile(filepath.Join(root, "repoA", ".git", "nerdr", "reviews.jsonl"))
	recs := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(recs) != 2 || !strings.Contains(recs[0], "FAKEREVIEW") || !strings.Contains(recs[1], `"verdict":"approve"`) {
		t.Fatalf("reviews kept:\n%s", data)
	}

	// A review by hand opens a pane beside the agent, to pick the
	// reviewer in; esc closes it.
	h.keys(prefix + "t")
	h.keys("r")
	h.waitFor("Review claude's work", 3*time.Second)
	h.keys("\x1b")
	h.waitGone("Review claude's work", 3*time.Second)
}
