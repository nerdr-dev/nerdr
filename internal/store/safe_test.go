package store

import (
	"os"
	"path/filepath"
	"testing"
)

// An agent that can write the data folder must not have nerdr, outside
// its sandbox, append or write through a link it put there: a review's
// line appended to ~/.bashrc is run by the next shell.
func TestWritesDoNotFollowLinks(t *testing.T) {
	repo := gitRepo(t)
	t.Cleanup(func() { SetDataDir("") })
	SetDataDir("")
	outside := filepath.Join(t.TempDir(), "bashrc")
	if err := os.WriteFile(outside, []byte("# rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := Path(repo, "reviews.jsonl")
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, notes); err != nil {
		t.Fatal(err)
	}
	if err := Append(notes, map[string]string{"summary": "$(touch pwned)"}); err == nil {
		t.Error("Append wrote through a link")
	}
	page := Path(repo, filepath.Join("walkthrough", "main.html"))
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, page); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(page, []byte("<html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "# rc\n" {
		t.Errorf("the file linked to was written: %q", b)
	}
	if st, err := os.Lstat(page); err != nil || !st.Mode().IsRegular() {
		t.Errorf("WriteFile left the link in place: %v %v", st, err)
	}
}

// A folder in the data folder that an agent made a link out of it is not
// written through either.
func TestWritesDoNotLeaveTheDataFolder(t *testing.T) {
	repo := gitRepo(t)
	t.Cleanup(func() { SetDataDir("") })
	SetDataDir(".nerdr")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, ".nerdr")); err != nil {
		t.Fatal(err)
	}
	if err := Append(Path(repo, "why.jsonl"), map[string]string{"a": "b"}); err == nil {
		t.Error("Append wrote out of the worktree")
	}
	if err := WriteFile(Path(repo, "page.html"), []byte("x"), 0o644); err == nil {
		t.Error("WriteFile wrote out of the worktree")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("written outside: %v", entries)
	}
}
