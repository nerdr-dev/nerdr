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
	// Nor into the user's own opencode config, where nerdr's plugin goes
	// (see hooks.OpenCodeConfigDir): with these unset it follows HOME, or
	// XDG_CONFIG_HOME, as a test sets them.
	os.Unsetenv("OPENCODE_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")
	// The check from inside a sandbox tries to write nothing, but for the
	// tests of that.
	SandboxWrites = func(*Core, *Tab) []string { return nil }
	SandboxWarns = func(*Core, *Tab) []string { return nil }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
