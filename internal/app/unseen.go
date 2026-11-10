package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
)

// openUnseen lists the programs in the holders that no tab took up, each
// to be stopped, or all of a machine's; none is stopped without the user.
// With several machines, each machine's are under its name.
func (m *Model) openUnseen() tea.Cmd {
	several := len(m.fleet.Machines()) > 1
	items := []menuItem{{label: "Programs from before nerdr started again, in no tab"}}
	found := false
	for _, mc := range m.fleet.Live() {
		unseen := mc.Core.Unseen
		if len(unseen) == 0 {
			continue
		}
		found = true
		where := ""
		if several {
			where = " on " + machineLabel(mc)
			items = append(items, menuItem{label: machineLabel(mc) + ":"})
		}
		for _, u := range unseen {
			items = append(items, menuItem{label: unseenLine(u, time.Now()), run: func(m *Model) tea.Cmd {
				return m.askStopUnseen(mc, u.ID, "Stop "+u.What+where+"?", []string{unseenLine(u, time.Now())})
			}})
		}
		if len(unseen) > 1 {
			key := "a"
			if several {
				key = ""
			}
			items = append(items, menuItem{label: fmt.Sprintf("Stop all %d%s", len(unseen), where), key: key, run: func(m *Model) tea.Cmd {
				return m.askStopUnseen(mc, "", fmt.Sprintf("Stop all %d programs in no tab%s?", len(unseen), where), nil)
			}})
		}
	}
	if !found {
		m.toast("no programs run outside the tabs")
		return nil
	}
	m.dialog = &menu{title: "Programs in no tab", items: items, x: m.width / 4, y: m.height / 4, hi: 1}
	return nil
}

// askStopUnseen asks before stopping program id of mc's holder, all of
// them for "".
func (m *Model) askStopUnseen(mc *fleet.Machine, id, title string, lines []string) tea.Cmd {
	m.dialog = &confirm{
		title:     title,
		lines:     append(lines, "It is hung up, and killed if it is still there after a few seconds."),
		defaultNo: true,
		yesLabel:  "Stop", noLabel: "Cancel",
		yes: func(m *Model) tea.Cmd { return m.doOn(mc, core.StopUnseen{ID: id}) },
	}
	return nil
}

// unseenLine is u in a line: what it is, its process, its sandbox and
// how long it has run.
func unseenLine(u core.UnseenProgram, now time.Time) string {
	s := fmt.Sprintf("%s · pid %d", u.What, u.Pid)
	if u.Sandboxed {
		s += " · in a sandbox"
	} else {
		s += " · not in a sandbox"
	}
	if !u.Started.IsZero() {
		s += " · " + ago(now.Sub(u.Started))
	}
	return s
}

// ago is d as a time gone by, roughly.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just started"
	case d < time.Hour:
		return fmt.Sprintf("started %d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("started %d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("started %d days ago", int(d.Hours()/24))
}
