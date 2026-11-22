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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	// dirs maps each actively watched worktree directory to the number of
	// files counted in it; watched is the sum of 1+files across dirs (as
	// kqueue counts them). Only run's goroutine changes both once run has
	// started.
	dirs    map[string]int
	watched int
	done    chan struct{}
	// ctx is cancelled by Close, ending a git command addNewDir waits on.
	ctx    context.Context
	cancel context.CancelFunc
	// closing makes Close once only, however many call it at once.
	closing sync.Once
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
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{
		ctx:      ctx,
		cancel:   cancel,
		C:        make(chan struct{}, 1),
		fs:       fs,
		worktree: path,
		gitDirs:  map[string]bool{},
		dirs:     map[string]int{},
		notes:    store.Dir(path),
		done:     make(chan struct{}),
	}
	counts, total, err := sourceDirs(ctx, path, ".")
	if err == nil && overKqueue(total) {
		err = fmt.Errorf("%d files and folders are too many to watch with kqueue", total)
	}
	if err == nil {
		for d, files := range counts {
			if err = fs.Add(d); err != nil {
				break
			}
			w.dirs[d] = files
			w.watched += 1 + files
		}
	}
	if err == nil {
		err = w.addGitDirs(commonDir)
	}
	if err != nil {
		cancel()
		fs.Close()
		return nil, err
	}
	go w.run()
	return w, nil
}

// addGitDirs watches the worktree's own git directory (its index and HEAD),
// the common directory (the branch refs, and by default the notes folder)
// and the records' directory when it exists yet.
// Both are Cleaned, as relevant's directories are, so that a commonDir
// given with a trailing separator still matches.
func (w *Watcher) addGitDirs(commonDir string) error {
	commonDir = filepath.Clean(commonDir)
	dirs := []string{commonDir}
	if out, err := proc.Output(gitsafe.Command(w.worktree, "rev-parse", "--absolute-git-dir")); err == nil {
		if d := filepath.Clean(strings.TrimSpace(string(out))); d != commonDir {
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

// Close stops the watch. It may be called more than once, and at once.
func (w *Watcher) Close() {
	w.closing.Do(func() {
		close(w.done)
		w.cancel()
		w.fs.Close()
	})
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
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		w.dropPath(ev.Name)
	}
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil {
			if fi.IsDir() {
				w.addNewDir(ev.Name)
			} else if _, ok := w.dirs[dir]; ok {
				w.dirs[dir]++
				w.watched++
			}
		}
	}
	return true
}

// dropPath releases watch capacity when a file or directory is removed or
// renamed away: any watched directory at or below path is dropped from
// dirs and fsnotify, and a removed file inside a watched directory lowers
// that directory's file count.
func (w *Watcher) dropPath(path string) {
	p := filepath.Clean(path)
	prefix := p + string(filepath.Separator)
	droppedDir := false
	for d, files := range w.dirs {
		if d == p || strings.HasPrefix(d, prefix) {
			delete(w.dirs, d)
			w.watched -= 1 + files
			_ = w.fs.Remove(d)
			droppedDir = true
		}
	}
	if droppedDir {
		return
	}
	parent := filepath.Clean(filepath.Dir(p))
	if n, ok := w.dirs[parent]; ok && n > 0 {
		w.dirs[parent] = n - 1
		w.watched--
	}
}

// addNewDir watches a directory created in the worktree, and the ones git
// would list inside it — an agent creating a package creates its
// subdirectories before the watch on the first can be added. Where kqueue
// watches (not Linux), what would take the watch past kqueueMax is not
// added: changes in it go unseen until the watch is set up again. The git
// commands are given gitTimeout, and end once the watch is closed.
func (w *Watcher) addNewDir(dir string) {
	rel, err := filepath.Rel(w.worktree, dir)
	if err != nil || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		return
	}
	ctx, cancel := context.WithTimeout(w.ctx, gitTimeout)
	defer cancel()
	if proc.Run(gitsafe.CommandContext(ctx, w.worktree, "check-ignore", "-q", "--", rel)) == nil {
		return // ignored: build output, dependencies
	}
	if ctx.Err() != nil {
		return // timed out, or closed: whether it is ignored is not known
	}
	counts, _, err := sourceDirs(ctx, w.worktree, rel)
	if err != nil {
		counts = map[string]int{filepath.Clean(dir): 0}
	}
	if w.dirs == nil {
		w.dirs = map[string]int{}
	}
	delta := 0
	for d, files := range counts {
		if old, ok := w.dirs[d]; ok {
			delta += files - old
		} else {
			delta += 1 + files
		}
	}
	if overKqueue(w.watched + delta) {
		return
	}
	for d, files := range counts {
		if old, ok := w.dirs[d]; ok {
			w.dirs[d] = files
			w.watched += files - old
			continue
		}
		if err := w.fs.Add(d); err == nil {
			w.dirs[d] = files
			w.watched += 1 + files
		}
	}
}

// gitTimeout is the longest addNewDir waits on a git command.
const gitTimeout = 10 * time.Second

// overKqueue reports whether n files and directories are too many to watch
// here: more than kqueueMax, where kqueue is what watches.
func overKqueue(n int) bool { return kqueueGOOS && n > kqueueMax }

// kqueueGOOS is whether kqueue watches here, fsnotify's way outside Linux
// (and Windows, which nerdr does not run on); a variable for the tests.
var kqueueGOOS = runtime.GOOS != "linux"

// sourceDirs maps each directory under sub (relative to the worktree) that
// holds a file git would list (tracked, or new and not ignored) to how many
// such files live directly in it, and returns the total directories + files.
func sourceDirs(ctx context.Context, worktree, sub string) (map[string]int, int, error) {
	out, err := proc.Output(gitsafe.CommandContext(ctx, worktree, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard", "--", sub))
	if err != nil {
		return nil, 0, err
	}
	files := 0
	counts := map[string]int{filepath.Clean(filepath.Join(worktree, sub)): 0}
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		files++
		fileDir := filepath.Clean(filepath.Join(worktree, filepath.Dir(filepath.FromSlash(f))))
		counts[fileDir]++
		for d := filepath.Dir(filepath.FromSlash(f)); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
			abs := filepath.Clean(filepath.Join(worktree, d))
			if _, ok := counts[abs]; ok {
				break // its parents are in already
			}
			counts[abs] = 0
		}
	}
	return counts, files + len(counts), nil
}
