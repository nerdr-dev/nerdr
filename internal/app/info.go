package app

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Each tab's branches are looked at in the background: how much they
// change, how far behind main they are, their pull request and its CI, and
// which files they change — so the tab bar can say where each tab stands,
// and warn when two tabs change the same files and are heading for a merge
// conflict.

// details is the short state of a tab for the tab bar, e.g.
// "+142−37 ↓3 #12✗ ⚠ ✉1". The lines added and deleted are for the active
// tab only: on every tab they make the row long, and say little about who
// needs you.
func (m *Model) details(t *core.Tab, bg lipgloss.Color, active bool) string {
	st := func(c string) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(c)) }
	var parts []string
	if n := len(t.Panes()) - 1; n > 0 {
		parts = append(parts, m.tip(st("114").Render("⧉"+strconv.Itoa(n)), core.Plural(n, "pane")+" beside the agent: terminals, agents, reviews"))
	}
	if t.Info.Known() {
		add, del, _, behind := t.Info.Sums()
		if active && add+del > 0 {
			parts = append(parts, m.tip(st("71").Render("+"+strconv.Itoa(add))+st("167").Render("−"+strconv.Itoa(del)),
				"Lines added and removed on the branch"))
		}
		if behind > 0 {
			parts = append(parts, m.tip(st("179").Render("↓"+strconv.Itoa(behind)),
				core.Plural(behind, "commit")+" on the base branch not in this one"))
		}
		if t.Info.Merged() {
			parts = append(parts, m.tip(st("141").Render("✓merged"), "The branch is merged"))
		} else if prs := t.Info.Prs(); len(prs) > 0 {
			s := "#" + strconv.Itoa(prs[0].Number)
			tip := "Pull request #" + strconv.Itoa(prs[0].Number)
			switch {
			case prs[0].After > 0:
				// The branch went on after it: what is on top is in no
				// pull request.
				s += " " + strings.ToLower(prs[0].State) + "·" + strconv.Itoa(prs[0].After) + " after"
				tip += ", " + strings.ToLower(prs[0].State) + " · " + core.Plural(prs[0].After, "commit") +
					" after it, in no pull request"
			case prs[0].State == "CLOSED":
				s += " closed"
				tip += ", closed"
			}
			s = st("110").Render(s)
			switch t.Info.Ci() {
			case "pass":
				s += st("71").Render("✓")
				tip += " · checks passed"
			case "fail":
				s += st("196").Render("✗")
				tip += " · checks failed"
			case "pending":
				s += st("220").Render("…")
				tip += " · checks running"
			}
			if active {
				// The active tab's, as a link: on another tab a click
				// goes to the tab. The other buttons do what they do on
				// the tab.
				tip += " · click to open it in the browser"
				s = m.zone(s, func(m *Model, ev tea.MouseMsg) tea.Cmd {
					if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
						m.openTabPRs()
						return nil
					}
					return m.tabMouse(t, ev)
				})
			}
			parts = append(parts, m.tip(s, tip))
		}
	}
	if len(m.machineOf(t).Core.Overlaps[t.ID]) > 0 {
		parts = append(parts, m.tip(st("214").Render("⚠"), "Other tabs change some of the same files"))
	}
	if n := len(t.TaskRefs()); n > 0 {
		s := "✉" + strconv.Itoa(n)
		tip := core.Plural(n, "task") + " waiting to be sent to the agent"
		if len(t.ExtraAgents()) > 0 {
			tip = core.Plural(n, "task") + " waiting to be sent to the tab's agents"
		}
		if gone := len(t.Inactive); gone > 0 {
			tip += " · " + strconv.Itoa(gone) + " for a closed agent"
		}
		if t.QueuePaused {
			s += "⏸"
			tip += " · paused: the agent did not take the last one up"
		}
		parts = append(parts, m.tip(st("81").Render(s), tip))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, st("240").Render(" "))
}
