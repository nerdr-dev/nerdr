package core

import (
	"slices"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// StartSpacing is how far apart the agents of the tabs restored at start
// are started, the tab shown first: twenty agents starting at once, each
// reading its conversation and its config, slow the machine down and the
// tab you are looking at with it.
const StartSpacing = 100 * time.Millisecond

// StartLater is cmd run after d, as the agents are started at start.
func StartLater(d time.Duration, cmd Cmd) Cmd {
	if cmd == nil || d <= 0 {
		return cmd
	}
	return func() Event {
		time.Sleep(d)
		return cmd()
	}
}

// HasAuto says whether agent t has a message waiting to be typed in by
// itself, which an agent asleep would never be given.
func HasAuto(t *Tab) bool {
	for _, msg := range t.Messages {
		if msg.Auto {
			return true
		}
	}
	return false
}

// TabHasAuto is hasAuto for any agent of tab t, which sleep together.
func TabHasAuto(t *Tab) bool {
	return slices.ContainsFunc(append([]*Tab{t}, t.ExtraAgents()...), HasAuto)
}

// SleepAfter is how long an agent done and seen is left running out of
// sight; 0 for ever.
func (m *Core) SleepAfter() time.Duration {
	return time.Duration(m.Cfg.SleepAfter) * time.Minute
}

// CanCarryOn says whether agent a, started again, carries on the
// conversation it is in rather than starting afresh: see start.
func (m *Core) CanCarryOn(a *Tab) bool {
	if !slices.Contains(agents.All, a.Agent) {
		// nerdr resumes an agent it does not know only with the command
		// its integration reported, or the config's resume_args: a
		// conversation id alone it has no way to pass on.
		if !a.InSandbox && !m.SandboxedKind(a.Agent) {
			if _, ok := agents.ResumeReported(m.SpecsFor(a), a.Agent, a.ResumeArgv); ok {
				return true
			}
		}
		return len(m.Cfg.Agents[a.Agent].ResumeArgs) > 0 && a.PartOf == nil && !m.SharesKind(a)
	}
	if a.SessionID != "" {
		return true
	}
	if a.PartOf != nil || m.SharesKind(a) {
		return false
	}
	switch a.Agent {
	case agents.Codex, agents.Claude, agents.OpenCode, agents.Cursor, agents.Gemini, agents.Antigravity:
		// Each carries on the worktree's latest conversation; copilot
		// would start afresh.
		return true
	}
	return false // copilot would start afresh
}

// sleepTab stops the agents of t, to be started again when t is visited.
func (m *Core) SleepTab(t *Tab, now time.Time) {
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if old := a.Pane; old != nil {
			a.Pane = nil
			go old.Close()
		}
		a.Light, a.Changed = StAsleep, now
	}
	Debugf("tab %s: agents asleep", t.Name)
}

// wakeTab starts again the agents of t that are asleep, carrying their
// conversations on.
func (m *Core) WakeTab(t *Tab) Cmd {
	if t == nil {
		return nil
	}
	var cmds []Cmd
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if a.Light == StAsleep {
			a.FallbackTried = false
			cmds = append(cmds, m.Start(a, true))
		}
	}
	return Batch(cmds...)
}

// SleepIdle puts to sleep the agents of the tabs that have been done, seen
// and out of sight for sleep_after, and wakes those asleep that have been
// given something to do since.
func (m *Core) SleepIdle(now time.Time) Cmd {
	var cmds []Cmd
	for _, t := range m.Tabs {
		switch {
		case t.Light == StAsleep && (TabHasAuto(t) || m.kept(t)):
			// Given a task since, or on screen by a way track missed.
			cmds = append(cmds, m.WakeTab(t))
		case m.CanSleep(t, now):
			m.SleepTab(t, now)
		}
	}
	return Batch(cmds...)
}

// CanSleep says whether t's agents can be put to sleep now: each done,
// seen and idle for sleep_after, and the tab out of sight as long.
func (m *Core) CanSleep(t *Tab, now time.Time) bool {
	d := m.SleepAfter()
	if d == 0 || m.kept(t) || now.Sub(t.LastActive) < d || now.Sub(t.ShownAt) < d {
		return false
	}
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if a.Pane == nil || a.Light != StIdle || now.Sub(a.Changed) < d || !m.CanCarryOn(a) ||
			a.Review != nil || a.Spinoff != nil || HasAuto(a) || m.Watching(a) {
			return false
		}
		if exited, _, _ := a.Pane.Exited(); exited {
			return false
		}
	}
	return true
}

// kept reports whether the client keeps t in view: in front, or beside it
// though the window may be too narrow to show both.
func (m *Core) kept(t *Tab) bool {
	if m.Client == nil {
		return false
	}
	return t == m.Front() || t == m.ByRef(m.Client.Sight().Beside)
}
