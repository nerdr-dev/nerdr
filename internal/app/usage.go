package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/usage"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The usage dialog, the prefix and u: how much of the agents' accounts'
// rate limits is used, and when each starts afresh, and how full each
// agent's context window is. It shows what the agents themselves say:
// claude through nerdr's status line (hooks.StatusLineCommand), codex in
// its logs. See package usage.

// usageDialog shows the accounts' limits and the agents' context windows.
// Codex's are read from its logs when it opens, and again on r; it is
// drawn again with every frame, so the times count down. Any other key
// closes it.
type usageDialog struct {
	codex     *usage.Report
	codexAt   time.Time
	reading   bool
	contexts  map[*core.Tab]*usage.Report // codex agents', from their logs
	firstRead bool
	job       string // the read asked of core last
}

func (m *Model) openUsage() tea.Cmd {
	if m.home().Core.ClaudeLimits == nil {
		m.do(core.ReadClaudeLimits{})
	}
	d := &usageDialog{}
	m.dialog = d
	return m.readUsage(d)
}

// readUsage has core read codex's logs for d, where codex runs.
func (m *Model) readUsage(d *usageDialog) tea.Cmd {
	d.reading = true
	q := core.ReadUsage{}
	for _, a := range m.fleet.AllAgents() {
		if a.Agent == agents.Codex && a.SessionID != "" {
			q.Sessions = append(q.Sessions, core.AgentSession{A: m.fleet.Ref(a), Session: a.SessionID})
		}
	}
	m.usageJobs++
	d.job = "usage-" + strconv.Itoa(m.usageJobs)
	q.Job = d.job
	return m.do(q)
}

// onUsage takes what core read for the usage dialog's read asked last.
func (m *Model) onUsage(ev core.UsageMsg) {
	d, ok := m.dialog.(*usageDialog)
	if !ok || ev.Job != d.job {
		return
	}
	d.reading, d.firstRead = false, true
	d.codex, d.codexAt, d.contexts = ev.Codex, ev.At, map[*core.Tab]*usage.Report{}
	for _, c := range ev.Contexts {
		if a := m.fleet.ByRef(c.A); a != nil {
			r := c.R
			d.contexts[a] = &r
		}
	}
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
		lines = append(lines, head+dim("  "+from+", "+core.Ago(now.Sub(at))))
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
	case !core.IsOn(m.home().Core.Cfg.ClaudeStatusLine):
		account("Claude", "not known: claude_status_line is off", nil, time.Time{})
	case m.home().Core.ClaudeLimits == nil:
		account("Claude", "not known yet: a claude agent tells it after its first answer", nil, time.Time{})
	default:
		account("Claude", "from claude's status line", m.home().Core.ClaudeLimits, m.home().Core.ClaudeAt)
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
	for _, t := range m.fleet.Tabs() {
		for _, a := range t.AgentsIn() {
			r := a.Usage
			if a.Agent == agents.Codex {
				r = d.contexts[a]
			}
			if r == nil || r.Context == nil {
				continue
			}
			name := t.Name
			if a != t {
				if p := a.PaneOf(); p != nil {
					name += " · " + m.machineOf(t).Core.PaneName(t, p)
				}
			}
			all = append(all, row{name, string(a.Agent), r})
			nameW = max(nameW, len([]rune(name)))
		}
	}
	nameW = min(nameW, 34)
	for _, r := range all {
		line := padTo(core.TruncName(r.name, nameW), nameW+2) + bar(*r.r.Context, 12) + fmt.Sprintf(" %3d%%", int(math.Round(*r.r.Context)))
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
		note = dim("  resets " + core.ResetTime(resets, now) + " (in " + core.Left(resets.Sub(now)) + ")")
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
