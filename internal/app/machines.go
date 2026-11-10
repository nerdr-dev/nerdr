package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
)

// The TUI shows the tabs of several machines' cores. One whose core is out
// of reach is grey: its tabs stay, dimmed, and one of them shows why in
// place of its agent, and the key that connects to it again (r). The
// others work on, and the TUI ends only when no other machine is left.

// grey reports whether the core of mc is out of reach: gone for good until
// the user says (Gone), or not back in coreGoneWait, and tried again (Err).
// One just gone, which its holder starts again at once, is not: what was
// shown stays until it is back.
func grey(mc *fleet.Machine) bool { return mc.Gone != "" || (mc.Away && mc.Err != "") }

// machineLabel names mc to the user.
func machineLabel(mc *fleet.Machine) string {
	switch {
	case mc.Name != "":
		return mc.Name
	case mc.Host != "":
		return mc.Host
	}
	return "this machine"
}

// othersLeft reports whether a machine other than mc is still left to the
// TUI: one not gone for good.
func (m *Model) othersLeft(mc *fleet.Machine) bool {
	for _, o := range m.fleet.Live() {
		if o != mc {
			return true
		}
	}
	return false
}

// goneFor has the TUI go on without the core of mc, gone for good for
// why: grey, while another machine is left; else the TUI ends, with err
// said as it does.
func (m *Model) goneFor(mc *fleet.Machine, why, err string) tea.Cmd {
	mc.Away = false
	if !m.othersLeft(mc) {
		m.goneErr = err
		return tea.Quit
	}
	mc.Gone = why
	// What the review panel asked of it will not be answered.
	m.reviewAsk.FailAll("the connection to nerdr's core has closed")
	m.fleet.Bump()
	m.syncInput()
	m.toast(machineLabel(mc) + ": " + why)
	return nil
}

// goneToast says the core of mc is out of reach, for what was asked of it.
func (m *Model) goneToast(mc *fleet.Machine) string {
	if mc.Gone != "" {
		return machineLabel(mc) + " is not connected: " + mc.Gone
	}
	if mc.Name == "" && len(m.fleet.Machines()) == 1 {
		return "nerdr's core is gone: connecting again…"
	}
	return machineLabel(mc) + ": nerdr's core is gone: connecting again…"
}

// takenOver reports whether another nerdr took the core of mc over.
func takenOver(mc *fleet.Machine) bool { return mc.Gone == attach.ByeTakenOver }

// greyKey handles key k on a tab of grey machine mc: r connects to it
// again, or, taken over by another nerdr, asks to take it back.
func (m *Model) greyKey(mc *fleet.Machine, k string) tea.Cmd {
	if k != "r" && k != "R" {
		return nil
	}
	switch {
	case mc.Gone == "" && mc.Err != "":
		// Tried again and again without a password: the user logs in.
		if s, ok := m.server(mc); ok && m.dial != nil {
			return m.login(s)
		}
		m.toast(m.goneToast(mc))
		return nil
	case mc.Gone == "":
		m.toast(m.goneToast(mc))
		return nil
	case mc.Redial == nil:
		m.toast("nerdr cannot connect to " + machineLabel(mc) + " again: start nerdr again")
		return nil
	case takenOver(mc):
		m.dialog = takeBack{mc}
		m.syncInput()
		return nil
	}
	return m.connectAgain(mc)
}

// connectAgain has the TUI connect to the core of mc again, once it was
// gone for good: as when the connection has just closed.
func (m *Model) connectAgain(mc *fleet.Machine) tea.Cmd {
	if s, ok := m.server(mc); ok && m.dial != nil {
		// Asked for by the user: ssh may ask for a password.
		return m.login(s)
	}
	mc.Gone, mc.Away = "", true
	m.fleet.Bump()
	m.toast(m.goneToast(mc))
	return m.redialCore(mc)
}

// takeBack asks whether to take the core of a machine back from the other
// nerdr that took it over: the other then shows it grey.
type takeBack struct{ mc *fleet.Machine }

func (d takeBack) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y", "enter":
		m.dialog = nil
		return m.connectAgain(d.mc)
	case "esc", "n", "N", "ctrl+c":
		m.dialog = nil
	}
	return nil
}

func (d takeBack) view(m *Model, w, _ int) string {
	return box("Take "+machineLabel(d.mc)+" back?", []string{
		"Another nerdr took over " + machineLabel(d.mc) + "'s core.",
		"Taking it back shows it here, and grey there.",
		"",
		m.button("y", "take it back") + "   " + m.button("esc", "cancel"),
	}, w)
}

// greyLines is what a tab of grey machine mc shows in place of its agents,
// w×h: why the machine is out of reach, and what r does.
func (m *Model) greyLines(mc *fleet.Machine, w, h int) []string {
	name := machineLabel(mc)
	lines := []string{"", "  " + lipgloss.NewStyle().Bold(true).Render(name+" is not connected")}
	switch {
	case mc.Gone != "":
		lines = append(lines, "  "+dim(mc.Gone), "")
		switch {
		case mc.Redial == nil:
		case takenOver(mc):
			lines = append(lines, "  "+m.keys("r", "take it back"))
		default:
			lines = append(lines, "  "+m.keys("r", "connect again"))
		}
	default:
		why := "connecting again…"
		if mc.Err != "" {
			why = mc.Err + " — trying again"
		}
		lines = append(lines, "  "+dim(why))
		if _, ok := m.server(mc); ok && mc.Err != "" {
			lines = append(lines, "", "  "+m.keys("r", "log in"))
		}
	}
	return framed(fitLines(lines, w-2, h-2), w, h, name, m.focus == core.FocusAgent)
}

// machineBadge names the machine of group g before it in the bars, when
// it is not this one: grey, with a cross, while its core is out of reach.
func (m *Model) machineBadge(g string) string {
	mc, _ := m.fleet.GroupOf(g)
	return m.badgeOf(mc)
}

// emptyMachines are the badges of the named machines with no tabs, for
// the bar: else nothing would show they are there, or grey.
func (m *Model) emptyMachines() string {
	var out string
	for _, mc := range m.fleet.Machines() {
		if mc.Name == "" || len(mc.Core.Tabs) > 0 {
			continue
		}
		out += m.onClick(m.badgeOf(mc), (*Model).openMachines)
	}
	return out
}

// badgeOf is machine mc's badge, "" for this one.
func (m *Model) badgeOf(mc *fleet.Machine) string {
	if mc.Name == "" {
		return ""
	}
	st := lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("231")).Bold(true)
	label, tip := " "+mc.Name+" ", "The agents of these tabs run on "+mc.Name
	if grey(mc) {
		st = lipgloss.NewStyle().Background(lipgloss.Color("238")).Foreground(lipgloss.Color("246"))
		label, tip = " "+mc.Name+" ✕ ", mc.Name+" is not connected · r on one of its tabs: connect again"
	}
	return m.tip(st.Render(label), tip)
}

// groupShown is group g as the bars show it, after its machine's badge:
// the group as its machine's core names it.
func (m *Model) groupShown(g string) string {
	_, name := m.fleet.GroupOf(g)
	return name
}

// groupWidth is the columns group g's name takes in the bars, its
// machine's badge with it: as machineBadge draws it, without a tip made.
func (m *Model) groupWidth(g string) int {
	mc, name := m.fleet.GroupOf(g)
	w := lipgloss.Width(name)
	switch {
	case mc.Name == "":
	case grey(mc):
		w += lipgloss.Width(" " + mc.Name + " ✕ ")
	default:
		w += lipgloss.Width(" " + mc.Name + " ")
	}
	return w
}
