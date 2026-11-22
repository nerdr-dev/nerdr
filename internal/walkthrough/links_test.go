package walkthrough

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A walkthrough is older than the files in the worktree, not than what a
// link in it points at.
func TestOlderDoesNotFollowLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "x.go")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	// Written after the links were made; what they point at changed since.
	w := Walkthrough{Time: time.Now().Format(timeLayout)}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(target, later, later); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"link.go", "out/x.go"} {
		if Older(root, w, []string{f}) {
			t.Errorf("%s: older by what a link points at", f)
		}
	}
	own := filepath.Join(root, "own.go")
	if err := os.WriteFile(own, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(own, later, later); err != nil {
		t.Fatal(err)
	}
	if !Older(root, w, []string{"own.go"}) {
		t.Error("a file of its own changed since, but not older")
	}
}
