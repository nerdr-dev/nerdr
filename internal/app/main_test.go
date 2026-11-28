package app

import (
	"os"
	"testing"
)

// TestMain keeps the tests out of the user's own opencode config, where
// nerdr's plugin goes (see hooks.OpenCodeConfigDir): with these unset it
// follows HOME, or XDG_CONFIG_HOME, as a test sets them.
func TestMain(m *testing.M) {
	os.Unsetenv("OPENCODE_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")
	setPointer = func(string) {} // not onto the terminal running the tests
	spinoffHoldWait = 0          // see TestFleetSpinoffHoldUnanswered
	os.Exit(m.Run())
}
