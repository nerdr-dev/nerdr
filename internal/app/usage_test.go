package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// TestUsageFromTheStatusLine: what a claude agent's status line reports
// is its context and the account's limits, kept for the next start too,
// and shown in the usage dialog.
func TestUsageFromTheStatusLine(t *testing.T) {
	m := testModel(t)
	yes := true
	m.cfg.ClaudeStatusLine = &yes
	tb := m.cur()
	ctx := 34.0
	reset := time.Now().Add(2 * time.Hour).Unix()
	m.Update(hooks.Report{Method: hooks.Usage, Pane: tb.hookKey(), Limits: &usage.Report{
		Session: &usage.Window{Used: 23, ResetsAt: reset}, Weekly: &usage.Window{Used: 41},
		Context: &ctx, Model: "Opus 5.5",
	}})
	if tb.usage == nil || m.claudeLimits == nil || m.claudeLimits.Session.Used != 23 {
		t.Fatalf("kept: %+v %+v", tb.usage, m.claudeLimits)
	}
	if s, ok := usage.LoadClaude(state.Dir()); !ok || s.Weekly.Used != 41 {
		t.Fatalf("saved: %+v %v", s, ok)
	}
	m.claudeLimits = nil
	m.openUsage()
	if m.claudeLimits == nil {
		t.Fatal("the saved limits should be read when none is known")
	}
	v := ansi.Strip(m.dialog.view(m, 120, 30))
	for _, want := range []string{"Claude", "23%", "41%", "resets", "Opus 5.5", "34%", tb.Name} {
		if !strings.Contains(v, want) {
			t.Fatalf("the dialog should show %q:\n%s", want, v)
		}
	}
}

func TestLimitLine(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	l := ansi.Strip(limitLine("5 hours", &usage.Window{Used: 50, ResetsAt: now.Add(90 * time.Minute).Unix()}, now))
	if !strings.Contains(l, "50%") || !strings.Contains(l, "resets 13:30 (in 1h 30m)") {
		t.Fatalf("line %q", l)
	}
	l = ansi.Strip(limitLine("5 hours", &usage.Window{Used: 50, ResetsAt: now.Add(-time.Minute).Unix()}, now))
	if !strings.Contains(l, "0%") || !strings.Contains(l, "started afresh") {
		t.Fatalf("a window past its reset: %q", l)
	}
}
