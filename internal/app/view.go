package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	// The active repository and the active tab are blue, as the review
	// panel's active F key is; the tab a darker blue, so the two stand
	// apart in the rows right on top of each other. While the prefix is
	// on, the active entry of the row the arrows walk turns a lighter
	// blue, so you see which row ← → moves along.
	tabBarBg      = lipgloss.Color("235")
	groupActiveBg = lipgloss.Color("25")
	tabActiveBg   = lipgloss.Color("24")
	navActiveBg   = lipgloss.Color("31")
	// accent marks where you are: the frame of the pane with the
	// keyboard. A quiet blue, the review panel's too, so it does not
	// compete with what is in the panes.
	accent       = lipgloss.Color("110")
	groupAccent  = lipgloss.Color("67")
	frameColor   = lipgloss.Color("240")
	lightColours = map[core.Status]lipgloss.Color{
		core.StStarting: lipgloss.Color("244"),
		core.StIdle:     lipgloss.Color("46"),
		core.StWorking:  lipgloss.Color("220"),
		core.StBlocked:  lipgloss.Color("196"),
		core.StDone:     lipgloss.Color("46"),
		core.StUnknown:  lipgloss.Color("244"),
		core.StExited:   lipgloss.Color("239"),
		core.StAsleep:   lipgloss.Color("67"),
		// Cyan: nothing to do with yellow's "at work" or the blue of asleep.
		core.StBackground: lipgloss.Color("45"),
	}
	// lightSymbols draws the lights as shapes of their own rather than
	// circles told apart by colour: status_lights = "symbols".
	lightSymbols bool
)

// light is a tab's traffic light: a filled circle for
// something that wants a look (working, needs input, done and not yet
// seen), a hollow one once it has been seen or has nothing to show.
func light(s core.Status) string {
	return lipgloss.NewStyle().Foreground(lightColours[s]).Render(lightGlyph(s))
}

func lightGlyph(s core.Status) string {
	if lightSymbols {
		// Symbols, which keep the colours too.
		switch s {
		case core.StBlocked:
			return "×"
		case core.StWorking:
			return "◐"
		case core.StBackground:
			return "◎"
		case core.StDone:
			return "✓"
		case core.StIdle:
			return "○"
		case core.StExited:
			return "◌"
		case core.StAsleep:
			return "z"
		}
		return "·"
	}
	switch s {
	case core.StIdle, core.StStarting, core.StUnknown:
		return "○"
	case core.StExited:
		return "◌"
	case core.StAsleep:
		return "z"
	}
	return "●"
}

// lightOn is light on background bg, for the bars: a glyph with no
// background of its own leaves a hole in a highlighted entry.
func lightOn(s core.Status, bg lipgloss.Color) string {
	return lipgloss.NewStyle().Background(bg).Foreground(lightColours[s]).Render(lightGlyph(s))
}

// lightTip is lightOn with a tip saying what the light means; of says
// what it is the light of, when that is not the tab it is on.
func (m *Model) lightTip(s core.Status, bg lipgloss.Color, of string) string {
	tip := statusTip(s)
	if of != "" {
		tip += " — " + of
	}
	return m.tip(lightOn(s, bg), tip)
}

// statusTip is what a light in status s means, for its tip.
func statusTip(s core.Status) string {
	switch s {
	case core.StStarting:
		return "Starting"
	case core.StIdle:
		return "Idle"
	case core.StWorking:
		return "Working"
	case core.StBackground:
		return "Turn over; a process it started in the background still runs"
	case core.StBlocked:
		return "Needs input: the agent is asking you something"
	case core.StDone:
		return "Done, and not looked at yet"
	case core.StExited:
		return "Exited"
	case core.StAsleep:
		return "Asleep: stopped while done and nobody looked; carries on when you visit the tab"
	}
	return "State not known"
}

// View draws the screen, and notes where its zones ended up for the
// mouse; see zones.go.
func (m *Model) View() string {
	m.fleet.Lock()
	defer m.fleet.Unlock()
	if m.sameFrame && m.frame != "" && m.frameGen == m.fleet.Gen() {
		m.sameFrame = false
		m.syncPointer()
		return m.frame
	}
	m.sameFrame = false
	m.sideMemoOK = false
	m.frameGen = m.fleet.Gen()
	term.NextFrame()
	m.zoneFns, m.zoneTips, m.zoneAreas = m.zoneFns[:0], m.zoneTips[:0], m.zoneAreas[:0]
	m.dialogZones, m.hintZones = noZones, noZones
	m.underZones, m.panelZones = 0, 0
	m.uiKeyFocused = m.focusedPluginKey()
	frame, rects := scanZones(m.draw())
	for _, mc := range m.fleet.Machines() {
		if mc.Screens != nil {
			mc.Screens.Shown()
		}
	}
	m.zoneRects = rects
	if m.hover.shown {
		frame = strings.Join(m.drawTip(strings.Split(frame, "\n")), "\n")
	}
	m.frame = frame
	m.syncPointer()
	if m.wheelStepped {
		// The wheel's gap runs from here; see wheelStep.
		m.lastWheelStep, m.wheelStepped = time.Now(), false
	}
	return frame
}

// noZones is a zone id past any there is.
const noZones = 1 << 30

func (m *Model) draw() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.editor != nil {
		return strings.Join(fitLines(m.drawPane(m.editor, 0, true), m.width, m.height), "\n")
	}
	var bars string
	if m.sideW() > 0 {
		bars = m.sideBarRow()
	} else {
		bars = m.tabBar()
	}
	if m.showGroups() {
		bars = groupRowBg(m.groupBar()) + "\n" + bars
	}
	bodyH := m.bodyH()
	var body []string
	under := len(m.zoneFns)
	switch {
	case m.splitShown():
		l, r := m.sides()
		lw, rw := m.splitWidths()
		body = besides(m.tabLines(l, lw, bodyH), m.gap(), m.tabLines(r, rw, bodyH))
	default:
		body = m.agentLines(m.mainW(), bodyH)
	}
	if m.panelDlg || m.global.shown {
		// The global plugins are a dialog as the panel's is, over the tab
		// that stays active underneath.
		m.underZones, m.panelZones = under, len(m.zoneFns)
		body = m.panelDialogOver(body)
	}
	if m.railShown() {
		body = besides(body, " ", m.railLines(bodyH))
	}
	if m.sideW() > 0 {
		body = m.peekSide(besides(m.sideLines(bodyH), "", body))
	}
	if t := m.Cur(); m.prefix && m.nav && m.dialog == nil && t != nil {
		if rt, d := t.ReviewToTell(); d != nil {
			// Over the agent, clear of the sidebar walked and the hint at
			// the bottom.
			left := m.sideW()
			if m.sideSmall && left > 0 {
				left = m.wideW() // peekSide's
			}
			w := m.width - left
			if card := m.reviewPeek(rt, d, w); card != "" {
				cw := lipgloss.Width(strings.SplitN(card, "\n", 2)[0])
				body = overlayAt(body, card, m.width, left+(w-cw)/2, 2)
			}
		}
	}
	// The bottom bar's zones before the dialog's: those from dialogZones
	// on are taken for the dialog's, and its buttons must not work through
	// it.
	var foot string
	if m.footRows() > 0 {
		foot = "\n" + m.bottomBarRow()
	}
	if c, ok := m.dialog.(*closeTab); ok && c.hidden() {
		// Not drawn until it knows what it is asking; see closeWait.
	} else if d, ok := m.dialog.(*moveDialog); ok && d.hidden(m) {
		// Nor until the move has something to ask; see moveWait.
	} else if m.dialog != nil {
		m.dialogZones = len(m.zoneFns)
		// The whole box is a zone of its own, so a click on it that
		// misses its buttons is not taken for a click outside.
		box := m.area(m.dialog.view(m, m.width, bodyH), func(*Model, tea.MouseMsg) tea.Cmd { return nil })
		if a, ok := m.dialog.(anchored); ok {
			x, y := a.anchor()
			body = overlayAt(body, box, m.width, x, y-m.barRows())
		} else {
			body = overlay(body, box, m.width)
		}
	}
	if m.hintShown() {
		// What the prefix can be followed by, in a box in the middle,
		// until the next key.
		m.hintZones = len(m.zoneFns)
		box := m.area(m.prefixHint(m.width, len(body)), func(*Model, tea.MouseMsg) tea.Cmd { return nil })
		body = overlay(body, box, m.width)
	}
	return bars + "\n" + strings.Join(body, "\n") + foot
}

// besides joins two columns of the body with the one between them.
func besides(left []string, gap string, right []string) []string {
	body := make([]string, len(left))
	for i := range body {
		body[i] = left[i] + gap + right[i]
	}
	return body
}

// gap is the column between the two sides of the body. It is where the
// sides are dragged wider or narrower, and shows a line while it is.
func (m *Model) gap() string {
	if m.drag == dragRule {
		return lipgloss.NewStyle().Foreground(accent).Render("│")
	}
	return " "
}

// agentLines draws the active tab's panes into a w×h area.
func (m *Model) agentLines(w, h int) []string {
	return m.tabLines(m.Cur(), w, h)
}

// framed draws body, (w-2)×(h-2), in a rounded frame with title in its
// top edge.
func framed(body []string, w, h int, title string, active bool) []string {
	return framedEnd(body, w, h, title, "", active)
}

// framedEnd is framed with end, a button, at the right of its top edge.
func framedEnd(body []string, w, h int, title, end string, active bool) []string {
	bs := lipgloss.NewStyle().Foreground(frameColor)
	ts := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	if active {
		bs = lipgloss.NewStyle().Foreground(accent)
		ts = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	}
	top := bs.Render("╭" + strings.Repeat("─", max(w-2, 0)) + "╮")
	if title == "" && end != "" && w > 8 {
		if ew := lipgloss.Width(end); w-4 >= ew+2 {
			top = bs.Render("╭"+strings.Repeat("─", w-ew-5)) + " " + end + " " + bs.Render("─╮")
		}
	}
	if title != "" && w > 8 {
		title = ansi.Truncate(title, w-6, "…")
		tw := lipgloss.Width(title)
		top = bs.Render("╭─") + ts.Render(" ") + ts.Render(title) + ts.Render(" ") +
			bs.Render(strings.Repeat("─", max(w-tw-5, 0))+"╮")
		if ew := lipgloss.Width(end); end != "" && w-tw-5 >= ew+3 {
			top = bs.Render("╭─") + ts.Render(" ") + ts.Render(title) + ts.Render(" ") +
				bs.Render(strings.Repeat("─", w-tw-5-ew-3)) + " " + end + " " + bs.Render("─╮")
		}
	}
	out := make([]string, 0, h)
	out = append(out, top)
	side := bs.Render("│")
	for i := 0; i < h-2 && i < len(body); i++ {
		out = append(out, side+body[i]+"\x1b[0m"+side)
	}
	out = append(out, bs.Render("╰"+strings.Repeat("─", max(w-2, 0))+"╯"))
	return out
}

// activeBg is the background of the active tab in the tab row.
func (m *Model) activeBg() lipgloss.Color {
	if m.prefix && !m.onGroups {
		return navActiveBg
	}
	return tabActiveBg
}

// barStatus is what the right of the bars shows: nerdr's messages — the
// badges and the help as a rule, in their place the prefix's state or a
// toast. Its "? commands" opens the palette of every command. reserve is the columns the entries on the
// left leave them: msgRoom, or what the badges need when that is more,
// though never more than half the row. It is kept whether a message
// shows or not, so the entries do not move about as messages come and go.
// hostBadge names the machine the active tab's core runs on, when it is
// not this one and has no name of its own among the TUI's: one that does
// is named by its groups' badges (machineBadge).
func (m *Model) hostBadge() string {
	mc := m.machineOf(m.Cur())
	host := mc.Host
	if host == "" || mc.Name != "" {
		return ""
	}
	return m.tip(lipgloss.NewStyle().Background(machineUpBg).Foreground(lipgloss.Color("231")).Bold(true).
		Render(" "+host+" "), "The agents of these tabs run on "+host)
}

func (m *Model) barStatus() (msg string, reserve int) {
	hint := " " + m.pk("?") + " commands "
	if m.focus == core.FocusReview {
		hint = " review · esc → agent" + hint
	}
	status := m.hostBadge()
	if !m.sideMachines() {
		// The wide sidebar lists the machines at its foot.
		status = m.emptyMachines() + status
	}
	if m.footRows() == 0 {
		// Else the bottom bar has them.
		status += m.prBadge() + m.issueBadge() + m.waitingBadge()
	}
	status += m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("244")).Render(hint),
		func(m *Model) tea.Cmd {
			return m.openPalette()
		}), "Every command nerdr has, to search and run")
	msg = status
	switch {
	case m.prefix:
		label := " " + m.cfg().Prefix + " … "
		if m.nav {
			label = " " + m.cfg().Prefix + " ← → tabs · enter to stay "
			if len(m.fleet.Groups()) > 1 {
				label = " " + m.cfg().Prefix + " ← → tabs · ↑ repos · enter to stay "
			}
			if m.onGroups {
				label = " " + m.cfg().Prefix + " ← → repos · ↓ tabs · enter to stay "
			}
			if m.sideW() > 0 {
				label = " " + m.cfg().Prefix + " ↑ ↓ tabs · ← → repos · enter to stay "
				if m.onGroups {
					label = " " + m.cfg().Prefix + " ↑ ↓ repos · ← → tabs · enter to stay "
				}
			}
		}
		msg = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Bold(true).
			Render(label)
	case m.toastText != "" && time.Now().Before(m.toastUntil):
		msg = lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Render(" " + m.toastText + " ")
	}
	reserve = min(max(msgRoom, lipgloss.Width(status)), m.width/2)
	return msg, reserve
}

// groupBar is the repository row, over the tab row: one entry per group
// of tabs, with the light of the tab in it that most needs attention, and
// on the right nerdr's messages.
func (m *Model) groupBar() string {
	bar := lipgloss.NewStyle().Background(tabBarBg)
	cur := m.curGroup()
	msg, reserve := m.barStatus()

	gs := m.fleet.Groups()
	items := make([]barItem, len(gs))
	at := 0
	for i, g := range gs {
		n := len(m.fleet.GroupTabs(g))
		var part string
		if g == cur {
			at = i
			bg := groupActiveBg
			if m.prefix && m.onGroups {
				bg = navActiveBg
			}
			st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("231")).Bold(true)
			part = m.machineBadge(g) + st.Render(" ") + m.lightTip(m.fleet.GroupStatus(g), bg, "the tab here that most wants a look") + st.Render(" "+g+" ") +
				lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("250")).Render("("+strconv.Itoa(n)+") ")
		} else {
			st := lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("250"))
			part = m.machineBadge(g) + st.Render(" ") + m.lightTip(m.fleet.GroupStatus(g), tabBarBg, "the tab here that most wants a look") + st.Render(" "+g+" ") +
				lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("241")).Render("("+strconv.Itoa(n)+") ")
		}
		part = m.tip(part, g+": "+core.Plural(n, "tab")+" · click: go there · right-click: more")
		items[i] = barItem{st: m.fleet.GroupStatus(g), s: m.zone(part, func(m *Model, ev tea.MouseMsg) tea.Cmd {
			switch {
			case ev.Action != tea.MouseActionPress:
				return nil
			case ev.Button == tea.MouseButtonLeft:
				return m.selectGroup(g)
			case ev.Button == tea.MouseButtonRight:
				return m.openGroupMenu(g, ev.X, m.barRows())
			}
			return nil
		})}
	}
	var plus string
	if m.mouseOn() {
		// A new tab in any repository; the tab row's + is for its group.
		plus = bar.Render(" ") + m.tip(m.onClick(plusStyle.Render(" + "), (*Model).openWizard), "New worktree, in any repository, with a tab of its own")
	}
	avail := m.width - reserve - lipgloss.Width(plus)
	left := m.scrollBar(items, at, &m.groupRow, avail, "repository",
		func(m *Model, i int) tea.Cmd {
			if gs := m.fleet.Groups(); i < len(gs) {
				return m.selectGroup(gs[i])
			}
			return nil
		})
	left += plus
	// A message longer than the room it has is cut, not the repositories.
	msg = m.fitMsg(msg, m.width-lipgloss.Width(left)-1)
	return joinBar(left, msg, m.width, bar)
}

// groupBarBg is the repository row's background: darker than the tab
// row's, so the two rows stand apart without a row between them.
const groupBarBg = "233"

// groupRowBg gives the repository row its own background. The row is
// drawn on the bars' background, as the tab row is, from parts shared
// with it (the messages, the scroll markers); its
// background is swapped afterwards rather than passed down to each.
func groupRowBg(s string) string {
	return strings.ReplaceAll(s, "48;5;"+string(tabBarBg), "48;5;"+groupBarBg)
}

// msgRoom is the columns the bars keep for nerdr's messages: room for
// most of them, which are a tab's name and a few words.
const msgRoom = 50

// tabBar is the tab row: the tabs of the active group, numbered within
// it, each with its traffic light. Without the repository row above it,
// it has nerdr's messages on its right too, and, with more than one
// repository, the active one's name on its left.
func (m *Model) tabBar() string {
	bar := lipgloss.NewStyle().Background(tabBarBg)
	var items []barItem
	at := 0
	now := time.Now()
	g := m.curGroup()
	idx := m.fleet.GroupTabs(g)
	for n, i := range idx {
		t := m.fleet.Tabs()[i]
		num := strconv.Itoa(n+1) + ":"
		bg := tabBarBg
		if i == m.Active {
			bg = m.activeBg()
		}
		var extra string
		cold := m.machineOf(t).Core.Cold(t, now)
		if cold {
			extra = m.tip(lipgloss.NewStyle().Background(bg).Foreground(groupAccent).Render("❄"), "Cold: not used for a while") +
				lipgloss.NewStyle().Background(bg).Render(" ")
		}
		if mark := m.reviewMark(t, bg); mark != "" {
			extra += mark + lipgloss.NewStyle().Background(bg).Render(" ")
		}
		if t == m.splitTab() {
			side, where := "◨", "right"
			if m.splitLeft {
				side, where = "◧", "left"
			}
			extra += m.tip(lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("81")).Render(side), "Shown beside the active tab, on the "+where) +
				lipgloss.NewStyle().Background(bg).Render(" ")
		}
		// The active tab's details are the bottom bar's, when it shows.
		if m.showDetails && !(i == m.Active && m.footRows() > 0) {
			if d := m.details(t, bg, i == m.Active); d != "" {
				extra += d + lipgloss.NewStyle().Background(bg).Render(" ")
			}
		}
		var part string
		if i == m.Active {
			st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("231")).Bold(true)
			part = m.tabBadge(t) + st.Render(" ") + m.lightTip(t.Lit(), bg, "") + st.Render(" "+num+t.Name+" ") +
				lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("248")).Render(string(t.Agent)+" ") + extra
		} else {
			st := lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("250"))
			if cold || grey(m.machineOf(t)) {
				st = st.Foreground(lipgloss.Color("243"))
			}
			part = m.tabBadge(t) + st.Render(" ") + m.lightTip(t.Lit(), bg, "") + st.Render(" "+num+t.Name+" ") + extra
		}
		if i == m.Active {
			at = n
		}
		part = m.tip(part, t.Name+" · "+string(t.Agent)+" · "+t.Lit().Label()+" · right-click: more · middle-click: close")
		items = append(items, barItem{st: t.Lit(), s: m.zone(part, func(m *Model, ev tea.MouseMsg) tea.Cmd { return m.tabMouse(t, ev) })})
	}
	var plus string
	if m.mouseOn() {
		plus = bar.Render(" ") + m.tip(m.onClick(plusStyle.Render(" + "), func(m *Model) tea.Cmd {
			if repo := tabRepo(m.Cur()); repo != "" {
				return m.openWizardFor(repo)
			}
			return m.openWizard()
		}), "New worktree, in this repository, with a tab of its own")
	}
	warning := m.topWarning()
	var repo, msg string
	reserve := lipgloss.Width(warning)
	if !m.showGroups() {
		m.groupRow = barRow{} // not drawn, so nothing hidden in it
		var r int
		msg, r = m.barStatus()
		reserve += r
		if len(m.fleet.Groups()) > 1 {
			repo = m.repoLabel(g)
		}
	}
	if m.tabRows == nil {
		m.tabRows = map[string]*barRow{}
	}
	row := m.tabRows[g]
	if row == nil {
		row = &barRow{}
		m.tabRows[g] = row
	}
	left := repo + m.scrollBar(items, at, row, m.width-reserve-lipgloss.Width(plus)-lipgloss.Width(repo), "tab",
		func(m *Model, i int) tea.Cmd {
			if idx := m.fleet.GroupTabs(g); i < len(idx) {
				return m.selectTab(idx[i])
			}
			return nil
		}) + plus
	// A message longer than the room it has is cut, not the tabs.
	msg = m.fitMsg(msg, m.width-lipgloss.Width(left)-lipgloss.Width(warning)-1)
	return joinBar(left, warning+msg, m.width, bar)
}

// overlapWarning is the warning in the bar that other tabs change some of
// the active tab's files; "" when none do.
func (m *Model) overlapWarning() string {
	t := m.Cur()
	if t == nil {
		return ""
	}
	ov := m.machineOf(t).Core.Overlaps[t.ID]
	if len(ov) == 0 {
		return ""
	}
	n := 0
	for _, o := range ov {
		n += len(o.Files)
	}
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).
		Render(" ⚠ "+core.Plural(n, "file")+" also changed in "+ov[0].With.Name+moreTabs(len(ov)-1)+" · "+m.pk("g g")+" "),
		(*Model).showBranchPR), "Other tabs change some of the same files · click: which")
}

// repoLabel names repository g at the start of the tab row while the
// repository row is put away, so it is still clear whose tabs these are;
// a click brings the row back.
func (m *Model) repoLabel(g string) string {
	bg := tabBarBg
	if m.prefix && m.onGroups {
		bg = navActiveBg
	}
	s := m.machineBadge(g) + lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("250")).Bold(true).Render(" "+g+" ") +
		lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("241")).Render("› ")
	return m.tip(m.onClick(s, func(m *Model) tea.Cmd {
		m.hideGroups = false
		return nil
	}), "Repository "+g+" · "+m.pk("]")+": next · click or "+m.pk("l b")+": show all repositories")
}

// tabMouse handles the mouse on tab t in the tab bar: a click goes to it,
// a middle click closes it, a right click shows what can be done to it.
func (m *Model) tabMouse(t *core.Tab, ev tea.MouseMsg) tea.Cmd {
	i := m.fleet.TabIndex(t)
	if ev.Action != tea.MouseActionPress || i < 0 {
		return nil
	}
	switch ev.Button {
	case tea.MouseButtonLeft:
		return m.selectTab(i)
	case tea.MouseButtonMiddle:
		return tea.Batch(m.selectTab(i), m.openClose())
	case tea.MouseButtonRight:
		y := m.barRows()
		if m.sideW() > 0 {
			y = ev.Y + 1 // under the tab, in the list
		}
		return m.openTabMenu(t, ev.X, y)
	}
	return nil
}

var (
	buttonStyle = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Bold(true)
	plusStyle   = lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("81")).Bold(true)
)

// joinBar puts left and right on one row of width w, filling between.
func joinBar(left, right string, w int, bar lipgloss.Style) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw > w {
		left = ansi.Truncate(left, max(w-rw, 0), "…")
		lw = lipgloss.Width(left)
	}
	return left + bar.Render(strings.Repeat(" ", max(w-lw-rw, 0))) + right
}

func moreTabs(n int) string {
	if n <= 0 {
		return ""
	}
	return " +" + strconv.Itoa(n)
}

// fitLines makes lines exactly w columns wide and h rows tall.
func fitLines(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		var l string
		if i < len(lines) {
			l = lines[i]
		}
		// Measured once, and cut only when too wide: most lines fit.
		n := ansi.StringWidth(l)
		if n > w {
			l = ansi.Truncate(l, w, "")
			n = ansi.StringWidth(l)
		}
		if n < w {
			l += strings.Repeat(" ", w-n)
		}
		out[i] = l
	}
	return out
}

func padTo(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// overlay draws box centred over body.
func overlay(body []string, box string, w int) []string {
	bl := strings.Split(box, "\n")
	bw := 0
	for _, l := range bl {
		bw = max(bw, lipgloss.Width(l))
	}
	return overlayAt(body, box, w, (w-bw)/2, (len(body)-len(bl))/2)
}

// overlayAt draws box over body with its top-left corner at (left, top),
// moved in as far as needed to fit.
func overlayAt(body []string, box string, w, left, top int) []string {
	bl := strings.Split(box, "\n")
	bw := 0
	for _, l := range bl {
		bw = max(bw, lipgloss.Width(l))
	}
	top = max(min(top, len(body)-len(bl)), 0)
	left = max(min(left, w-bw), 0)
	for i, l := range bl {
		y := top + i
		if y >= len(body) {
			break
		}
		row := body[y]
		prefix := ansi.Truncate(row, left, "")
		prefix = padTo(prefix, left)
		suffix := ansi.TruncateLeft(row, left+bw, "")
		body[y] = prefix + "\x1b[0m" + padTo(l, bw) + "\x1b[0m" + suffix
	}
	return body
}

func padBg(s string, w int, st lipgloss.Style) string {
	if n := lipgloss.Width(s); n < w {
		return s + st.Render(strings.Repeat(" ", w-n))
	}
	return s
}
