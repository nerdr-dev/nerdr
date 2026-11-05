package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nerdr-dev/nerdr/internal/core"
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

// restoreStarts starts the agents of the tabs restored at start, spaced
// startSpacing apart, the tab shown first; those of tabs gone cold sleep
// until visited.
func (m *Model) restoreStarts() []tea.Cmd {
	now := time.Now()
	order := make([]*core.Tab, 0, len(m.Tabs))
	if t := m.Cur(); t != nil {
		order = append(order, t)
	}
	for _, t := range m.Tabs {
		if t != m.Cur() {
			order = append(order, t)
		}
	}
	var cmds []tea.Cmd
	n := 0
	for _, t := range order {
		if t != m.Cur() && t != m.splitTab() && m.Cold(t, now) && !core.TabHasAuto(t) {
			for _, a := range append([]*core.Tab{t}, t.ExtraAgents()...) {
				a.Light = core.StAsleep
			}
			cmds = append(cmds, fromCore(m.LookUpGoal(t)), fromCore(m.CountSkills(t)))
			continue
		}
		cmds = append(cmds, fromCore(core.StartLater(time.Duration(n)*core.StartSpacing, toCore(fromCore(m.Start(t, true))))), fromCore(m.LookUpGoal(t)), fromCore(m.CountSkills(t)))
		n++
		for _, a := range t.ExtraAgents() {
			cmds = append(cmds, fromCore(core.StartLater(time.Duration(n)*core.StartSpacing, toCore(fromCore(m.Start(a, true))))), fromCore(m.LookUpGoal(a)))
			n++
		}
	}
	return cmds
}

// sleepIdle puts to sleep the agents of the tabs that have been done, seen
// and out of sight for sleep_after, and wakes those asleep that have been
// given something to do since.
func (m *Model) sleepIdle(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.Tabs {
		switch {
		case t.Light == core.StAsleep && (core.TabHasAuto(t) || t == m.Cur() || t == m.splitTab()):
			// Given a task since, or on screen by a way track missed.
			cmds = append(cmds, fromCore(m.WakeTab(t)))
		case m.canSleep(t, now):
			m.SleepTab(t, now)
		}
	}
	return tea.Batch(cmds...)
}

// canSleep says whether t's agents can be put to sleep now: each done,
// seen and idle for sleep_after, and the tab out of sight as long.
func (m *Model) canSleep(t *core.Tab, now time.Time) bool {
	d := m.SleepAfter()
	if d == 0 || t == m.Cur() || t == m.splitTab() || now.Sub(t.LastActive) < d || now.Sub(t.ShownAt) < d {
		return false
	}
	for _, a := range append([]*core.Tab{t}, t.ExtraAgents()...) {
		if a.Pane == nil || a.Light != core.StIdle || now.Sub(a.Changed) < d || !m.CanCarryOn(a) ||
			a.Review != nil || a.Spinoff != nil || core.HasAuto(a) || m.Watching(a) {
			return false
		}
		if exited, _, _ := a.Pane.Exited(); exited {
			return false
		}
	}
	return true
}
