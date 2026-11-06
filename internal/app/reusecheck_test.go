package app

import (
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
)

// Every test draws the review panel anew where its last drawing is used
// again, and fails if the two differ; see reuseCheck.
func init() { reuseCheck = true }

// The panel drawn again from its last drawing, on the ticks and as the
// panes draw, is the panel as it is, whichever of its screens is up and
// whatever the keys did before.
func TestReviewReuseStaysCurrent(t *testing.T) {
	m := testModel(t)
	m.reviewOpen = true
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	ticks := []tea.Msg{core.FrameMsg{}, core.DetectMsg{}, core.InfoTickMsg{}}
	for _, k := range []string{"f1", "f2", "f2", "f2", "f3", "f4", "f1", "tab", "down", "up", "enter", "esc"} {
		m.Update(keyMsg(k))
		m.View()
		for _, tick := range ticks {
			m.Update(tick)
			m.View() // panics if the reused panel is out of date
		}
	}
}

// The check itself: a change to the panel that no message told of is
// caught.
func TestReviewReuseCheckCatchesStale(t *testing.T) {
	m := testModel(t)
	m.reviewOpen = true
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m.View()
	rm, _ := m.review.Update(keyMsg("f3")) // behind Update's back
	m.review = rm.(review.Model)
	m.Update(core.FrameMsg{})
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "reused") {
			t.Fatalf("stale panel not caught: %v", r)
		}
	}()
	m.View()
}

// Core changes on its own goroutine, between the client's messages, and
// counts it in Gen: the panel is drawn anew, though the message that follows
// is only a tick. The change here is made to the panel itself, as the check
// above does, standing for one to the state the panel shows.
func TestReviewReuseAfterCoreChanged(t *testing.T) {
	for _, counted := range []bool{true, false} {
		m := testModel(t)
		m.reviewOpen = true
		m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
		m.View()
		m.Mu.Lock()
		rm, _ := m.review.Update(keyMsg("f3"))
		m.review = rm.(review.Model)
		if counted {
			m.Gen++ // as the loop does for each event of core's
		}
		m.Mu.Unlock()
		m.Update(core.RedrawMsg{})
		func() {
			defer func() {
				r := recover()
				if counted && r != nil {
					t.Fatalf("the change was not drawn: %v", r)
				}
				if !counted && r == nil {
					t.Fatal("the check cannot see the change: the test shows nothing")
				}
			}()
			m.View()
		}()
	}
}

// Core changes the panel between Update and View, on its own goroutine: the
// reuse Update allowed is not taken.
func TestReviewReuseCheckedWhereDrawn(t *testing.T) {
	m := testModel(t)
	m.reviewOpen = true
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m.View()
	m.Update(core.FrameMsg{}) // reuse allowed
	m.Mu.Lock()
	rm, _ := m.review.Update(keyMsg("f3"))
	m.review = rm.(review.Model)
	m.Gen++ // as the loop does
	m.Mu.Unlock()
	m.View() // panics if the panel drawn at the Update is reused
}

// Core moving the focus off the panel, as an agent switch does from the
// detection, is drawn: it counts as a change.
func TestCoreChangingTheClientCounts(t *testing.T) {
	m := testModel(t)
	m.reviewOpen = true
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m.Update(keyMsg("f1"))
	m.setFocus(core.FocusReview)
	m.View()
	before := m.Gen
	m.Tell(core.SwitchedMsg{T: m.Ref(m.Cur())}) // core's way, through Client
	if m.Gen == before {
		t.Fatal("core changing the focus did not count")
	}
	m.Update(core.RedrawMsg{})
	m.View() // panics if the panel with the old focus is reused
}

// A tab's name changed by core on its own goroutine, between two Updates,
// widens the sidebar: the agent's program is given the narrower room on the
// next Update, though nothing changed during it.
func TestCoreWideningTheSidebarResizesTheAgent(t *testing.T) {
	m := testModel(t)
	m.sideBar = true
	tb := runningTab(t, m)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	before, _ := tb.Pane.Size()

	m.Mu.Lock()
	tb.Name = strings.Repeat("x", 30) // as FollowBranches does, on the loop
	m.Gen++
	m.Mu.Unlock()
	m.Update(core.ChangedMsg{})

	after, _ := tb.Pane.Size()
	want, _ := m.paneSize(tb)
	if after == before || after != want {
		t.Fatalf("the agent is %d columns after the sidebar widened, was %d, want %d", after, before, want)
	}
}
