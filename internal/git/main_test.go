package git

import (
	"os"
	"testing"
)

// TestMain keeps the tests' temporary Git folders, made in the state
// folder (see statedir), out of the user's own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nerdr-git-test-state")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
