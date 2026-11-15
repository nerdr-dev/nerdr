package move

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSnapshotRacyCleanIndex(t *testing.T) {
	_, src, _ := machines(t)
	wt := work(t, src)
	git(t, wt, "add", "-A")
	git(t, wt, "commit", "-qm", "baseline")
	// Model coarse stat timestamps, as the git package's index tests do.
	git(t, wt, "config", "core.trustctime", "false")
	git(t, wt, "config", "core.checkStat", "minimal")
	file := filepath.Join(wt, "a.txt")
	stamp := time.Unix(1700000000, 0)
	stampFile := func(path string) {
		t.Helper()
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	write(t, file, "a\n")
	stampFile(file)
	git(t, wt, "add", "a.txt")
	index := strings.TrimSpace(git(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	stampFile(index)
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	write(t, file, "b\n")
	stampFile(file)
	want := strings.TrimSpace(git(t, wt, "hash-object", "a.txt"))
	p, err := Snapshot(wt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Wip == "" || strings.TrimSpace(git(t, wt, "rev-parse", p.Wip+":a.txt")) != want {
		t.Fatal("snapshot lost the worktree edit")
	}
	if got := git(t, wt, "show", p.Staged+":a.txt"); got != "a\n" {
		t.Fatalf("staged content changed: %q", got)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !st.ModTime().Equal(stamp) {
		t.Fatal("snapshot changed the source index")
	}
}

func TestSnapshotRejectsUnsafeIndex(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "directory", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			_, src, _ := machines(t)
			wt := work(t, src)
			index := strings.TrimSpace(git(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "index"))
			if err := os.Remove(index); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "fifo":
				err = syscall.Mkfifo(index, 0o600)
			case "symlink":
				err = os.Symlink(index+"-missing", index)
			case "directory":
				err = os.Mkdir(index, 0o700)
			case "oversized":
				var f *os.File
				f, err = os.Create(index)
				if err == nil {
					err = f.Truncate(129 << 20)
					f.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := Snapshot(wt); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("snapshot accepted an unsafe index")
				}
			case <-time.After(5 * time.Second):
				if kind == "fifo" {
					if f, err := os.OpenFile(index, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
						f.Close()
					}
				}
				t.Fatal("snapshot blocked on an unsafe index")
			}
		})
	}
}
