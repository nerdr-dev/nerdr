package core

import (
	"fmt"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/holder"
)

// The programs in the holder that no tab took up, as a core started again
// found them: one of a tab since closed, or a reviewer of a review the
// session did not keep. They are shown, and stopped only when the user
// says so, never by themselves.

func init() {
	registerRequests(StopUnseen{})
}

// UnseenProgram is a program in the holder no tab took up, as a client is
// shown it.
type UnseenProgram struct {
	ID string
	// What is what it was started as: "agent claude of tab parser", say.
	What      string
	Pid       int
	Sandboxed bool
	Started   time.Time
}

// unseenStopGrace is how long a program stopped has, hung up, before it
// is killed.
var unseenStopGrace = 3 * time.Second

// heldUnseen says whether held program p is one no tab takes up: not an
// agent that will be when its tab's agent starts. m.Mu is held.
func (m *Core) heldUnseen(p holder.Prog) bool {
	l, ok := ParseLabel(p.Label)
	return !ok || l.Kind != HeldAgent || l.Agent != "" || m.AgentByID(l.Tab) == nil
}

// NoteUnseen puts in Unseen the programs in the holder no tab takes up,
// for the client to show. m.Mu is held.
func (m *Core) NoteUnseen() {
	m.Unseen = nil
	if m.Held == nil {
		return
	}
	for _, p := range m.Held.Progs {
		if !m.heldUnseen(p) {
			continue
		}
		m.Unseen = append(m.Unseen, UnseenProgram{ID: p.ID, What: m.unseenWhat(p), Pid: p.Pid,
			Sandboxed: p.Sandboxed, Started: time.Unix(0, p.Started)})
	}
	slices.SortFunc(m.Unseen, func(a, b UnseenProgram) int { return a.Started.Compare(b.Started) })
}

// unseenWhat is what held program p was started as, in words. m.Mu is
// held.
func (m *Core) unseenWhat(p holder.Prog) string {
	l, ok := ParseLabel(p.Label)
	if !ok {
		return "a program nerdr did not start"
	}
	tab := "a tab since closed"
	if t := m.TabByID(l.Tab); t != nil {
		tab = "tab " + t.Name
	}
	switch l.Kind {
	case HeldAgent:
		if l.Agent != "" {
			return "a reviewer of " + tab + "'s work"
		}
		return "the agent of " + tab
	case HeldShell:
		return fmt.Sprintf("terminal %d of %s", l.N, tab)
	case HeldPluginUI:
		return "the screen of plugin " + l.Plugin + " in " + tab
	case HeldRun:
		return "a run of plugin " + l.Plugin + " in " + tab
	}
	return l.Kind + " of " + tab
}

// StopUnseen stops program ID of those no tab took up, every one of them
// when ID is "": hung up, then killed after a while, and let go of.
type StopUnseen struct{ ID string }

func (q StopUnseen) Do(m *Core) Cmd {
	if m.Held == nil {
		return nil
	}
	var stop []holder.Prog
	for id, p := range m.Held.Progs {
		if (q.ID == "" || id == q.ID) && m.heldUnseen(p) {
			stop = append(stop, p)
			delete(m.Held.Progs, id)
			delete(m.Held.Checkpoints, id)
		}
	}
	m.NoteUnseen()
	if len(stop) == 0 {
		return nil
	}
	Logf("stopping %s no tab took up", Plural(len(stop), "program"))
	cl := m.Held.Client
	return func() Event {
		var wg sync.WaitGroup
		for _, p := range stop {
			wg.Add(1)
			go func() {
				defer wg.Done()
				stopHeld(cl, p)
			}()
		}
		wg.Wait()
		return nil
	}
}

// stopHeld hangs up held program p, kills it if it is still there after
// unseenStopGrace, and lets go of it, for the holder to reap.
func stopHeld(cl *holder.Client, p holder.Prog) {
	proc := cl.Attach(p, p.Pos)
	exited := make(chan struct{})
	go func() {
		_ = proc.Wait()
		close(exited)
	}()
	_ = proc.Signal(proc.Pid(), syscall.SIGHUP)
	select {
	case <-exited:
	case <-time.After(unseenStopGrace):
		_ = proc.Signal(proc.Pid(), syscall.SIGKILL)
		select {
		case <-exited:
		case <-time.After(unseenStopGrace):
		}
	}
	_ = proc.Close()
}
