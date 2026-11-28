package core

import "time"

// A part made on another machine (see spinoffremote.go) is the TUI's to
// see through, step by step, with cores that do not know each other: the
// tab's core is told only at the end. So that nothing else answers the
// part meanwhile, the API or another client, the TUI has the tab's core
// hold it first, as busy, and lets go of it if the part is not made.

func init() {
	registerRequests(SpinoffHold{})
	registerEvents(SpinoffHeldMsg{})
}

// SpinoffHoldFor is the longest a part is held for a client making it on
// another machine: one that went away, or lost track of it, does not
// keep it from being answered for ever.
const SpinoffHoldFor = 15 * time.Minute

// SpinoffHold has tab T's first part, of the id Part, held for job Job,
// Busy saying what is done about it; the answer is a SpinoffHeldMsg. With
// Release, the hold of Job, if it is still there, is let go of, and
// nothing is told.
type SpinoffHold struct {
	Job     string
	T       TabRef
	Part    string
	Busy    string
	Release bool
}

// SpinoffHeldMsg is SpinoffHold Job's answer: Err is why the part was not
// held, "" when it was.
type SpinoffHeldMsg struct {
	Job string
	Err string
}

func (q SpinoffHold) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if q.Release {
		if t != nil && q.Job != "" && t.SpinoffHold == q.Job {
			t.SpinoffBusy, t.SpinoffHold, t.SpinoffHeldUntil = "", "", time.Time{}
		}
		return nil
	}
	held := func(err string) Cmd { return m.Tell(SpinoffHeldMsg{Job: q.Job, Err: err}) }
	switch {
	case q.Job == "":
		return held("no job to hold the part for")
	case t == nil:
		return held("the tab is gone")
	case t.Spinoff == nil || len(t.Spinoff.Next) == 0 || t.Spinoff.Next[0].ID != q.Part:
		return held("this part is no longer the one asked about")
	case t.SpinoffBusy != "":
		return held("the part is being made already: " + t.SpinoffBusy)
	}
	busy := q.Busy
	if busy == "" {
		busy = "being made on another machine…"
	}
	t.SpinoffBusy, t.SpinoffHold, t.SpinoffHeldUntil, t.SpinoffFailed = busy, q.Job, time.Now().Add(SpinoffHoldFor), nil
	return held("")
}

// ExpireSpinoffHolds lets go of the parts held longer than SpinoffHoldFor.
func (m *Core) ExpireSpinoffHolds(now time.Time) {
	for _, t := range m.Tabs {
		if t.SpinoffHold != "" && now.After(t.SpinoffHeldUntil) {
			t.SpinoffBusy, t.SpinoffHold, t.SpinoffHeldUntil = "", "", time.Time{}
		}
	}
}
