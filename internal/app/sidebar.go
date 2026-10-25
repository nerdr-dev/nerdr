package app

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The sidebar: with tab_bar = "left" (or the prefix and z t), the
// repositories and the tabs are two lists left of the body, rather than
// two rows over it — the repositories on top, the active one's tabs
// below. One row over it all keeps the active tab's name and nerdr's
// messages. After the prefix, ↑ ↓ walk the list the arrows are on, and
// ← → go between the two.
//
// Made small (the « at its top right, the prefix and z m, or tab_bar =
// "left-small" for every start), the sidebar is
// the lights alone, so what wants a look is still seen: a click on one
// goes there, and resting the pointer on it names it. While the prefix is
// on, the whole sidebar is drawn over the agent, so the arrows walk names
// and not just lights; the agent keeps its size, and does not rewrap.

const (
	sideMin = 22 // the sidebar's fewest columns, its frames included: room for "repositories" and its «
	sideMax = 36 // and its most
)

// railW is the columns of the small sidebar, its frames included: a
// light, a space either side.
const railW = 5

// sideW is the columns the sidebar takes, the one after it included: 0
// without one, or in a window too narrow to give it room.
func (m *Model) sideW() int {
	if !m.sideBar || m.width < 3*sideMin {
		return 0
	}
	if m.sideSmall {
		return railW + 1
	}
	return m.wideW()
}

// wideW is the columns of the sidebar with its names, the one after it
// included. It is as wide as its edge was dragged to or, left alone, as
// the longest entry needs, so the body does not move about with the group
// shown; names longer than that end in "…" and are whole in their tips.
func (m *Model) wideW() int {
	if m.sideWidth > 0 {
		return min(max(m.sideWidth, sideMin), m.width/2) + 1
	}
	w := 0
	for _, g := range m.groups() {
		// light, name, (n)
		w = max(w, lipgloss.Width(g)+len(strconv.Itoa(len(m.groupTabs(g))))+6)
		for n, i := range m.groupTabs(g) {
			// light, n:name, a mark or two, and its details
			w = max(w, lipgloss.Width(m.tabs[i].Name)+len(strconv.Itoa(n+1))+8+lipgloss.Width(m.sideDetails(m.tabs[i], i == m.active)))
		}
	}
	return min(max(w+2, sideMin), max(sideMax, m.width/5), m.width/3) + 1
}

// bodyW is the columns right of the sidebar: the agent, the review panel,
// the split.
func (m *Model) bodyW() int { return m.width - m.sideW() }

// sideRepoH is the rows of the repository list, its frame included, in a
// sidebar h rows tall, small or wide: 0 when there is no room.
func (m *Model) sideRepoH(h int, small bool) int {
	if h < 10 {
		return 0
	}
	rows := len(m.groups())
	if m.mouseOn() {
		rows++ // + new repo
	}
	if small {
		return max(min(rows+3, h/2), 4) // and the » over them
	}
	return max(min(rows+2, h/3), 3)
}

// sideLines draws the sidebar as it is, h rows of sideW columns.
func (m *Model) sideLines(h int) []string {
	return m.sidebarLines(h, m.sideSmall)
}

// sidebarLines draws the sidebar small (its lights) or wide (with the
// names), h rows of its columns and the one after them.
func (m *Model) sidebarLines(h int, small bool) []string {
	w := m.wideW() - 1
	if small {
		w = railW
	}
	rh := m.sideRepoH(h, small)
	var out []string
	if rh > 0 {
		out = m.repoList(w, rh, small)
	}
	out = append(out, m.tabList(w, h-rh, small)...)
	for i := range out {
		out[i] += " "
	}
	return out
}

// peekSide draws the wide sidebar over the left of body while the small
// one is shown and the prefix is on, for the arrows to walk names.
func (m *Model) peekSide(body []string) []string {
	if !m.sideSmall || m.sideW() == 0 || !m.prefix || m.keyGroup != "" || m.dialog != nil {
		return body
	}
	return overlayAt(body, strings.Join(m.sidebarLines(len(body), false), "\n"), m.width, 0, 0)
}

// toggleSideSmall makes the sidebar small or wide again; with the tabs
// on top, it puts them on the left, small.
func (m *Model) toggleSideSmall() tea.Cmd {
	if !m.sideBar {
		m.sideBar, m.sideSmall = true, true
	} else {
		m.sideSmall = !m.sideSmall
	}
	m.saveSession()
	return m.resize()
}

// sideToggle is the « or » that makes the sidebar small or wide.
func (m *Model) sideToggle(small bool) string {
	glyph, tip := "«", "Smaller: the lights alone · "+m.pk("z m")
	if small {
		glyph, tip = "»", "Wider: with the names · "+m.pk("z m")
	}
	return m.tip(m.onClick(lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(glyph),
		(*Model).toggleSideSmall), tip)
}

// railEntry is one entry of the small sidebar: a light, with the active
// one's background.
func (m *Model) railEntry(active, walking bool, s status, of string) string {
	bg := lipgloss.Color("")
	if active {
		bg = tabActiveBg
		if walking {
			bg = navActiveBg
		}
	}
	sp := lipgloss.NewStyle().Background(bg).Render(" ")
	return sp + m.lightTip(s, bg, of) + sp
}

// repoList is the list of repositories, in a w×h frame; small, their
// lights under the » that makes it wide; wide, the « at the right of its
// top, by the agent.
func (m *Model) repoList(w, h int, small bool) []string {
	cur := m.curGroup()
	walking := m.prefix && m.onGroups
	var lines []string
	if small {
		lines = append(lines, "  "+m.sideToggle(true))
	}
	at := 0
	for _, g := range m.groups() {
		n := len(m.groupTabs(g))
		if g == cur {
			at = len(lines)
		}
		tip := plural(n, "tab")
		var s string
		if small {
			s = m.railEntry(g == cur, walking, m.groupStatus(g), "the tab here that most wants a look · "+g)
		} else {
			// (7 2● 3●): the tabs, and how many of them ask and work, in their lights
			moreSt := lipgloss.NewStyle().Background(entryBg(g == cur, walking)).Foreground(lipgloss.Color("244"))
			count := moreSt.Render(" (" + strconv.Itoa(n))
			for _, st := range []status{stBlocked, stWorking} {
				if k := m.groupCount(g, st); k > 0 {
					count += moreSt.Render(" "+strconv.Itoa(k)) + lightOn(st, entryBg(g == cur, walking))
					tip += ", " + strconv.Itoa(k) + " " + st.label()
				}
			}
			s = m.sideEntry(w-2, g == cur, walking, m.groupStatus(g), "the tab here that most wants a look",
				g, "", count+moreSt.Render(")"))
		}
		s = m.tip(s, g+": "+tip+" · click: go there · right-click: more")
		lines = append(lines, m.zone(s, func(m *Model, ev tea.MouseMsg) tea.Cmd {
			switch {
			case ev.Action != tea.MouseActionPress:
				return nil
			case ev.Button == tea.MouseButtonLeft:
				return m.selectGroup(g)
			case ev.Button == tea.MouseButtonRight:
				return m.openGroupMenu(g, ev.X, ev.Y+1)
			}
			return nil
		}))
	}
	if m.mouseOn() {
		plus := " + new repo"
		if small {
			plus = " + "
		}
		lines = append(lines, m.tip(m.onClick(plusStyle.UnsetBackground().Render(plus), (*Model).openNewRepoDialog),
			"Work in a repository: pick one, make one, or clone one"))
	}
	if small {
		return framed(fitLines(sideWindow(lines, at, h-2), w-2, h-2), w, h, "", walking)
	}
	return framedEnd(fitLines(sideWindow(lines, at, h-2), w-2, h-2), w, h, "repositories", m.sideToggle(false), walking)
}

// tabList is the active repository's tabs, numbered within it as the tab
// row numbers them, in a w×h frame; with the details on, each but the
// active one has them on a line under it.
func (m *Model) tabList(w, h int, small bool) []string {
	g := m.curGroup()
	walking := m.prefix && !m.onGroups
	now := time.Now()
	var lines []string
	at := 0
	for n, i := range m.groupTabs(g) {
		t := m.tabs[i]
		active := i == m.active
		if active {
			at = len(lines)
		}
		more := ""
		if active {
			more = " " + string(t.Agent)
		}
		if m.cold(t, now) {
			more += " ❄"
		}
		if t == m.splitTab() {
			mark := " ◨"
			if m.splitLeft {
				mark = " ◧"
			}
			more += mark
		}
		var s string
		if small {
			s = m.railEntry(active, walking, t.lit(), strconv.Itoa(n+1)+":"+t.Name)
		} else {
			s = m.sideEntry(w-2, active, walking, t.lit(), "", strconv.Itoa(n+1)+":"+t.Name, more, m.sideDetails(t, active))
		}
		s = m.tip(s, t.Name+" · "+string(t.Agent)+" · "+t.lit().label()+" · right-click: more · middle-click: close")
		lines = append(lines, m.zone(s, func(m *Model, ev tea.MouseMsg) tea.Cmd { return m.tabMouse(t, ev) }))
	}
	if m.mouseOn() {
		plus := " + new worktree"
		if small {
			plus = " + "
		}
		lines = append(lines, m.tip(m.onClick(plusStyle.UnsetBackground().Render(plus), func(m *Model) tea.Cmd {
			if repo := tabRepo(m.cur()); repo != "" {
				return m.openWizardFor(repo)
			}
			return m.openWizard()
		}), "New worktree, in this repository, with a tab of its own"))
	}
	title := "tabs"
	if g != "" {
		title = g
	}
	if small {
		title = ""
	}
	return framed(fitLines(sideWindow(lines, at, h-2), w-2, h-2), w, h, title, walking)
}

// sideDetails is what the tab row says about tab t, for its entry in the
// list: "" for the active tab, whose are in the row on top, and with the
// details off. On the entry's own line, cut short where there is no room,
// rather than on a line of its own that reads as the name wrapped.
func (m *Model) sideDetails(t *tab, active bool) string {
	if !m.showDetails || active {
		return ""
	}
	if d := m.details(t, lipgloss.Color(""), false); d != "" {
		return " " + d
	}
	return ""
}

// sideEntry is one entry of a list, w columns wide: its light, its name
// and, dimmer, more about it, then extra as it is styled. The active one
// has the bars' active background, blue while the arrows walk its list.
func (m *Model) sideEntry(w int, active, walking bool, s status, of, name, more, extra string) string {
	bg := entryBg(active, walking)
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	if active {
		st = lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("231")).Bold(true)
	}
	moreSt := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("244"))
	line := st.Render(" ") + m.lightTip(s, bg, of) + st.Render(" "+name) + moreSt.Render(more) + extra
	line = ansi.Truncate(line, w, "…")
	return line + st.Render(strings.Repeat(" ", max(w-lipgloss.Width(line), 0)))
}

// entryBg is the background of a list's entry: the bars' active one for
// the active entry, blue while the arrows walk its list.
func entryBg(active, walking bool) lipgloss.Color {
	switch {
	case active && walking:
		return navActiveBg
	case active:
		return tabActiveBg
	}
	return lipgloss.Color("")
}

// sideWindow is the h of lines shown, around line at, so the active entry
// is always in view.
func sideWindow(lines []string, at, h int) []string {
	if h <= 0 {
		return nil
	}
	if len(lines) <= h {
		return lines
	}
	start := min(max(at-h/2, 0), len(lines)-h)
	return lines[start : start+h]
}

// sideBarRow is the row over the sidebar and the body: the active tab, as
// the tab row has it, and on the right nerdr's messages.
func (m *Model) sideBarRow() string {
	bar := lipgloss.NewStyle().Background(tabBarBg)
	m.groupRow = barRow{} // neither row is drawn, so nothing hidden in them
	var left string
	if t := m.cur(); t != nil {
		st := lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("231")).Bold(true)
		left = st.Render(" ") + m.lightTip(t.lit(), tabBarBg, "") + st.Render(" "+t.Name+" ") +
			lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("248")).Render(string(t.Agent)+" ")
		if m.showDetails {
			if d := m.details(t, tabBarBg, true); d != "" {
				left += d + bar.Render(" ")
			}
		}
	}
	warning := m.overlapWarning()
	msg, menuBtn, _ := m.barStatus()
	msg = ansi.Truncate(msg, max(m.width-lipgloss.Width(left)-lipgloss.Width(warning)-lipgloss.Width(menuBtn)-1, 0), "…")
	return joinBar(left, warning+msg+menuBtn, m.width, bar)
}

// onSideEdge reports whether column x is the wide sidebar's edge: its
// right frame or the column after it, where a press drags its width.
func (m *Model) onSideEdge(x int) bool {
	w := m.sideW()
	return w > 0 && !m.sideSmall && (x == w-1 || x == w-2)
}

// dragSideTo sets the sidebar's width from the pointer at column x, its
// column after it kept under the pointer.
func (m *Model) dragSideTo(x int) tea.Cmd {
	w := min(max(x, sideMin), m.width/2)
	if w == m.wideW()-1 {
		return nil
	}
	m.sideWidth = w
	return m.resize()
}

// sideWheel handles the wheel over the sidebar, below its zones: over
// the repositories it steps through them, over the tabs through those.
func (m *Model) sideWheel(ev tea.MouseMsg) tea.Cmd {
	if !isWheel(ev) || !m.wheelStep() {
		return nil
	}
	if ev.Y-m.barRows() < m.sideRepoH(m.height-m.barRows(), m.sideSmall) {
		return m.stepGroup(wheelDir(ev))
	}
	return m.stepInGroup(wheelDir(ev))
}

// sideArrows is groups with the arrows as the sidebar has them, for the
// prefix hint: ↑ ↓ along a list, ← → between the two.
func sideArrows(groups []commandGroup) []commandGroup {
	side := map[string]commandEntry{
		"up":    {key: "↑", label: "Tab up", run: "up", desc: "the tab above, or repository in the repository list; the prefix stays on for more"},
		"down":  {key: "↓", label: "Tab down", run: "down", desc: "the tab below, or repository in the repository list; the prefix stays on for more"},
		"left":  {key: "←", label: "Repositories / tabs", run: "left", desc: "the arrows move to the other list: the repositories or their tabs"},
		"right": {key: "→", label: "Tabs / repositories", run: "right", desc: "the arrows move to the other list: the repositories or their tabs"},
	}
	out := make([]commandGroup, len(groups))
	for i, g := range groups {
		entries := make([]commandEntry, 0, len(g.entries))
		for _, e := range g.entries {
			if s, ok := side[e.run]; ok {
				e = s
			}
			entries = append(entries, e)
		}
		out[i] = commandGroup{g.title, entries}
	}
	return out
}
