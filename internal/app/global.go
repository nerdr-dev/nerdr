package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
)

// The global plugins' dialog, behind the prefix and v only: the global
// plugins (see plugin.Global), which are no tab's, and so are not in any
// tab's plugins pane. It is called the global tab here, as it was one.
// It is the plugins screen of a panel of its own, a dialog over the
// window's main area as the panel's is, with the keyboard while it is
// shown. The tab that was active stays so underneath: going to a tab, esc
// or a click outside it closes it. It opens by itself on a global plugin an
// agent runs, with open_global_plugins (see showPluginRun).
//
// The global plugins are a machine's: the tab shows those of one machine,
// home at first, and ctrl+o goes on to the next.

// globalTab is the global tab's state.
type globalTab struct {
	shown bool
	// panel is the global tab's panel while it is not shown; while it is,
	// the window's own, kept aside.
	panel panelState
	ready bool // panel was made
	// on is the machine whose global plugins are shown, nil for home.
	on *fleet.Machine
}

// globalOn is the machine whose global plugins the global tab shows.
func (m *Model) globalOn() *fleet.Machine {
	if mc := m.global.on; mc != nil && m.fleet.Named(mc.Name) == mc {
		return mc
	}
	return m.fleet.Home()
}

// pluginsMachine is the machine whose plugins the panel lists for t: t's,
// or with no tab in the global tab, the one it shows.
func (m *Model) pluginsMachine(t *core.Tab) *fleet.Machine {
	if t == nil && m.global.shown && m.viewIn == nil {
		return m.globalOn()
	}
	return m.machineOf(t)
}

// showGlobal goes to the global tab, with the keyboard.
func (m *Model) showGlobal() tea.Cmd {
	if m.global.shown {
		return nil
	}
	m.dialog = nil
	if m.panelDlg {
		m.closePanelDialog()
	}
	if m.scrolling {
		m.scrollKey(tea.KeyMsg{Type: tea.KeyEsc})
	}
	m.plugins.uiFocus = ""
	if !m.global.ready {
		m.global.ready = true
		m.global.panel.review = m.review
		m.global.panel.plugins = pluginsView{global: true}
	}
	m.swapPanel(&m.global.panel)
	m.global.shown = true
	var cmd tea.Cmd
	if !m.review.Plugins() {
		cmd = m.review.SetScreen(review.ScreenAt(core.ViewPlugins))
		m.review = m.review.SetFixed(true) // the global tab is of this screen
	}
	m.focus = core.FocusReview
	m.review = m.review.SetFocused(true)
	return tea.Batch(cmd, m.resize())
}

// leaveGlobal leaves the global tab for the tab that was active, the
// agent having the keyboard.
func (m *Model) leaveGlobal() {
	if !m.global.shown {
		return
	}
	m.plugins.uiFocus = ""
	m.swapPanel(&m.global.panel)
	m.global.shown = false
	m.focus = core.FocusAgent
	m.review = m.review.SetFocused(false)
	m.resizePanes()
}

// toggleGlobal is the prefix and v: to the global tab, and back.
func (m *Model) toggleGlobal() tea.Cmd {
	if m.global.shown {
		m.leaveGlobal()
		return m.resize()
	}
	return m.showGlobal()
}

// globalNextMachine shows the global plugins of the next machine that is
// not grey: ctrl+o in the global tab. It reports whether there was one.
func (m *Model) globalNextMachine() bool {
	ms := m.fleet.Machines()
	if len(ms) < 2 {
		return false
	}
	at, i := m.globalOn(), 0
	for j, mc := range ms {
		if mc == at {
			i = j
		}
	}
	for k := 1; k < len(ms); k++ {
		if mc := ms[(i+k)%len(ms)]; !grey(mc) {
			m.global.on = mc
			m.plugins.uiFocus = ""
			return true
		}
	}
	return false
}

// globalMouse is a click or the wheel over the window's main area while
// the global plugins' dialog is open: in it, its panel's, as the panel
// dialog's are; a click outside closes it.
func (m *Model) globalMouse(ev tea.MouseMsg) tea.Cmd {
	x, y, w, h := m.dlgRect()
	if ev.X < x || ev.X >= x+w || ev.Y < y || ev.Y >= y+h {
		if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
			m.leaveGlobal()
			return m.resize()
		}
		return nil
	}
	ev.X, ev.Y = ev.X-x, ev.Y-y
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		m.drag = dragReview
	}
	rm, cmd := m.review.Update(ev)
	m.review = rm.(review.Model)
	return cmd
}
