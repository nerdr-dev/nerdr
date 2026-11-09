package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// eventsClient keeps what it is told.
type eventsClient struct {
	NoClient
	told []Event
}

func (c *eventsClient) Tell(ev Event) Cmd {
	c.told = append(c.told, ev)
	return nil
}

// runNow runs cmd and what a batch of it holds, giving the events; one
// that takes longer than d is left.
func runNow(cmd Cmd, d time.Duration) []Event {
	if cmd == nil {
		return nil
	}
	got := make(chan Event, 1)
	go func() { got <- cmd() }()
	select {
	case ev := <-got:
		if b, ok := ev.(BatchEvent); ok {
			var out []Event
			for _, c := range b {
				out = append(out, runNow(c, d)...)
			}
			return out
		}
		if ev == nil {
			return nil
		}
		return []Event{ev}
	case <-time.After(d):
		return nil
	}
}

// TestCoreWatchesTheReviewedWorktree: asked to, core watches the worktree
// the review panel shows, tells the client once the watch is in place and
// at each change, and counts the changes for MaySkip; asked for none, it
// stops.
func TestCoreWatchesTheReviewedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	wt := t.TempDir()
	if out, err := exec.Command("git", "-C", wt, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cl := &eventsClient{}
	c := &Core{Client: cl}
	handle := func(evs []Event) Cmd {
		t.Helper()
		var cmds []Cmd
		c.Mu.Lock()
		defer c.Mu.Unlock()
		for _, ev := range evs {
			cmd, ok := c.Handle(ev)
			if !ok {
				t.Fatalf("core did not handle %T", ev)
			}
			cmds = append(cmds, cmd)
		}
		return Batch(cmds...)
	}

	c.Mu.Lock()
	cmd := WatchForReview{Wt: wt}.Do(c)
	c.Mu.Unlock()
	wait := handle(runNow(cmd, 5*time.Second))
	if len(cl.told) != 1 || cl.told[0] != (WatchingMsg{Wt: wt, OK: true}) {
		if len(cl.told) == 1 && cl.told[0] == (WatchingMsg{Wt: wt}) {
			t.Skip("the kernel refused the watch")
		}
		t.Fatalf("told %#v, want the watch in place", cl.told)
	}

	if err := os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs := runNow(wait, 3*time.Second)
	if len(evs) == 0 {
		t.Fatal("the watch reported nothing")
	}
	handle(evs)
	if last := cl.told[len(cl.told)-1]; last != (WorktreeChangedMsg{Wt: wt, N: 1}) {
		t.Fatalf("told %#v, want the change", last)
	}
	if got, n := c.reviewWatching(); got != wt || n != 1 {
		t.Fatalf("watching %q, %d changes", got, n)
	}

	c.Mu.Lock()
	cmd = WatchForReview{Wt: wt}.Do(c) // a client come since
	c.Mu.Unlock()
	if cmd != nil || cl.told[len(cl.told)-1] != (WatchingMsg{Wt: wt, OK: true}) {
		t.Fatalf("asked again for the same: %v, told %#v", cmd != nil, cl.told[len(cl.told)-1])
	}

	c.Mu.Lock()
	cmd = WatchForReview{}.Do(c)
	c.Mu.Unlock()
	runNow(cmd, 5*time.Second)
	if got, n := c.reviewWatching(); got != "" || n != 1 {
		t.Fatalf("after stopping: watching %q, %d changes", got, n)
	}
}
