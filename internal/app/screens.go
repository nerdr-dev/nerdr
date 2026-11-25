package app

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's screens are a dialog over the window, opened with F1
// to F5, used, and closed again with esc or q, the agent keeping its size
// underneath. The code changes (F1) and the plugins can be panes of the
// tab too, beside its agents and terminals, as many of each as you like,
// each with all of its own state: see views.go. The plugins have no
// F-key, and are always a pane: the prefix and t p. F1 is a dialog for a
// look at the diff now and then, unless the tab has the changes in a
// pane, where it goes; the prefix and t c puts them in one.

// paneScreen reports whether screen i (0 for F1) can be a pane.
func paneScreen(i int) bool { return i == core.ViewChanges || i == core.ViewPlugins }

// panelShown reports whether the panel is on screen: as the dialog, or,
// while one is being handled, a view pane.
func (m *Model) panelShown() bool { return m.panelDlg || m.viewIn != nil || m.global.shown }

// showPanel shows the panel as a dialog over the window; a view pane
// being handled is already shown.
func (m *Model) showPanel() {
	if m.viewIn == nil && !m.global.shown {
		m.panelDlg = true
	}
}

// closePanelDialog closes the dialog.
func (m *Model) closePanelDialog() tea.Cmd {
	m.panelDlg = false
	return nil
}

// screenPressed is F1 to F5 pressed. F1 goes to the tab's pane of the
// code changes when it has one, as screenPane says. The others, and F1
// without a pane, come up as the dialog with the keyboard; pressed again
// where it already has it, it goes away (F2 excepted, which goes on to its
// next view).
func (m *Model) screenPressed(msg tea.KeyMsg) tea.Cmd {
	i, ok := review.ScreenOfFKey(msg.String())
	if !ok {
		return nil
	}
	// Another screen is the tab's: it leaves the global tab.
	m.leaveGlobal()
	if t := m.Cur(); t != nil && i == core.ViewChanges && m.viewOfScreen(t, i) != nil {
		return m.screenPane(i)
	}
	on := m.focus == core.FocusReview && m.review.ScreenIndex() == i
	if on && m.panelDlg && msg.String() != "f2" {
		cmd := m.closePanelDialog()
		m.setFocus(core.FocusAgent)
		return tea.Batch(cmd, m.resize())
	}
	cmd := m.reviewKey(msg)
	m.setFocus(core.FocusReview)
	return tea.Batch(cmd, m.resize())
}

// screenPane goes to the tab's pane of screen i (ViewChanges or
// ViewPlugins), opening one when it has none, and pressed there, back to
// the agent. In the plugins' pane, with a plugin's own screen running, it
// goes between the list and that screen instead.
func (m *Model) screenPane(i int) tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	p := m.viewOfScreen(t, i)
	// In the pane before a dialog over it took the keyboard or not.
	in := p != nil && m.viewFocused(t, p)
	m.closePanelDialog()
	if m.focus == core.FocusReview {
		m.setFocus(core.FocusAgent)
	}
	switch {
	case p == nil:
		return tea.Batch(m.newView(i), m.resize())
	case in:
		if i == core.ViewPlugins {
			var ok bool
			cmd := m.inView(t, p, func() tea.Cmd { ok = m.togglePluginUI(); return nil })
			if ok {
				return cmd
			}
		}
		return tea.Batch(m.focusPane(t, t.OwnPane()), m.resize())
	}
	return tea.Batch(m.focusPane(t, p), m.resize())
}

// togglePane is ctrl+q t c: the F1 dialog goes in a pane of the tab's, to
// be kept there; without it, the code changes open in a new pane.
func (m *Model) togglePane() tea.Cmd {
	i := core.ViewChanges
	if m.panelDlg && paneScreen(m.review.ScreenIndex()) {
		i = m.review.ScreenIndex()
		m.closePanelDialog()
		m.setFocus(core.FocusAgent)
	}
	return tea.Batch(m.newView(i), m.resize())
}

// closePanel is ctrl+q t x, or ×, on the dialog: it closes.
func (m *Model) closePanel() tea.Cmd {
	cmd := m.closePanelDialog()
	m.setFocus(core.FocusAgent)
	return tea.Batch(cmd, m.resize())
}

// dlgRect is where the panel's dialog is on the screen: its top-left
// corner and its size, over the agent and its panes.
func (m *Model) dlgRect() (x, y, w, h int) {
	mw, bh := m.mainW(), max(m.height-m.barRows(), 3)
	w = max(min(mw-4, mw*9/10), min(mw, 40))
	h = max(bh-2, min(bh, 10))
	return m.sideW() + (mw-w)/2, m.barRows() + (bh-h)/2, w, h
}

// panelDialogOver draws the panel's dialog over body, the main area's
// rows of mainW columns.
func (m *Model) panelDialogOver(body []string) []string {
	x, y, w, h := m.dlgRect()
	lines := m.panelLines(w, h)
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	if m.mouseOn() {
		end := m.tip(m.onClick(st.Render("×"), (*Model).closePanel), "Close · "+m.pk("t x"))
		if m.global.shown {
			end = m.tip(m.onClick(st.Render("×"), func(m *Model) tea.Cmd { m.leaveGlobal(); return m.resize() }),
				"Close · esc or "+m.pk("v"))
		}
		lines = endOver(lines, w, end, m.focus == core.FocusReview)
	}
	return overlayAt(body, strings.Join(lines, "\n"), m.mainW(), x-m.sideW(), y-m.barRows())
}

// topFree is the run of ─ at the right of the top edge of lines, w wide,
// after the title of the last frame in it: what buttons can go in.
func topFree(lines []string, w int) int {
	if len(lines) == 0 || ansi.StringWidth(lines[0]) != w {
		return 0
	}
	top := ansi.Strip(lines[0])
	if !strings.HasSuffix(top, "╮") {
		return 0
	}
	r := []rune(strings.TrimSuffix(top, "╮"))
	free := 0
	for i := len(r) - 1; i >= 0 && r[i] == '─'; i-- {
		free++
	}
	return free
}

// endOver puts end, buttons, at the right of the top edge of lines, w
// wide, as framedEnd does: where they fit, a ─ either side.
func endOver(lines []string, w int, end string, active bool) []string {
	ew := lipgloss.Width(end)
	if end == "" || ew+4 > topFree(lines, w) {
		return lines
	}
	bs := lipgloss.NewStyle().Foreground(frameColor)
	if active {
		bs = lipgloss.NewStyle().Foreground(accent)
	}
	lines[0] = ansi.Truncate(lines[0], w-ew-4, "") + "\x1b[0m " + end + " " + bs.Render("─╮")
	return lines
}

// reviewXY is where screen cell (sx, sy) is in the dialog's drawing; at
// its left edge when left of it, as a drag that runs off it is.
func (m *Model) reviewXY(sx, sy int) (int, int) {
	x, y, _, _ := m.dlgRect()
	return max(sx-x, 0), sy - y
}

// dialogPanelMouse is the mouse over the window while the panel's dialog
// is open: in the dialog it is the panel's; a click outside closes it.
func (m *Model) dialogPanelMouse(ev tea.MouseMsg) tea.Cmd {
	x, y, w, h := m.dlgRect()
	if ev.X < x || ev.X >= x+w || ev.Y < y || ev.Y >= y+h {
		if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
			return m.closePanel()
		}
		return nil
	}
	ev.X, ev.Y = ev.X-x, ev.Y-y
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		m.drag = dragReview
	}
	rm, cmd := m.review.Update(ev)
	m.review = rm.(review.Model)
	return tea.Batch(cmd, m.syncPRPanel())
}
