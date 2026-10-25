package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/usage"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The usage dialog, the prefix and u: how much of the agents' accounts'
// rate limits is used, and when each starts afresh, and how full each
// agent's context window is. It shows what the agents themselves say:
// claude through nerdr's status line (hooks.StatusLineCommand), codex in
// its logs. See package usage.

// onUsage keeps what a claude agent's status line said: its context, and
// its account's limits, which are every claude agent's.
func (m *Model) onUsage(r hooks.Report) {
	a := m.agentByHook(r.Pane)
	if a == nil || r.Limits == nil {
		return
	}
	now := time.Now()
	a.usage, a.usageAt = r.Limits, now
	if r.Limits.Session == nil && r.Limits.Weekly == nil {
		return
	}
	changed := m.claudeLimits == nil || !sameLimits(m.claudeLimits, r.Limits)
	m.claudeLimits, m.claudeAt = r.Limits, now
	if changed {
		usage.SaveClaude(state.Dir(), usage.Saved{Report: *r.Limits, At: now})
	}
}

func sameLimits(a, b *usage.Report) bool {
	same := func(x, y *usage.Window) bool {
		return x == nil && y == nil || x != nil && y != nil && *x == *y
	}
	return same(a.Session, b.Session) && same(a.Weekly, b.Weekly)
}

// usageDialog shows the accounts' limits and the agents' context windows.
// Codex's are read from its logs when it opens, and again on r; it is
// drawn again with every frame, so the times count down. Any other key
// closes it.
type usageDialog struct {
	codex     *usage.Report
	codexAt   time.Time
	reading   bool
	contexts  map[*tab]*usage.Report // codex agents', from their logs
	firstRead bool
}

// usageReadMsg is what codex's logs say: its account's limits, and the
// context of each codex agent.
type usageReadMsg struct {
	d        *usageDialog
	codex    *usage.Report
	at       time.Time
	contexts map[*tab]*usage.Report
}

func (m *Model) openUsage() tea.Cmd {
	if m.claudeLimits == nil {
		if s, ok := usage.LoadClaude(state.Dir()); ok {
			r := s.Report
			m.claudeLimits, m.claudeAt = &r, s.At
		}
	}
	d := &usageDialog{}
	m.dialog = d
	return m.readUsage(d)
}

// readUsage reads codex's logs for d, in the background.
func (m *Model) readUsage(d *usageDialog) tea.Cmd {
	d.reading = true
	logs := map[*tab]string{}
	for _, a := range m.allAgents() {
		if a.Agent == agents.Codex && a.SessionID != "" {
			logs[a] = a.SessionID
		}
	}
	return func() tea.Msg {
		msg := usageReadMsg{d: d, contexts: map[*tab]*usage.Report{}}
		if r, at, ok := usage.Codex(agents.CodexHome()); ok {
			msg.codex, msg.at = &r, at
		}
		for a, id := range logs {
			if path := agents.CodexLog(id); path != "" {
				if r, ok := usage.CodexLog(path); ok {
					msg.contexts[a] = &r
				}
			}
		}
		return msg
	}
}

func (m *Model) onUsageRead(msg usageReadMsg) {
	d := msg.d
	d.reading, d.firstRead = false, true
	d.codex, d.codexAt, d.contexts = msg.codex, msg.at, msg.contexts
}

func (d *usageDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "r" {
		return m.readUsage(d)
	}
	m.dialog = nil
	return nil
}

func (d *usageDialog) view(m *Model, w, h int) string {
	now := time.Now()
	var lines []string
	account := func(name, from string, r *usage.Report, at time.Time) {
		head := lipgloss.NewStyle().Bold(true).Render(name)
		if r != nil && r.Plan != "" {
			head += dim(" · " + r.Plan)
		}
		if r == nil {
			lines = append(lines, head, dim("  "+from), "")
			return
		}
		lines = append(lines, head+dim("  "+from+", "+ago(now.Sub(at))))
		for _, win := range []struct {
			name string
			w    *usage.Window
		}{{"5 hours", r.Session}, {"week", r.Weekly}} {
			if win.w != nil {
				lines = append(lines, "  "+limitLine(win.name, win.w, now))
			}
		}
		lines = append(lines, "")
	}
	switch {
	case !isOn(m.cfg.ClaudeStatusLine):
		account("Claude", "not known: claude_status_line is off", nil, time.Time{})
	case m.claudeLimits == nil:
		account("Claude", "not known yet: a claude agent tells it after its first answer", nil, time.Time{})
	default:
		account("Claude", "from claude's status line", m.claudeLimits, m.claudeAt)
	}
	switch {
	case d.codex != nil:
		account("Codex", "from codex's log", d.codex, d.codexAt)
	case !d.firstRead:
		account("Codex", "reading codex's logs…", nil, time.Time{})
	default:
		account("Codex", "no codex log with its limits", nil, time.Time{})
	}

	var rows []string
	nameW := 0
	type row struct {
		name, kind string
		r          *usage.Report
	}
	var all []row
	for _, t := range m.tabs {
		for _, a := range t.agentsIn() {
			r := a.usage
			if a.Agent == agents.Codex {
				r = d.contexts[a]
			}
			if r == nil || r.Context == nil {
				continue
			}
			name := t.Name
			if a != t {
				if p := a.paneOf(); p != nil {
					name += " · " + m.paneName(t, p)
				}
			}
			all = append(all, row{name, string(a.Agent), r})
			nameW = max(nameW, len([]rune(name)))
		}
	}
	nameW = min(nameW, 34)
	for _, r := range all {
		line := padTo(truncName(r.name, nameW), nameW+2) + bar(*r.r.Context, 12) + fmt.Sprintf(" %3d%%", int(math.Round(*r.r.Context)))
		if r.r.Model != "" {
			line += dim("  " + r.r.Model)
		} else {
			line += dim("  " + r.kind)
		}
		rows = append(rows, line)
	}
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Context window of each agent"))
	if len(rows) == 0 {
		lines = append(lines, dim("  none known yet: claude tells it in its status line, codex in its log"))
	}
	for _, r := range rows {
		lines = append(lines, "  "+r)
	}
	lines = append(lines, "", m.keys("r", "read codex's logs again", "esc", "close"))
	return box("Usage and rate limits", lines, w)
}

// limitLine is one window of an account's limits: a bar, the share used
// and when it starts afresh.
func limitLine(name string, w *usage.Window, now time.Time) string {
	used := w.Used
	resets := w.Resets()
	note := ""
	switch {
	case resets.IsZero():
	case !resets.After(now):
		used, note = 0, dim("  started afresh "+resets.Format("15:04"))
	default:
		note = dim("  resets " + resetTime(resets, now) + " (in " + left(resets.Sub(now)) + ")")
	}
	return padTo(name, 9) + bar(used, 20) + fmt.Sprintf(" %3d%%", int(math.Round(used))) + note
}

// bar draws pct percent as a bar w wide, coloured by how near full it is.
func bar(pct float64, w int) string {
	n := int(math.Round(min(max(pct, 0), 100) / 100 * float64(w)))
	color := lipgloss.Color("71")
	switch {
	case pct >= 90:
		color = lipgloss.Color("203")
	case pct >= 70:
		color = lipgloss.Color("214")
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", n)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("░", w-n))
}

// resetTime is when t is, said shortly from now: the time today, else
// the day and time.
func resetTime(t, now time.Time) string {
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// left is a time to come, said shortly: "2h 10m", "3d 4h".
func left(d time.Duration) string {
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
func ago(d time.Duration) string {
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
