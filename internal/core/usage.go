package core

import (
	"fmt"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// onUsage keeps what a claude agent's status line said: its context, and
// its account's limits, which are every claude agent's.
func (m *Core) OnUsage(r hooks.Report) {
	a := m.AgentByHook(r.Pane)
	if a == nil || r.Limits == nil {
		return
	}
	now := time.Now()
	a.Usage, a.UsageAt = r.Limits, now
	if r.Limits.Session == nil && r.Limits.Weekly == nil {
		return
	}
	changed := m.ClaudeLimits == nil || !SameLimits(m.ClaudeLimits, r.Limits)
	m.ClaudeLimits, m.ClaudeAt = r.Limits, now
	if changed {
		usage.SaveClaude(state.Dir(), usage.Saved{Report: *r.Limits, At: now})
	}
}

func SameLimits(a, b *usage.Report) bool {
	same := func(x, y *usage.Window) bool {
		return x == nil && y == nil || x != nil && y != nil && *x == *y
	}
	return same(a.Session, b.Session) && same(a.Weekly, b.Weekly)
}

// resetTime is when t is, said shortly from now: the time today, else
// the day and time.
func ResetTime(t, now time.Time) string {
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// left is a time to come, said shortly: "2h 10m", "3d 4h".
func Left(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm", max(int(d/time.Minute), 1))
}

// ago is a time gone, said shortly: "just now", "5 min ago".
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
}
