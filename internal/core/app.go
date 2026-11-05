package core

import (
	"strconv"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
)

type FocusArea int

const (
	FocusAgent FocusArea = iota
	FocusReview
)

// ResizeSettle is how long after a resize the size is asked for again.
const ResizeSettle = 300 * time.Millisecond

// PruneOrders drops the reading-order entries and walkthroughs of
// branches that are gone from the repositories of tabs, once at start, so
// the files do not grow for ever and a branch name used again does not
// find them; see order.Prune and walkthrough.Prune.
func PruneOrders(tabs []*Tab) Cmd {
	var paths []string
	for _, t := range tabs {
		for _, w := range t.Worktrees {
			paths = append(paths, w.Path)
		}
	}
	return func() Event {
		done := map[string]bool{}
		for _, p := range paths {
			if f := order.File(p); f != "" && !done[f] {
				done[f] = true
				_, _ = order.Prune(p)
			}
			if f := walkthrough.File(p); f != "" && !done[f] {
				done[f] = true
				_, _ = walkthrough.Prune(p)
			}
		}
		return nil
	}
}

type FrameMsg struct{}

type DetectMsg struct{}

// WaitFrame waits for a pane to draw, then gives it a moment to finish
// the burst so one frame shows the lot.
func WaitFrame(wake chan struct{}) Cmd {
	return func() Event {
		<-wake
		time.Sleep(16 * time.Millisecond)
		return FrameMsg{}
	}
}

func DetectTick() Cmd {
	return Tick(300*time.Millisecond, func(time.Time) Event { return DetectMsg{} })
}

// Cur is the active tab, or nil when there are none.
func (m *Core) Cur() *Tab {
	if m.Active < 0 || m.Active >= len(m.Tabs) {
		return nil
	}
	return m.Tabs[m.Active]
}

// SeenAfter is how long a tab must be the active one before it counts as
// looked at: tabs passed on the way to another, up or down the list, keep
// their green lights.
const SeenAfter = time.Second

// ToastMsg is a toast from work done out of the screen's way.
type ToastMsg string

// StopPanes stops panes side by side, and tells herdr nerdr is gone. It
// does not touch the model.
func StopPanes(panes []*term.Pane, h *herdr.Client) {
	var wg sync.WaitGroup
	for _, p := range panes {
		wg.Add(1)
		go func() { p.Close(); wg.Done() }()
	}
	wg.Wait()
	h.Release()
}

func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// detectAgent is detect for agent a, the own of a tab or another in one
// of its panes; shown is whether its tab is on screen.
func (m *Core) DetectAgent(t *Tab, shown bool, now time.Time) Cmd {
	var cmds []Cmd
	if t.Light == StAsleep {
		return nil // see sleep.go
	}
	visible := m.Client.AgentVisible(t)
	was := t.Light
	t.Classify(visible, now)
	if t.Light != was && !shown {
		switch t.Light {
		case StDone:
			t.Owner().Away.Done++
		case StBlocked:
			t.Owner().Away.Blocked++
		}
	}
	if t.Light == StWorking {
		t.LastActive = now
		t.Owner().LastActive = now
	}
	if t.Light.AtWork() && !was.AtWork() {
		cmds = append(cmds, m.LookUpGoal(t), m.PromptLater(t)) // it has been asked something
		if t.Typed != nil {
			t.Typed.Worked()
		}
	}
	if t.Typed != nil {
		if s := t.Typed.Take(); s != "" {
			t.NoteSaid(s)
			m.SetGoal(t, s)
			m.SetLast(t, s)
		}
	}
	if t.Light != was {
		Debugf("agent %s: %s -> %s (visible %v; %s)", m.Client.AgentName(t), was.Label(), t.Light.Label(), visible, t.Why)
		m.Client.NotifyChange(t, visible, now)
		if was.AtWork() {
			// Whatever the agent did is worth a look in the tab bar.
			t.Owner().LookAgain(false)
		}
	}
	if t.Light == StExited && was != StExited {
		cmds = append(cmds, m.OnExit(t))
	}
	if was.AtWork() && (t.Light == StIdle || t.Light == StDone) {
		m.TellCutOff(t)
		// The log has the turn's prompt by now, whenever it was written.
		m.FindingsDone(t)
		cmds = append(cmds, m.AfterTurn(t), m.LookUpGoal(t), m.RecapLater(t), m.CountSkills(t), m.ClaimSession(t))
	}
	cmds = append(cmds, m.DetectReviewer(t, now))
	m.CheckSent(t, now)
	cmds = append(cmds, m.CheckHandoff(t, now), m.DeliverQueue(t, now))
	if visible && t.Light == StDone {
		t.Seen()
	}
	return Batch(cmds...)
}

// onExit handles an agent that has stopped. One that was resuming an old
// conversation and died at once most likely could not find it (claude
// says so and exits when the conversation was never saved): start a new
// one in its place, once.
func (m *Core) OnExit(t *Tab) Cmd {
	if t.Pane == nil {
		return nil
	}
	_, err, ran := t.Pane.Exited()
	if t.Resumed && !t.FallbackTried && ran < 8*time.Second && err != nil {
		t.FallbackTried = true
		old := t.Pane
		go old.Close()
		t.SessionID, t.ResumeArgv = "", nil
		cmd := m.Start(t, false)
		if t.Pane != nil && t.Pane != old {
			m.Toast(t.Name + ": could not resume, started a new conversation")
			m.Client.SaveSession()
		}
		return cmd
	}
	if err != nil {
		t.Err = err.Error()
	}
	return nil
}

// onReport takes a request or report from an agent.
func (m *Core) OnReport(msg hooks.Report) Cmd {
	msg = m.Vouch(msg)
	if t := m.AgentByHook(msg.Pane); t != nil {
		t.Heard = true // it reaches nerdr, whatever it asks
	}
	if why := m.SandboxRefusal(msg); why != "" {
		Answerer(msg)(hooks.Answer{Err: why})
		return nil
	}
	switch msg.Method {
	case hooks.Spinoff:
		return m.OnSpinoff(msg)
	case hooks.Handoff:
		return m.OnHandoff(msg)
	case hooks.Review:
		return m.OnReview(msg)
	case hooks.RequestReview:
		return m.OnReady(msg)
	case hooks.Message:
		return m.OnMessage(msg)
	case hooks.NewAgent:
		return m.OnNewAgent(msg)
	case hooks.Usage:
		m.OnUsage(msg)
		return nil
	case hooks.Query:
		return m.OnQuery(msg)
	case hooks.Terminal:
		return m.OnTerminal(msg)
	case hooks.Close:
		return m.OnClose(msg)
	case hooks.NewTab:
		return m.OnNewTab(msg)
	case hooks.Status:
		m.OnStatus(msg)
		return nil
	case hooks.Plugin:
		return m.OnPlugin(msg)
	}
	return m.OnHook(msg)
}
