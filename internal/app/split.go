package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// The split: two tabs side by side, the active one, which has the
// keyboard, and m.split beside it. The review panel shares the right-hand
// side with it: while the panel is open the active tab is shown with it as
// ever, and hiding the panel brings the split back. Each tab keeps its
// side. Going to a tab moves it in on the side with the keyboard, except
// the other tab on screen, where the keyboard moves across instead.

// splitTab is the tab beside the active one, or nil when there is none.
// Only tabs of one repository share the screen: going to a tab of another
// ends the split.
func (m *Model) splitTab() *tab {
	if m.split == nil || m.split == m.cur() || m.split.group() != m.curGroup() {
		return nil
	}
	if m.tabIndex(m.split) < 0 {
		return nil
	}
	return m.split
}

// splitShown reports whether two agents are on screen now.
func (m *Model) splitShown() bool {
	return m.splitTab() != nil && !m.reviewOpen && !m.reviewZoom
}

// sides are the tabs on the left and the right while the split is shown;
// otherwise the active tab and nil.
func (m *Model) sides() (left, right *tab) {
	if !m.splitShown() {
		return m.cur(), nil
	}
	if m.splitLeft {
		return m.split, m.cur()
	}
	return m.cur(), m.split
}

// splitWidths are the columns of the left and the right tab, either side
// of the one-column rule.
func (m *Model) splitWidths() (int, int) {
	rw := max(m.mainW()*m.splitWidth/100, 10)
	return max(m.mainW()-rw-1, 10), rw
}

// tabSize is the area agent t draws in: its pane, less the panel above
// it.
func (m *Model) tabSize(t *tab) (int, int) {
	return m.paneSize(t)
}

// agentX is the column the active tab's frame starts at.
func (m *Model) agentX() int {
	if m.splitShown() && m.splitLeft {
		lw, _ := m.splitWidths()
		return m.sideW() + lw + 1
	}
	return m.sideW()
}

// toggleSplit puts the active tab on the right with a neighbour on the
// left, which gets the keyboard, or goes back to one tab. A split hidden
// behind the review panel is brought back rather than ended.
func (m *Model) toggleSplit() tea.Cmd {
	if s := m.splitTab(); s != nil {
		if !m.splitShown() {
			m.reviewOpen, m.reviewZoom = false, false
			m.setFocus(focusAgent)
			return m.resize()
		}
		m.split = nil
		m.saveSession()
		return m.resize()
	}
	t := m.cur()
	other := m.neighbour(t)
	if other == nil {
		m.toast(m.commandOff("z v"))
		return nil
	}
	return m.splitWith(other, t)
}

// splitWith shows left and right side by side, the keyboard on the left.
func (m *Model) splitWith(left, right *tab) tea.Cmd {
	m.split, m.splitLeft = right, false
	m.reviewOpen, m.reviewZoom = false, false
	m.setFocus(focusAgent)
	cmd := m.selectTab(m.tabIndex(left))
	m.saveSession()
	return tea.Batch(cmd, m.wakeTab(right), m.resize())
}

// swapSides moves the two tabs of the split to each other's side.
func (m *Model) swapSides() tea.Cmd {
	if !m.splitShown() {
		return nil
	}
	m.splitLeft = !m.splitLeft
	m.saveSession()
	return m.resize()
}

// neighbour is the tab to put beside t: the one before it in its group,
// or after; nil when t is alone in its repository.
func (m *Model) neighbour(t *tab) *tab {
	if t == nil {
		return nil
	}
	idx := m.groupTabs(t.group())
	for pos, i := range idx {
		if m.tabs[i] != t {
			continue
		}
		if pos > 0 {
			return m.tabs[idx[pos-1]]
		}
		if pos+1 < len(idx) {
			return m.tabs[idx[pos+1]]
		}
	}
	return nil
}
