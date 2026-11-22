package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An agent that cannot write .git/hooks but can write the git directory
// must not have nerdr write a hook for it: .git/nerdr made a link to
// hooks, inside the anchor os.Root keeps to, is refused like a link out.
func TestWritesDoNotFollowALinkToHooks(t *testing.T) {
	repo := gitRepo(t)
	t.Cleanup(func() { SetDataDir("") })
	SetDataDir("")
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(hooks)
	if err := os.Symlink("hooks", filepath.Join(repo, ".git", "nerdr")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(Path(repo, "pre-commit"), []byte("#!/bin/sh\n"), 0o755); err == nil {
		t.Error("WriteFile wrote through .git/nerdr -> hooks")
	}
	if err := WriteFile(Path(repo, filepath.Join("handoff", "pre-commit")), []byte("x"), 0o644); err == nil {
		t.Error("WriteFile made a folder through .git/nerdr -> hooks")
	}
	if err := Append(Path(repo, "post-checkout"), map[string]string{"a": "b"}); err == nil {
		t.Error("Append wrote through .git/nerdr -> hooks")
	}
	if after, _ := os.ReadDir(hooks); len(after) != len(before) {
		t.Errorf("hooks changed: %v -> %v", before, after)
	}

	// A link one folder down, nerdr/handoff -> ../hooks, likewise.
	if err := os.Remove(filepath.Join(repo, ".git", "nerdr")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git", "nerdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../hooks", filepath.Join(repo, ".git", "nerdr", "handoff")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(Path(repo, filepath.Join("handoff", "pre-push")), []byte("x"), 0o644); err == nil {
		t.Error("WriteFile wrote through nerdr/handoff -> hooks")
	}
	if _, err := os.Lstat(filepath.Join(hooks, "pre-push")); err == nil {
		t.Error("a file was written in hooks")
	}
}

// A named pipe put where a records file is must neither keep nerdr
// waiting on its open nor be written to.
func TestAppendRefusesAPipe(t *testing.T) {
	repo := gitRepo(t)
	t.Cleanup(func() { SetDataDir("") })
	SetDataDir("")
	notes := Path(repo, "reviews.jsonl")
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(notes, 0o644); err != nil {
		t.Skip("mkfifo:", err)
	}
	done := make(chan error, 2)
	go func() { done <- Append(notes, map[string]string{"a": "b"}) }()
	go func() {
		_, err := Rewrite(notes, func([]byte) bool { return false })
		done <- err
	}()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Error("a pipe was opened as a records file")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("blocked on a named pipe")
		}
	}
}

// openFile refuses what is not a regular file once opened, even when the
// look before the open saw nothing there (a pipe made in between).
func TestOpenFileRefusesWhatIsNotRegular(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "p")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("mkfifo:", err)
	}
	if f, err := openFile(fifo, os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Fatal("opened a pipe")
	}
}

// Rewrite holds what it keeps in memory: a file bigger than ReadLines
// would read is left as it is, not read whole.
func TestRewriteRefusesAFileTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{}\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxRecordsBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	called := false
	if _, err := Rewrite(path, func([]byte) bool { called = true; return false }); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("Rewrite of a file too large: %v", err)
	}
	if called {
		t.Error("Rewrite read a file too large")
	}
	if st, _ := os.Stat(path); st.Size() != maxRecordsBytes+1 {
		t.Errorf("the file was changed: %d bytes", st.Size())
	}
}

// Another process holding a records file's lock — an agent that took it
// and never lets go — makes Append and Rewrite give up after lockTimeout,
// not wait forever; once let go, they work again.
func TestLockGivesUpWhenHeld(t *testing.T) {
	old := lockTimeout
	lockTimeout = 200 * time.Millisecond
	t.Cleanup(func() { lockTimeout = old })
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A separate open file description, as another process's would be.
	holder, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = Append(path, map[string]int{"b": 2})
	if !errors.Is(err, errLockTimeout) {
		t.Errorf("Append under a held lock: %v", err)
	}
	if _, err := Rewrite(path, func([]byte) bool { return false }); !errors.Is(err, errLockTimeout) {
		t.Errorf("Rewrite under a held lock: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("waited %v", d)
	}
	_ = syscall.Flock(int(holder.Fd()), syscall.LOCK_UN)
	if err := Append(path, map[string]int{"b": 2}); err != nil {
		t.Fatalf("Append once let go: %v", err)
	}
	if b, _ := os.ReadFile(path); strings.Count(string(b), "\n") != 2 {
		t.Errorf("file: %q", b)
	}
}

// A lock held only for a moment, as by another nerdr appending, is waited
// for.
func TestLockWaitsForAShortHold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	holder, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	fd := int(holder.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	let := make(chan struct{})
	go func() {
		defer close(let)
		time.Sleep(100 * time.Millisecond)
		_ = syscall.Flock(fd, syscall.LOCK_UN)
	}()
	defer func() { <-let }()
	if err := Append(path, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
}

// BranchCreated reads a reflog an agent may have replaced: a pipe, a
// link to /dev/zero or out of the repository, a folder on the way made a
// link, or a first line without end are all answered at once with no
// creation known, not waited on or read whole.
func TestBranchCreatedRefusesWhatAnAgentPutThere(t *testing.T) {
	repo := gitRepo(t)
	heads := filepath.Join(repo, ".git", "logs", "refs", "heads")
	if err := os.MkdirAll(heads, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	good := strings.Repeat("0", 40) + " " + strings.Repeat("1", 40) + " t <t@t> 1700000000 +0000\tbranch: Created\n"
	if err := os.WriteFile(filepath.Join(outside, "x"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(heads, "pipe"), 0o644); err != nil {
		t.Skip("mkfifo:", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink("/dev/zero", filepath.Join(heads, "zero")))
	must(os.Symlink(filepath.Join(outside, "x"), filepath.Join(heads, "out")))
	must(os.Symlink(outside, filepath.Join(heads, "dir")))
	must(os.WriteFile(filepath.Join(heads, "real"), []byte(good), 0o644))
	// A huge first line: only maxReflogLine of it is read.
	huge := strings.Repeat("0", 40) + " " + strings.Repeat("x", 4<<20)
	must(os.WriteFile(filepath.Join(heads, "huge"), []byte(huge), 0o644))

	for _, b := range []string{"pipe", "zero", "out", "dir/x", "huge"} {
		done := make(chan bool, 1)
		go func() {
			_, ok := BranchCreated(repo, b)
			done <- ok
		}()
		select {
		case ok := <-done:
			if ok {
				t.Errorf("%s: a creation was read", b)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: BranchCreated hung", b)
		}
	}
	if c, ok := BranchCreated(repo, "real"); !ok || c.Unix() != 1700000000 {
		t.Errorf("a real reflog: %v %v", c, ok)
	}
}
