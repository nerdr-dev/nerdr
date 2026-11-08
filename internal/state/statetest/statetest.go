// Package statetest has the session files of released versions of nerdr,
// for the tests that read them.
package statetest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// Old are the session files of released versions, in state's testdata: for
// each version one as nerdr makes them (-tabs) and one with every field
// set (-full). The README there says how they were made.
func Old(t testing.TB, kind string) []string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(here), "..", "testdata", "v*-"+kind+".json"))
	if len(files) == 0 {
		t.Fatal("no session files of older versions")
	}
	return files
}

// Put puts the session file at path in place as root's: its folders,
// written as /ROOT, become root's. It returns what it wrote. The state
// folder is XDG_STATE_HOME's, which the test sets.
func Put(t testing.TB, path, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.ReplaceAll(string(b), "/ROOT", root))
	if err := os.MkdirAll(filepath.Dir(state.Path(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(root), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}
