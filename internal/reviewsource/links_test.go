package reviewsource

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A file's stamp is of the worktree's own entry: a link's, which stays the
// same as what it points at changes, and none through a linked folder.
func TestStampsDoNotFollowLinks(t *testing.T) {
	wt, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "x.go")
	if err := os.WriteFile(target, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(wt, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wt, "out")); err != nil {
		t.Fatal(err)
	}
	var s Local
	before := s.Stamps(wt, []string{"link.go", "out/x.go"})
	if _, ok := before["out/x.go"]; ok {
		t.Error("stamped a file through a linked folder")
	}
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(target, []byte("longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, later, later); err != nil {
		t.Fatal(err)
	}
	if after := s.Stamps(wt, []string{"link.go"}); after["link.go"] != before["link.go"] || after["link.go"] == "" {
		t.Errorf("stamp %q, then %q", before["link.go"], after["link.go"])
	}
}
