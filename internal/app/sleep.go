package app

import (
	"slices"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"

	tea "github.com/charmbracelet/bubbletea"
)

// Agents asleep. An agent that is done, and has been seen, holds a process
// and a model session in memory for as long as its tab is open, though
// nobody may look at the tab for days. With sleep_after set, nerdr stops
// such an agent once its tab has been out of sight that many minutes, and
// carries its conversation on, as a restart does, when the tab is visited
// again. A tab gone cold is not started at all when nerdr starts, but
// first when it is visited.
//
// The agents of a tab sleep together, and only when each can carry its
// conversation on: an agent beside the tab's own, or one sharing its kind
// with another, that nerdr knows no conversation of would start afresh.
// Terminals are left running: what runs in them (a dev server, a watch)
// is not the agent's to stop.

// startSpacing is how far apart the agents of the tabs restored at start
// are started, the tab shown first: twenty agents starting at once, each
// reading its conversation and its config, slow the machine down and the
// tab you are looking at with it.
const startSpacing = 100 * time.Millisecond

// startLater is cmd run after d, as the agents are started at start.
func startLater(d time.Duration, cmd tea.Cmd) tea.Cmd {
	if cmd == nil || d <= 0 {
		return cmd
	}
	return func() tea.Msg {
		time.Sleep(d)
		return cmd()
	}
}

// restoreStarts starts the agents of the tabs restored at start, spaced
// startSpacing apart, the tab shown first; those of tabs gone cold sleep
// until visited.
func (m *Model) restoreStarts() []tea.Cmd {
	now := time.Now()
	order := make([]*tab, 0, len(m.tabs))
	if t := m.cur(); t != nil {
		order = append(order, t)
	}
	for _, t := range m.tabs {
		if t != m.cur() {
			order = append(order, t)
		}
	}
	var cmds []tea.Cmd
	n := 0
	for _, t := range order {
		if t != m.cur() && t != m.splitTab() && m.cold(t, now) && !tabHasAuto(t) {
			for _, a := range append([]*tab{t}, t.extraAgents()...) {
				a.status = stAsleep
			}
			cmds = append(cmds, m.lookUpGoal(t), m.countSkills(t))
			continue
		}
		cmds = append(cmds, startLater(time.Duration(n)*startSpacing, m.start(t, true)), m.lookUpGoal(t), m.countSkills(t))
		n++
		for _, a := range t.extraAgents() {
			cmds = append(cmds, startLater(time.Duration(n)*startSpacing, m.start(a, true)), m.lookUpGoal(a))
			n++
		}
	}
	return cmds
}

// hasAuto says whether agent t has a message waiting to be typed in by
// itself, which an agent asleep would never be given.
func hasAuto(t *tab) bool {
	for _, msg := range t.Messages {
		if msg.Auto {
			return true
		}
	}
	return false
}

// tabHasAuto is hasAuto for any agent of tab t, which sleep together.
func tabHasAuto(t *tab) bool {
	return slices.ContainsFunc(append([]*tab{t}, t.extraAgents()...), hasAuto)
}

// sleepAfter is how long an agent done and seen is left running out of
// sight; 0 for ever.
func (m *Model) sleepAfter() time.Duration {
	return time.Duration(m.cfg.SleepAfter) * time.Minute
}

// sleepIdle puts to sleep the agents of the tabs that have been done, seen
// and out of sight for sleep_after, and wakes those asleep that have been
// given something to do since.
func (m *Model) sleepIdle(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		switch {
		case t.status == stAsleep && (tabHasAuto(t) || t == m.cur() || t == m.splitTab()):
			// Given a task since, or on screen by a way track missed.
			cmds = append(cmds, m.wakeTab(t))
		case m.canSleep(t, now):
			m.sleepTab(t, now)
		}
	}
	return tea.Batch(cmds...)
}

// canSleep says whether t's agents can be put to sleep now: each done,
// seen and idle for sleep_after, and the tab out of sight as long.
func (m *Model) canSleep(t *tab, now time.Time) bool {
	d := m.sleepAfter()
	if d == 0 || t == m.cur() || t == m.splitTab() || now.Sub(t.LastActive) < d || now.Sub(t.shownAt) < d {
		return false
	}
	for _, a := range append([]*tab{t}, t.extraAgents()...) {
		if a.pane == nil || a.status != stIdle || now.Sub(a.changed) < d || !m.canCarryOn(a) ||
			a.review != nil || a.Spinoff != nil || hasAuto(a) || m.watching(a) {
			return false
		}
		if exited, _, _ := a.pane.Exited(); exited {
			return false
		}
	}
	return true
}

// canCarryOn says whether agent a, started again, carries on the
// conversation it is in rather than starting afresh: see start.
func (m *Model) canCarryOn(a *tab) bool {
	if !slices.Contains(agents.All, a.Agent) {
		// nerdr resumes an agent it does not know only with the command
		// its integration reported, or the config's resume_args: a
		// conversation id alone it has no way to pass on.
		if _, ok := agents.ResumeReported(m.specsFor(a), a.Agent, a.ResumeArgv); ok {
			return true
		}
		return len(m.cfg.Agents[a.Agent].ResumeArgs) > 0 && a.partOf == nil && !m.sharesKind(a)
	}
	if a.SessionID != "" {
		return true
	}
	if a.partOf != nil || m.sharesKind(a) {
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
func (m *Model) sleepTab(t *tab, now time.Time) {
	for _, a := range append([]*tab{t}, t.extraAgents()...) {
		if old := a.pane; old != nil {
			a.pane = nil
			go old.Close()
		}
		a.status, a.changed = stAsleep, now
	}
	debugf("tab %s: agents asleep", t.Name)
}

// wakeTab starts again the agents of t that are asleep, carrying their
// conversations on.
func (m *Model) wakeTab(t *tab) tea.Cmd {
	if t == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, a := range append([]*tab{t}, t.extraAgents()...) {
		if a.status == stAsleep {
			a.fallbackTried = false
			cmds = append(cmds, m.start(a, true))
		}
	}
	return tea.Batch(cmds...)
}
