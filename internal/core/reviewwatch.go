package core

import (
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/watch"
)

// The watch on the worktree the review panel shows, kept here where the
// worktree is: the panel may be on another machine. Core tells the client
// when it is in place (WatchingMsg), and each change it reports
// (WorktreeChangedMsg); the panel refreshes its files then, and polls
// seldom while it has the watch.

func init() { registerRequests(WatchForReview{}) }

// WatchForReview has core watch worktree Wt for the review panel, in
// place of the one it watched; "" watches none.
type WatchForReview struct{ Wt string }

// WatchingMsg tells whether the watch on worktree Wt is in place: it is
// not when the kernel refused it, or Wt is no git worktree.
type WatchingMsg struct {
	Wt string
	OK bool
}

// WorktreeChangedMsg is the watch on worktree Wt reporting a change, the
// Nth it has reported in all.
type WorktreeChangedMsg struct {
	Wt string
	N  int
}

// reviewWatch is the watch on the worktree the review panel shows: wt,
// watched by w once it is in place. n counts the changes reported, of
// every worktree watched, so a count kept from before tells whether
// anything changed since (see MaySkip).
type reviewWatch struct {
	wt string
	w  *watch.Watcher
	n  int
}

// watchStarted is the watch on wt set up away from the loop; w is nil
// when it could not be.
type watchStarted struct {
	wt string
	w  *watch.Watcher
}

// watchFired is w reporting a change.
type watchFired struct{ w *watch.Watcher }

func (q WatchForReview) Do(m *Core) Cmd {
	rw := &m.ReviewWatch
	if q.Wt == rw.wt {
		if rw.w != nil {
			m.Tell(WatchingMsg{Wt: rw.wt, OK: true}) // a client come since
		}
		return nil
	}
	old := rw.w
	rw.wt, rw.w = q.Wt, nil
	var cmds []Cmd
	if old != nil {
		cmds = append(cmds, func() Event { old.Close(); return nil })
	}
	if wt := q.Wt; wt != "" {
		// Setting it up walks the worktree's directories.
		cmds = append(cmds, func() Event {
			ev := watchStarted{wt: wt}
			if dir := store.CommonDir(wt); dir != "" {
				if w, err := watch.New(wt, dir); err == nil {
					ev.w = w
				}
			}
			return ev
		})
	}
	return Batch(cmds...)
}

func (m *Core) onWatchStarted(ev watchStarted) Cmd {
	rw := &m.ReviewWatch
	if ev.wt != rw.wt {
		if ev.w != nil {
			return func() Event { ev.w.Close(); return nil } // left meanwhile
		}
		return nil
	}
	rw.w = ev.w
	m.Tell(WatchingMsg{Wt: ev.wt, OK: ev.w != nil})
	if ev.w == nil {
		return nil
	}
	return waitForChange(ev.w)
}

func (m *Core) onWatchFired(ev watchFired) Cmd {
	rw := &m.ReviewWatch
	if ev.w != rw.w {
		return nil // a watch since closed
	}
	rw.n++
	m.Tell(WorktreeChangedMsg{Wt: rw.wt, N: rw.n})
	return waitForChange(ev.w)
}

// waitForChange waits for w's next report. It ends without an event once
// w is closed.
func waitForChange(w *watch.Watcher) Cmd {
	return func() Event {
		if !w.Wait() {
			return nil
		}
		return watchFired{w}
	}
}

// reviewWatching is the worktree the review panel's watch is in place on,
// "" for none, and how many changes it has reported in all.
func (m *Core) reviewWatching() (wt string, n int) {
	if m.ReviewWatch.w == nil {
		return "", m.ReviewWatch.n
	}
	return m.ReviewWatch.wt, m.ReviewWatch.n
}
