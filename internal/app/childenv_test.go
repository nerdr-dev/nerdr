package app

import (
	"slices"
	"testing"
)

// A tab's agent does not inherit what names the agent or terminal nerdr
// was started from, and is told it runs in nerdr.
func TestChildEnvLeavesOutTheHost(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("KITTY_WINDOW_ID", "3")
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("NERDR_TEST_KEPT", "yes")
	env := childEnv(&tab{})
	for _, kv := range []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=abc",
		"TMUX=/tmp/tmux-1000/default,1,0", "KITTY_WINDOW_ID=3", "TERM_PROGRAM=ghostty"} {
		if slices.Contains(env, kv) {
			t.Errorf("%s inherited", kv)
		}
	}
	for _, kv := range []string{"TERM_PROGRAM=nerdr", "NERDR_TEST_KEPT=yes"} {
		if !slices.Contains(env, kv) {
			t.Errorf("%s missing", kv)
		}
	}
}
