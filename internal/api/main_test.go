package api

import (
	"os"
	"testing"
)

// TestMain keeps the tests away from the user's own sessions: what the
// API asks of core, a message sent, has it save the session.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nerdr-api-test-state")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
