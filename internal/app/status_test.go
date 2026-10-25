package app

import (
	"strings"
	"testing"

	"nerdr/internal/hooks"
)

// statusFrom sends `nerdr status` from agent a, and returns nerdr's answer.
func statusFrom(m *Model, a *tab, text string, clear bool) hooks.Answer {
	reply := make(chan hooks.Answer, 1)
	m.onStatus(hooks.Report{Method: hooks.Status, Pane: a.hookKey(), Text: text, Clear: clear, Reply: reply})
	return <-reply
}

// An agent's status is shown above it and in nerdr tabs, told back when
// asked, and cleared.
func TestAgentSetsTheTabsStatus(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	statusFrom(m, tb, "found the cause; writing the fix", false)
	if tb.Status != "found the cause; writing the fix" {
		t.Fatalf("status %q", tb.Status)
	}
	if a := statusFrom(m, tb, "", false); !strings.HasPrefix(a.Text, "found the cause") {
		t.Errorf("asked: %q", a.Text)
	}
	var shown bool
	for _, r := range m.taskRows(tb, 100, 30) {
		shown = shown || r.label == "status:" && strings.Contains(r.text, "found the cause")
	}
	if !shown {
		t.Error("not shown above the agent")
	}
	if j := m.tabsJSON(tb); j[0].Status != tb.Status {
		t.Errorf("nerdr tabs has %q", j[0].Status)
	}
	statusFrom(m, tb, "", true)
	if tb.Status != "" {
		t.Errorf("not cleared: %q", tb.Status)
	}
}
