//go:build unix

package holder

import (
	"os"
	"path/filepath"
	"testing"
)

// A program is found in the PATH it is started with, not the holder's,
// and never in a relative entry, which could be the agent's worktree.
func TestLookPathUsesTheProgramsPATH(t *testing.T) {
	mine, theirs := t.TempDir(), t.TempDir()
	for _, d := range []string{mine, theirs} {
		if err := os.WriteFile(filepath.Join(d, "nerdr-test-prog"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", mine)
	if p, err := lookPath("nerdr-test-prog", []string{"PATH=/nowhere:" + theirs}); err != nil || p != filepath.Join(theirs, "nerdr-test-prog") {
		t.Errorf("found %q, %v; want the one in the program's PATH", p, err)
	}
	if p, err := lookPath("nerdr-test-prog", nil); err != nil || p != filepath.Join(mine, "nerdr-test-prog") {
		t.Errorf("with no PATH of its own: %q, %v", p, err)
	}
	t.Chdir(theirs)
	if p, err := lookPath("nerdr-test-prog", []string{"PATH=.:"}); err == nil {
		t.Errorf("found %q through a relative entry", p)
	}
}
