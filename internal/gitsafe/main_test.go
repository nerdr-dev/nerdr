package gitsafe

import (
	"os"
	"testing"
)

// TestMain keeps the folder git config runs in, in the state folder (see
// statedir), out of the user's own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nerdr-gitsafe-test-state")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
