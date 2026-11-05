package core

import (
	"fmt"
	"strconv"
)

// Neediest is the tab that most wants the user — blocked first, then
// finished, then working, then waiting on a background process, the most
// recent change winning a tie — or nil
// when every agent is idle.
func (m *Core) Neediest() (int, *Tab) {
	best, bi := (*Tab)(nil), -1
	for i, t := range m.Tabs {
		if t.Attention() == 0 {
			continue
		}
		if best == nil || t.Attention() > best.Attention() ||
			(t.Attention() == best.Attention() && t.Changed.After(best.Changed)) {
			best, bi = t, i
		}
	}
	return bi, best
}

// ReportHerdr tells herdr the state of the tab that most needs attention.
// herdr shows one agent per pane, and this pane has all of nerdr's.
func (m *Core) ReportHerdr() {
	if m.Herdr == nil {
		return
	}
	_, t := m.Neediest()
	if t == nil {
		t = m.Cur()
	}
	tokens := m.HerdrTokens()
	if t == nil {
		m.Herdr.Report("unknown", "no tabs", "nerdr", tokens)
		return
	}
	st := "idle"
	switch t.Lit() {
	case StWorking, StBackground: // idle would have herdr tell of it as done
		st = "working"
	case StBlocked:
		st = "blocked"
	case StUnknown, StStarting, StExited, StAsleep:
		st = "unknown"
	}
	working := 0
	for _, x := range m.Tabs {
		if x.Lit() == StWorking && x != t {
			working++
		}
	}
	msg := t.Name + ": " + t.Lit().Label()
	if working > 0 {
		msg += fmt.Sprintf(" · %d more working", working)
	}
	m.Herdr.Report(st, msg, "nerdr · "+t.Name, tokens)
}

// HerdrTokens sum every tab up for herdr's sidebar, which shows one agent
// per pane: $summary is e.g. "5 tabs · 2 working · 1 needs input", and
// $tabs, $working, $blocked and $done are the counts.
func (m *Core) HerdrTokens() map[string]string {
	var working, blocked, done int
	for _, t := range m.Tabs {
		switch t.Lit() {
		case StWorking:
			working++
		case StBlocked:
			blocked++
		case StDone:
			done++
		}
	}
	summary := Plural(len(m.Tabs), "tab")
	if working > 0 {
		summary += fmt.Sprintf(" · %d working", working)
	}
	if blocked > 0 {
		summary += fmt.Sprintf(" · %d need%s input", blocked, map[bool]string{true: "s", false: ""}[blocked == 1])
	}
	if done > 0 {
		summary += fmt.Sprintf(" · %d done", done)
	}
	return map[string]string{
		"summary": summary,
		"tabs":    strconv.Itoa(len(m.Tabs)),
		"working": strconv.Itoa(working),
		"blocked": strconv.Itoa(blocked),
		"done":    strconv.Itoa(done),
	}
}
