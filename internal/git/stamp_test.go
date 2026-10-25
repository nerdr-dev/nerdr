package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestStamp: a commit changes the stamp, an edit not added does not.
func TestStamp(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "a")
	before := Stamp(dir)
	if before == "" {
		t.Fatal("no stamp")
	}
	if err := os.WriteFile(file, []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Stamp(dir) != before {
		t.Fatal("an edit not added changed the stamp")
	}
	time.Sleep(10 * time.Millisecond)
	run("commit", "-q", "-am", "b")
	if Stamp(dir) == before {
		t.Fatal("a commit did not change the stamp")
	}
}
