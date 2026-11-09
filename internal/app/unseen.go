package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// openUnseen lists the programs in the holder that no tab took up, each
// to be stopped, or all of them; none is stopped without the user.
func (m *Model) openUnseen() tea.Cmd {
	if len(m.Core.Unseen) == 0 {
		m.toast("no programs run outside the tabs")
		return nil
	}
	items := []menuItem{{label: "Programs from before nerdr started again, in no tab"}}
	for _, u := range m.Core.Unseen {
		items = append(items, menuItem{label: unseenLine(u, time.Now()), run: func(m *Model) tea.Cmd {
			return m.askStopUnseen(u.ID, "Stop "+u.What+"?", []string{unseenLine(u, time.Now())})
		}})
	}
	if len(m.Core.Unseen) > 1 {
		items = append(items, menuItem{label: fmt.Sprintf("Stop all %d", len(m.Core.Unseen)), key: "a", run: func(m *Model) tea.Cmd {
			return m.askStopUnseen("", fmt.Sprintf("Stop all %d programs in no tab?", len(m.Core.Unseen)), nil)
		}})
	}
	m.dialog = &menu{title: "Programs in no tab", items: items, x: m.width / 4, y: m.height / 4, hi: 1}
	return nil
}

// askStopUnseen asks before stopping program id, all of them for "".
func (m *Model) askStopUnseen(id, title string, lines []string) tea.Cmd {
	m.dialog = &confirm{
		title:     title,
		lines:     append(lines, "It is hung up, and killed if it is still there after a few seconds."),
		defaultNo: true,
		yesLabel:  "Stop", noLabel: "Cancel",
		yes: func(m *Model) tea.Cmd { return m.do(core.StopUnseen{ID: id}) },
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
