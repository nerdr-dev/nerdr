package watch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
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

// TestWatchClosedAtOnce covers a watch closed from several goroutines at
// once, and again after: it is closed once, with no panic.
func TestWatchClosedAtOnce(t *testing.T) {
	dir, common := repo(t)
	w, err := New(dir, common)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			w.Close()
		})
	}
	close(start)
	wg.Wait()
	w.Close()
	if w.Wait() {
		t.Fatal("a closed watch reports a change")
	}
}

// TestWatchTakesCommonDirUncleaned: a common directory given with a
// trailing separator is still the git directory, whose scratch copies are
// not changes.
func TestWatchTakesCommonDirUncleaned(t *testing.T) {
	dir, common := repo(t)
	w, err := New(dir, common+string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	write(t, filepath.Join(common, "index.lock"), "")
	quiet(t, w, "git taking its index lock")
}

// bareWatcher is a watch on dir that is not run, for addNewDir to be
// called on at the test's pace.
func bareWatcher(t *testing.T, dir string) *Watcher {
	t.Helper()
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{fs: fs, worktree: dir, gitDirs: map[string]bool{}, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	t.Cleanup(w.Close)
	return w
}

// TestAddNewDirKeepsUnderKqueueMax: where kqueue watches, a directory that
// would take the watch past kqueueMax closes the watch so the caller falls
// back to polling; one that fits is watched.
func TestAddNewDirKeepsUnderKqueueMax(t *testing.T) {
	was := kqueueGOOS
	kqueueGOOS = true
	defer func() { kqueueGOOS = was }()
	dir, _ := repo(t)
	write(t, filepath.Join(dir, "newpkg", "sub", "b.go"), "package sub\n")
	newpkg := filepath.Join(dir, "newpkg")

	w := bareWatcher(t, dir)
	w.watched = kqueueMax - 1
	w.addNewDir(newpkg)
	if slices.Contains(w.fs.WatchList(), newpkg) || w.watched != kqueueMax-1 {
		t.Fatalf("past kqueueMax: watching %v, %d counted", w.fs.WatchList(), w.watched)
	}
	if w.Wait() {
		t.Fatal("watcher stayed open after addNewDir exceeded kqueueMax")
	}

	w2 := bareWatcher(t, dir)
	w2.addNewDir(newpkg)
	// newpkg (1 dir + 1 entry "sub") + sub (1 dir + 1 entry "b.go") = 4
	if !slices.Contains(w2.fs.WatchList(), filepath.Join(newpkg, "sub")) || w2.watched != 4 {
		t.Fatalf("under kqueueMax: watching %v, %d counted", w2.fs.WatchList(), w2.watched)
	}
}

// TestAddNewDirAfterClose: once the watch is closed, addNewDir's git
// commands end at once, and nothing is added.
func TestAddNewDirAfterClose(t *testing.T) {
	dir, _ := repo(t)
	write(t, filepath.Join(dir, "newpkg", "b.go"), "package newpkg\n")
	w := bareWatcher(t, dir)
	w.cancel()
	w.addNewDir(filepath.Join(dir, "newpkg"))
	if l := w.fs.WatchList(); len(l) != 0 || w.watched != 0 {
		t.Fatalf("closed watch added %v, %d counted", l, w.watched)
	}
}

// TestAddNewDirDeduplicatesAndDropsRemovedDirs: repeated events on the
// same directory and failed Adds do not inflate watched, and removing or
// renaming a directory releases its kqueue capacity.
func TestAddNewDirDeduplicatesAndDropsRemovedDirs(t *testing.T) {
	was := kqueueGOOS
	kqueueGOOS = true
	defer func() { kqueueGOOS = was }()
	dir, _ := repo(t)
	newpkg := filepath.Join(dir, "newpkg")
	sub := filepath.Join(newpkg, "sub")
	write(t, filepath.Join(sub, "b.go"), "package sub\n")

	w := bareWatcher(t, dir)
	w.addNewDir(newpkg)
	if w.watched != 4 || len(w.dirs) != 2 {
		t.Fatalf("first add: watched=%d dirs=%v, want 4 / 2 dirs", w.watched, w.dirs)
	}
	// Duplicate create event on the same directory must not inflate count.
	w.addNewDir(newpkg)
	if w.watched != 4 || len(w.dirs) != 2 {
		t.Fatalf("duplicate add: watched=%d dirs=%v, want 4 / 2 dirs", w.watched, w.dirs)
	}
	// A non-existent directory fails fs.Add and must not consume capacity.
	w.addNewDir(filepath.Join(dir, "gone"))
	if w.watched != 4 || len(w.dirs) != 2 {
		t.Fatalf("failed add: watched=%d dirs=%v, want 4 / 2 dirs", w.watched, w.dirs)
	}
	// Removing newpkg releases both newpkg and newpkg/sub from watched.
	w.relevant(fsnotify.Event{Name: newpkg, Op: fsnotify.Remove})
	if w.watched != 0 || len(w.dirs) != 0 {
		t.Fatalf("after remove: watched=%d dirs=%v, want 0 / empty", w.watched, w.dirs)
	}
	// Re-adding after removal succeeds and returns to 4.
	w.addNewDir(newpkg)
	if w.watched != 4 || len(w.dirs) != 2 {
		t.Fatalf("re-add after remove: watched=%d dirs=%v, want 4 / 2 dirs", w.watched, w.dirs)
	}
}

// TestNewRefusesIgnoredFilesOverKqueueMax: on kqueue, fsnotify opens every
// direct entry in each watched directory (including git-ignored files). A
// root directory containing more ignored files than kqueueMax must be
// refused by New before fs.Add opens descriptors for them.
func TestNewRefusesIgnoredFilesOverKqueueMax(t *testing.T) {
	was := kqueueGOOS
	kqueueGOOS = true
	defer func() { kqueueGOOS = was }()
	dir, common := repo(t)
	write(t, filepath.Join(dir, ".gitignore"), "build/\n*.ign\n")
	for i := range kqueueMax + 10 {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("f%05d.ign", i)))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	w, err := New(dir, common)
	if err == nil {
		w.Close()
		t.Fatal("New watched a directory with >kqueueMax ignored files")
	}
}

// TestWatchReportsFileInIntermediateDirectory: when the only tracked file
// sits under a/b/f (with no file directly in a), the intermediate folder a
// is still watched, so creating a/new.go notifies the watcher.
func TestWatchReportsFileInIntermediateDirectory(t *testing.T) {
	dir, common := repo(t)
	write(t, filepath.Join(dir, "a", "b", "f.go"), "package b\n")
	w, err := New(dir, common)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	write(t, filepath.Join(dir, "a", "new.go"), "package a\n")
	changed(t, w, "creating a file in an intermediate directory")
}

// TestWatchClosesWhenNewFilesPassKqueueMax: on kqueue, creating a file
// directly inside an already-watched directory past kqueueMax closes the
// watch so descriptors are released and the caller falls back to polling.
func TestWatchClosesWhenNewFilesPassKqueueMax(t *testing.T) {
	was := kqueueGOOS
	kqueueGOOS = true
	defer func() { kqueueGOOS = was }()
	dir, _ := repo(t)
	pkg := filepath.Join(dir, "pkg")

	w := bareWatcher(t, dir)
	if err := w.fs.Add(pkg); err != nil {
		t.Fatal(err)
	}
	w.dirs = map[string]int{pkg: kqueueMax - 1}
	w.watched = kqueueMax

	extra := filepath.Join(pkg, "over.go")
	write(t, extra, "package pkg\n")
	if w.relevant(fsnotify.Event{Name: extra, Op: fsnotify.Create}) {
		t.Fatal("relevant returned true after crossing kqueueMax")
	}
	if w.Wait() {
		t.Fatal("watcher stayed open after files in watched dir passed kqueueMax")
	}
}
