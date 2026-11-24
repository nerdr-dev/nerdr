package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// notifyModel is a model with tabs behind a tab of its own in front.
func notifyModel(notify string, tabs ...*core.Tab) *Model {
	front := doneTab("front")
	front.Light = core.StIdle
	m := &Model{fleet: fleet.New(&fleet.Machine{Core: core.Core{Tabs: append([]*core.Tab{front}, tabs...)}})}
	m.home().Core.Client = m
	m.home().Core.Cfg.Notify = notify
	m.home().Core.Cfg.Prefix = "ctrl+q"
	return m
}

func doneTab(name string) *core.Tab {
	return &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: name, Name: name, Agent: agents.Claude}, Light: core.StDone}}
}

// In nerdr, a tab behind the one shown says it is done in the bar, not
// on the desktop; the one shown says nothing, being on screen.
func TestBackgroundTabSaysSoInTheBar(t *testing.T) {
	shown, behind := doneTab("shown"), doneTab("behind")
	m := notifyModel("all", shown, behind)
	now := time.Now()
	m.notifyChange(shown, true, now)
	m.notifyChange(behind, false, now)
	m.releaseDone(now.Add(core.HoldDone))
	m.flushNotes()
	if len(m.notes) != 0 {
		t.Fatalf("desktop notes while in nerdr: %+v", m.notes)
	}
	if !strings.Contains(m.toastText, "behind: claude is done") || strings.Contains(m.toastText, "shown") ||
		!strings.Contains(m.toastText, "ctrl+q n") || !m.toastUntil.After(now) {
		t.Fatalf("bar: %q", m.toastText)
	}
}

// Two in one tick share the bar.
func TestBackgroundTabsShareTheBar(t *testing.T) {
	a, b := doneTab("a"), doneTab("b")
	b.Light = core.StBlocked
	m := notifyModel("all", a, b)
	now := time.Now()
	m.notifyChange(a, false, now)
	m.notifyChange(b, false, now)
	m.releaseDone(now.Add(core.HoldDone))
	m.flushNotes()
	if !strings.Contains(m.toastText, "a: claude is done") || !strings.Contains(m.toastText, "b: claude needs input") {
		t.Fatalf("bar: %q", m.toastText)
	}
}

// Away from nerdr it is the desktop, for the tab shown too.
func TestAwayItIsTheDesktop(t *testing.T) {
	shown := doneTab("shown")
	m := notifyModel("all", shown)
	m.blurred = true
	now := time.Now()
	m.notifyChange(shown, false, now)
	m.releaseDone(now.Add(core.HoldDone))
	if len(m.notes) != 1 || len(m.inApp) != 0 {
		t.Fatalf("notes %+v, in nerdr %q", m.notes, m.inApp)
	}
}

// notify = "off" turns the bar's news off too, and "blocked" leaves done out.
func TestNotifySettingCoversTheBar(t *testing.T) {
	for _, c := range []struct {
		notify string
		st     core.Status
		want   bool
	}{{"off", core.StDone, false}, {"blocked", core.StDone, false}, {"blocked", core.StBlocked, true}, {"done", core.StDone, true}} {
		tb := doneTab("x")
		tb.Light = c.st
		m := notifyModel(c.notify, tb)
		now := time.Now()
		m.notifyChange(tb, false, now)
		m.releaseDone(now.Add(core.HoldDone))
		if got := len(m.inApp) == 1; got != c.want {
			t.Errorf("notify %q, %s: %v", c.notify, c.st.Label(), got)
		}
	}
}

// News of an agent done is held a moment, and then for as long as the
// work in its tab goes on: one done is told of at once no more.
func TestDoneIsHeldAMoment(t *testing.T) {
	a := doneTab("a")
	m := notifyModel("all", a)
	now := time.Now()
	m.notifyChange(a, false, now)
	m.releaseDone(now)
	m.flushNotes()
	if m.toastText != "" {
		t.Fatalf("told at once: %q", m.toastText)
	}
	m.releaseDone(now.Add(core.HoldDone))
	m.flushNotes()
	if !strings.Contains(m.toastText, "a: claude is done") {
		t.Fatalf("bar: %q", m.toastText)
	}
}

// helperIn puts an agent beside t in its tab, with light st.
func helperIn(t *core.Tab, st core.Status) *core.Tab {
	h := doneTab(t.ID + "-helper")
	h.Light, h.PartOf = st, t
	t.PaneList = []*core.PaneTab{{Kind: core.PaneAgent, A: t}, {Kind: core.PaneAgent, A: h}}
	return h
}

// handedOn says whether news of a done, in a tab where work was handed on
// by set, waits while the work goes on, and comes once over is called.
func handedOn(t *testing.T, set func(a *core.Tab) (over func())) {
	t.Helper()
	a := doneTab("a")
	over := set(a)
	m := notifyModel("all", a)
	now := time.Now()
	m.notifyChange(a, false, now)
	m.releaseDone(now.Add(core.HoldDone))
	m.flushNotes()
	if m.toastText != "" {
		t.Fatalf("told while the work goes on: %q", m.toastText)
	}
	if over == nil {
		return
	}
	over()
	m.releaseDone(now.Add(2 * core.HoldDone))
	m.flushNotes()
	if !strings.Contains(m.toastText, "a: claude is done") {
		t.Fatalf("not told once the work was over: %q", m.toastText)
	}
}

// The agent asked for a review and its turn ended: the look at the
// worktree that starts the review is on its way, the review not yet
// there.
func TestDoneHeldForTheLookThatStartsAReview(t *testing.T) {
	handedOn(t, func(a *core.Tab) func() {
		a.ReviewLook = true
		return func() { a.ReviewLook = false } // nothing to review
	})
}

// A review under way, its reviewer still starting.
func TestDoneHeldForAReview(t *testing.T) {
	handedOn(t, func(a *core.Tab) func() {
		r := doneTab("reviewer")
		r.Light = core.StStarting
		a.Review = &core.ReviewRun{Phase: core.Reviewing, Seats: []*core.Seat{{R: r}}}
		return nil // its end is told as the review's news: see below
	})
}

// Another agent in the tab at work.
func TestDoneHeldForAnotherAgentAtWork(t *testing.T) {
	handedOn(t, func(a *core.Tab) func() {
		h := helperIn(a, core.StWorking)
		return func() { h.Light = core.StIdle }
	})
}

// A message for another agent in the tab, waiting to be typed in, or
// typed in and not yet taken up: it is idle, but not for long.
func TestDoneHeldForAMessageOnItsWay(t *testing.T) {
	handedOn(t, func(a *core.Tab) func() {
		h := helperIn(a, core.StIdle)
		h.Messages = []state.Message{{Text: "over to you", Auto: true}}
		return func() { h.Messages = nil; h.QueuePaused = false }
	})
	handedOn(t, func(a *core.Tab) func() {
		h := helperIn(a, core.StDone)
		h.AwaitWork = true
		return func() { h.AwaitWork = false }
	})
}

// A queue paused waits for the user, not for the agent: no work goes on.
func TestPausedQueueHoldsNothing(t *testing.T) {
	a := doneTab("a")
	h := helperIn(a, core.StIdle)
	h.Messages, h.QueuePaused = []state.Message{{Text: "over to you", Auto: true}}, true
	if a.WorkGoesOn() {
		t.Fatal("a paused queue holds the news up")
	}
}

// The work held for ends otherwise than done: the agent at it exits, or
// is closed. The news of the one done before comes then.
func TestDoneToldWhenTheWorkEndsOtherwise(t *testing.T) {
	handedOn(t, func(a *core.Tab) func() {
		h := helperIn(a, core.StStarting)
		return func() { h.Light = core.StExited }
	})
	handedOn(t, func(a *core.Tab) func() {
		helperIn(a, core.StWorking)
		return func() { a.PaneList = a.PaneList[:1] }
	})
}

// A review's news takes the place of the news held of the agent done.
func TestReviewNewsTakesThePlaceOfDone(t *testing.T) {
	a := doneTab("a")
	a.ReviewLook = true
	m := notifyModel("all", a)
	now := time.Now()
	m.notifyChange(a, false, now)
	m.notifyReview(a, "approved")
	a.ReviewLook = false
	m.releaseDone(now.Add(core.HoldDone))
	m.flushNotes()
	if strings.Contains(m.toastText, "is done") || !strings.Contains(m.toastText, "review approved") {
		t.Fatalf("bar: %q", m.toastText)
	}
}

// Seen, or at work again, while held: nothing to tell.
func TestHeldDoneSeenIsLetGo(t *testing.T) {
	a := doneTab("a")
	m := notifyModel("all", a)
	now := time.Now()
	m.notifyChange(a, false, now)
	a.Light = core.StIdle
	m.releaseDone(now.Add(core.HoldDone))
	m.flushNotes()
	if m.toastText != "" || len(m.heldDone) != 0 {
		t.Fatalf("bar %q, held %v", m.toastText, m.heldDone)
	}
}
