package why

import (
	"os"
	"path/filepath"
	"testing"
)

// A note is put on a file in the worktree, never on what a link in it
// points at: a file link or a folder link to code elsewhere is refused.
func TestAddDoesNotFollowWorktreeLinks(t *testing.T) {
	_, linked := gitWorktrees(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.go"), []byte("secret()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.go"), filepath.Join(linked, "pkg", "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(linked, "out")); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"pkg/link.go", "out/x.go"} {
		if _, _, err := Add(linked, Note{File: file, Anchor: "secret()", Short: "x"}); err == nil {
			t.Errorf("%s: a note through a link was taken", file)
		}
	}
}
