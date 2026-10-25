// Package watch tells when a worktree's change may have moved: a file in it
// written, created or removed, the index or HEAD updated, or a note or
// reading order recorded.
//
// The review panel used to find out by asking git every second and a half, which is
// a git status of the whole worktree on every tick whether anything
// happened or not, and still up to a tick and a half late when something
// did. A watch costs nothing while nothing happens, and hears about an
// agent's edit as it lands.
//
// Only directories git would list are watched — tracked files and new ones
// that are not ignored — so node_modules and build output neither use up
// the kernel's watch limit nor wake nerdr on every build.
//
// On macOS the watch is kqueue's, which holds a file open for every file
// in a watched directory, not one watch per directory as inotify does. A
// worktree with more files than kqueueMax is polled there instead, so the
// watch neither leaves nerdr short of files to open a pty or run git with
// nor makes switching tabs slow while it opens them all.
package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/store"

	"github.com/fsnotify/fsnotify"
)

// settle is how long the watch waits after an event for more to follow. An
// agent's edit, or a git command, is a burst of events; one refresh after
// the burst is what is wanted, not one per event.
const settle = 150 * time.Millisecond

// settleMax is the longest a report waits for a burst to end: changes
// that never pause, a file written over and over, are still reported.
const settleMax = 2 * time.Second

// kqueueMax is the most files and directories a worktree may have to be
// watched where the watch holds each open (see the package comment).
const kqueueMax = 5000

// Watcher reports changes to one worktree on C.
type Watcher struct {
	// C receives a value once a burst of changes has settled. It holds at
	// most one: a change reported but not yet read covers any after it.
	C chan struct{}

	fs       *fsnotify.Watcher
	worktree string
	// gitDirs are the directories git keeps its own state in. Only the few
	// files there that say the change moved are listened to; see relevant.
	gitDirs map[string]bool
	notes   string // where the why notes and reading order are kept; see internal/store
	done    chan struct{}
}

// New watches the worktree at path. commonDir is the repository's git common
// directory, which holds the notes and reading order every worktree shares.
// It fails when the kernel refuses the watches, for instance when its limit
// is used up, or when the worktree is too big for kqueue; the caller then
// goes on polling.
func New(path, commonDir string) (*Watcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		C:        make(chan struct{}, 1),
		fs:       fs,
		worktree: path,
		gitDirs:  map[string]bool{},
		notes:    store.Dir(path),
		done:     make(chan struct{}),
	}
	dirs, files, err := sourceDirs(path, ".")
	if err == nil && runtime.GOOS != "linux" && files+len(dirs) > kqueueMax {
		err = fmt.Errorf("%d files and folders are too many to watch with kqueue", files+len(dirs))
	}
	if err == nil {
		for _, d := range dirs {
			if err = fs.Add(d); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = w.addGitDirs(commonDir)
	}
	if err != nil {
		fs.Close()
		return nil, err
	}
	go w.run()
	return w, nil
}

// addGitDirs watches the worktree's own git directory (its index and HEAD),
// the common directory (the branch refs, and by default the notes folder)
// and the records' directory when it exists yet.
func (w *Watcher) addGitDirs(commonDir string) error {
	dirs := []string{commonDir}
	if out, err := proc.Output(gitsafe.Command(w.worktree, "rev-parse", "--absolute-git-dir")); err == nil {
		if d := strings.TrimSpace(string(out)); d != commonDir {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		w.gitDirs[d] = true
		if err := w.fs.Add(d); err != nil {
			return err
		}
	}
	if _, err := os.Stat(w.notes); err == nil {
		return w.fs.Add(w.notes)
	}
	return nil
}

// Wait blocks until a change is reported, and returns false instead once
// the watch has been closed.
func (w *Watcher) Wait() bool {
	select {
	case <-w.C:
		return true
	case <-w.done:
		return false
	}
}

// Close stops the watch.
func (w *Watcher) Close() {
	select {
	case <-w.done:
	default:
		close(w.done)
		w.fs.Close()
	}
}

func (w *Watcher) run() {
	timer := time.NewTimer(settle)
	timer.Stop()
	// burst is when the first change not yet reported came, zero when
	// none waits.
	var burst time.Time
	wait := func() {
		now := time.Now()
		if burst.IsZero() {
			burst = now
		}
		timer.Reset(max(min(settle, settleMax-now.Sub(burst)), 0))
	}
	for {
		select {
		case <-w.done:
			timer.Stop()
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if w.relevant(ev) {
				wait()
			}
		case _, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// An overflowed queue has lost events: report a change, since
			// one may be among them.
			wait()
		case <-timer.C:
			burst = time.Time{}
			select {
			case w.C <- struct{}{}:
			default:
			}
		}
	}
}

// relevant reports whether ev can have moved the change, and starts
// watching a directory that has just been created.
func (w *Watcher) relevant(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod {
		return false
	}
	dir, name := filepath.Split(ev.Name)
	dir = filepath.Clean(dir)
	if ev.Name == w.notes && ev.Has(fsnotify.Create) {
		// The notes folder, made by the first note: in the git directory,
		// or in the worktree where git ignores it (see store.ExcludeData).
		_ = w.fs.Add(w.notes)
		return true
	}
	if w.gitDirs[dir] {
		// Not index.lock and the like, git's scratch copies: the rename
		// that ends one is what counts.
		switch name {
		case "index", "HEAD", "packed-refs":
			return true
		}
		return false
	}
	if dir == w.notes {
		return !strings.HasSuffix(name, ".lock")
	}
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			w.addNewDir(ev.Name)
		}
	}
	return true
}

// addNewDir watches a directory created in the worktree, and the ones git
// would list inside it — an agent creating a package creates its
// subdirectories before the watch on the first can be added.
func (w *Watcher) addNewDir(dir string) {
	rel, err := filepath.Rel(w.worktree, dir)
	if err != nil || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		return
	}
	if proc.Run(gitsafe.Command(w.worktree, "check-ignore", "-q", "--", rel)) == nil {
		return // ignored: build output, dependencies
	}
	_ = w.fs.Add(dir)
	if dirs, _, err := sourceDirs(w.worktree, rel); err == nil {
		for _, d := range dirs {
			_ = w.fs.Add(d)
		}
	}
}

// sourceDirs lists the directories under sub (relative to the worktree)
// that hold a file git would list: tracked, or new and not ignored, and
// how many such files there are.
func sourceDirs(worktree, sub string) ([]string, int, error) {
	out, err := proc.Output(gitsafe.Command(worktree, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard", "--", sub))
	if err != nil {
		return nil, 0, err
	}
	files := 0
	seen := map[string]bool{filepath.Join(worktree, sub): true}
	dirs := []string{filepath.Join(worktree, sub)}
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		files++
		for d := filepath.Dir(filepath.FromSlash(f)); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
			abs := filepath.Join(worktree, d)
			if seen[abs] {
				break // its parents are in already
			}
			seen[abs] = true
			dirs = append(dirs, abs)
		}
	}
	return dirs, files, nil
}
