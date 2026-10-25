package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repo(t *testing.T) (dir, common string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	write(t, filepath.Join(dir, "pkg", "a.go"), "package pkg\n")
	write(t, filepath.Join(dir, ".gitignore"), "build/\n")
	git("add", ".")
	git("commit", "-q", "-m", "i")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// changed waits for a report, and fails the test if none comes.
func changed(t *testing.T, w *Watcher, what string) {
	t.Helper()
	select {
	case <-w.C:
	case <-time.After(3 * time.Second):
		t.Fatalf("no change reported after %s", what)
	}
}

// quiet makes sure no report comes.
func quiet(t *testing.T, w *Watcher, what string) {
	t.Helper()
	select {
	case <-w.C:
		t.Fatalf("a change was reported after %s", what)
	case <-time.After(4 * settle):
	}
}

func TestWatchHearsWhatMovesTheChange(t *testing.T) {
	dir, common := repo(t)
	w, err := New(dir, common)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	write(t, filepath.Join(dir, "pkg", "a.go"), "package pkg\n\nvar x = 1\n")
	changed(t, w, "editing a tracked file")

	// A directory made after the watch started, as an agent adding a
	// package does, with a subdirectory made before its watch was added.
	write(t, filepath.Join(dir, "newpkg", "sub", "b.go"), "package sub\n")
	changed(t, w, "creating a new package")
	write(t, filepath.Join(dir, "newpkg", "sub", "b.go"), "package sub\n\nvar y = 2\n")
	changed(t, w, "editing a file in a directory made after the watch began")

	write(t, filepath.Join(common, "nerdr", "why.jsonl"), "{}\n")
	changed(t, w, "recording a note")

	// A lock file of a package manager's is a file like any other; only
	// git's own, in its directory, are scratch copies.
	write(t, filepath.Join(dir, "yarn.lock"), "# yarn\n")
	changed(t, w, "writing yarn.lock")
	write(t, filepath.Join(common, "index.lock"), "")
	quiet(t, w, "git taking its index lock")
}

// TestWatchReportsChangesThatNeverPause: a file written again and again,
// faster than a burst settles, is still reported within settleMax.
func TestWatchReportsChangesThatNeverPause(t *testing.T) {
	dir, common := repo(t)
	w, err := New(dir, common)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(settle / 3):
				_ = os.WriteFile(filepath.Join(dir, "pkg", "a.go"), []byte(strings.Repeat("x", i)), 0o644)
			}
		}
	}()
	select {
	case <-w.C:
	case <-time.After(settleMax + time.Second):
		t.Fatal("changes that never paused were never reported")
	}
}

func TestWatchIgnoresWhatGitIgnores(t *testing.T) {
	dir, common := repo(t)
	w, err := New(dir, common)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Making the ignored directory is itself a change at the root; let it
	// settle, then writing inside it must not wake anyone.
	changed(t, w, "creating a directory at the root")
	write(t, filepath.Join(dir, "build", "out.js"), "x\n")
	quiet(t, w, "writing into an ignored directory")
}
