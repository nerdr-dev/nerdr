package app

import (
	"strconv"
	"strings"

	"nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The review panel's menu is a column at the right of the body: its
// screens, each by its F-key and a short name (the F-key alone with
// panel_labels off). While the panel is kept open the column gives way to
// the panel's own menu row over it (see panelMenu). An F-key, pressed or clicked there, opens the panel
// on its screen, left of the column, as wide as panel_width; a click on
// the screen open closes it again. Small (the prefix and z k, or layout =
// "small"), the panel folds again when the keyboard goes back to the
// agent. Kept open, it is made small by itself while the active tab's
// panes would not all fit beside it, and opens again once they do.

// panelRailW is the columns of the menu's column, its frame included.
func (m *Model) panelRailW() int {
	key := 2 // F1
	if keys, _ := m.screenMenuKeys(); len(keys) > 0 {
		key = len(keys[0])
	}
	if !m.panelLabels() {
		return key + 4 // a space either side, inside the frame
	}
	w := 0
	for _, s := range review.ScreenShort() {
		w = max(w, len(s))
	}
	return key + w + 5 // " F1 name ", inside the frame
}

// panelLabels reports whether the menu's column names its screens beside
// their F-keys.
func (m *Model) panelLabels() bool { return m.cfg.PanelLabels == nil || *m.cfg.PanelLabels }

// railShown reports whether the menu's column is on screen: whenever the
// window has room for it beside the agent, and the panel has no menu row
// of its own.
func (m *Model) railShown() bool {
	return !m.panelMenu() && m.bodyW() >= 3*m.panelRailW()
}

// panelMenu reports whether the panel has its menu as a row over its
// screen, in place of the column: while it is kept open, not small, so
// it is on screen all the time.
func (m *Model) panelMenu() bool {
	return m.reviewOpen && !m.panelSmall && (m.reviewZoom || !m.splitShown())
}

// panelTop is the rows the panel's menu row and the frame over it take
// above the review's own drawing.
func (m *Model) panelTop() int {
	if m.panelMenu() {
		return 1
	}
	return 0
}

// railCols is the columns the menu's column takes from the body, the one
// between it and the rest included.
func (m *Model) railCols() int {
	if !m.railShown() {
		return 0
	}
	return m.panelRailW() + 1
}

// railLines draws the menu's column, h rows of railW columns.
func (m *Model) railLines(h int) []string {
	rw := m.panelRailW()
	bs := lipgloss.NewStyle().Foreground(frameColor)
	if m.focus == focusReview {
		bs = lipgloss.NewStyle().Foreground(accent)
	}
	inner := rw - 2
	row := func(s string) string {
		return bs.Render("│") + padTo(s, inner) + bs.Render("│")
	}
	top := bs.Render("╭" + strings.Repeat("─", inner) + "╮")
	if inner >= 2 {
		top = bs.Render("╭─") + m.panelToggle(m.panelSmall) + bs.Render(strings.Repeat("─", inner-2)+"╮")
	}
	out := []string{top}
	short := review.ScreenShort()
	names := review.ScreenNames()
	shown := -1
	if m.reviewOpen || m.reviewZoom {
		shown = m.review.ScreenIndex()
	}
	keys, _ := m.screenMenuKeys()
	for i := range names {
		label := " F" + strconv.Itoa(i+1)
		if i < len(keys) {
			label = " " + keys[i]
		}
		if m.panelLabels() {
			label += " " + short[i]
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
		if i == shown {
			label = padTo(label+" ", inner)
			style = lipgloss.NewStyle().Background(tabActiveBg).Foreground(lipgloss.Color("231")).Bold(true)
		}
		item := style.Render(label)
		if i == 1 && m.review.News() {
			item += warn("◆")
		}
		n := i + 1
		tip := names[i] + " · " + m.screenKey(n)
		if i == shown {
			tip += " · click: close the panel"
		}
		out = append(out, row(""), row(m.tip(m.onClick(item, func(m *Model) tea.Cmd { return m.railClick(n) }), tip)))
	}
	for len(out) < h-1 {
		out = append(out, row(""))
	}
	out = append(out[:min(len(out), h-1)], bs.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

// railClick is a click on screen n in the menu's column: it opens the
// panel on it, or closes the panel when it is open on it already.
func (m *Model) railClick(n int) tea.Cmd {
	if (m.reviewOpen || m.reviewZoom) && m.review.ScreenIndex() == n-1 {
		m.reviewOpen, m.reviewZoom = false, false
		m.setFocus(focusAgent)
		return m.resize()
	}
	return m.handleKey(keyMsg("f" + strconv.Itoa(n)))
}

// panelToggle is the » or « that makes the panel small or keeps it open:
// « while it is small, » while it stays open.
func (m *Model) panelToggle(small bool) string {
	glyph, tip := "»", "Small: the panel folds when the keyboard goes back to the agent · "+m.pk("z k")
	switch {
	case small && m.panelAuto:
		glyph, tip = "«", "Small to make room for the panes; open again when they fit beside it · click to keep it open all the same · "+m.pk("z k")
	case small:
		glyph, tip = "«", "Keep the panel open when the keyboard goes back to the agent · "+m.pk("z k")
	}
	return m.tip(m.onClick(lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(glyph),
		(*Model).togglePanelSmall), tip)
}

// togglePanelSmall makes the panel small, or keeps it open: from the
// small panel, or the panel opened from it, it opens for good.
func (m *Model) togglePanelSmall() tea.Cmd {
	if m.panelSmall {
		m.panelKept, m.panelAuto = m.panelAuto, false
		m.panelSmall = false
		m.reviewOpen, m.reviewZoom = true, false
		return m.resize()
	}
	m.panelSmall, m.panelAuto, m.panelKept = true, false, false
	m.reviewOpen, m.reviewZoom = false, false
	m.setFocus(focusAgent)
	return m.resize()
}

// fitPanel makes room for the active tab's panes: the panel kept open is
// made small while the panes not minimised by hand would not all fit
// beside it at pane_min_width, and opens again once they would. One
// opened by hand all the same stays, until they fit.
func (m *Model) fitPanel() {
	t := m.cur()
	if t == nil || m.splitShown() || m.reviewZoom {
		return
	}
	n := 0
	for _, p := range t.panes() {
		if !p.small {
			n++
		}
	}
	rw, _ := m.reviewArea()
	w := max(m.mainW()-rw-1, 12)
	fits := n <= max((w+1)/(m.paneMinW()+1), 1)
	switch {
	case fits:
		m.panelKept = false
		if m.panelAuto {
			m.panelAuto, m.panelSmall, m.reviewOpen = false, false, true
		}
	case m.reviewOpen && !m.panelSmall && !m.panelKept:
		m.panelAuto, m.panelSmall = true, true
		m.reviewOpen = m.focus == focusReview
	}
}
