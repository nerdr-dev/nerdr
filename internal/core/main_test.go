package core

import (
	"os"
	"testing"
)

// TestMain keeps the tests away from the user's own sessions: core saves the
// session itself, with or without a client.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nerdr-core-test-state")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", dir)
	// The check from inside a sandbox tries to write nothing, but for the
	// tests of that.
	SandboxWrites = func(*Core, *Tab) []string { return nil }
	SandboxWarns = func(*Core, *Tab) []string { return nil }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
