package app

import (
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A tab is a worktree, and it can have several panes working in it side by
// side: its own agent, which it starts with, and any number of terminals,
// other agents and code reviews of an agent's work. A new one goes last,
// and they can be moved along; its own agent stays first. With more than one, each frame names its
// pane, with its agent's light: the tab's light in the tab list is the
// one of them that most wants a look.
//
// The panes are shown as columns, each in its frame. When there are more
// than the tab has room for at pane_min_width columns each, the ones used
// longest ago are folded into a row over the others (shown "minimised"),
// and the one the keyboard is in is always shown. A pane can be minimised
// by hand too; a click on it in the row, or the keyboard going to it,
// brings it back. The row is there only while a pane is in it.
//
// A review pane reviews the agent of the pane it was opened from: the
// findings go back to that agent, which is what it is for. Its other
// agents are tabs of their own as far as running them goes (started,
// read off the screen and sent messages as a tab's agent is), but are not
// in the tab list: they belong to the tab, and go with it. They are kept
// with it, and resumed when nerdr starts again. Terminals are not: what
// runs in them stops with nerdr.

// rowShown reports whether t has the row over its panes: while one of
// them is minimised, there to be brought back.
func (m *Model) rowShown(t *core.Tab) bool {
	_, w, _ := m.tabArea(t)
	return len(m.PaneCols(t, w)) < len(t.Panes())
}

// rowH is the rows the pane row takes over t's panes.
func (m *Model) rowH(t *core.Tab) int {
	if m.rowShown(t) {
		return 1
	}
	return 0
}

// dividerAt is which divider between t's panes is at (x, y) of its area —
// the column between two panes, or the frame either side of it — as the
// number of the pane before it; -1 for none.
func (m *Model) dividerAt(t *core.Tab, x, y int) int {
	_, aw, ah := m.tabArea(t)
	if y < m.rowH(t) || y >= ah {
		return -1
	}
	cols := m.PaneCols(t, aw)
	for i := 0; i+1 < len(cols); i++ {
		if gap := cols[i].X + cols[i].W; x >= gap-1 && x <= gap+1 {
			return i
		}
	}
	return -1
}

// dragDivider moves the divider after t's i'th pane shown to column x of
// its area: the two panes either side of it share their room anew, and
// the others keep theirs.
func (m *Model) dragDivider(t *core.Tab, i, x int) tea.Cmd {
	_, aw, _ := m.tabArea(t)
	cols := m.PaneCols(t, aw)
	if i < 0 || i+1 >= len(cols) {
		return nil
	}
	a, b := cols[i], cols[i+1]
	both := a.W + b.W
	aw2 := min(max(x-a.X, core.PaneMinShare), both-core.PaneMinShare)
	if aw2 == a.W {
		return nil
	}
	for _, c := range cols {
		c.P.Weight = float64(c.W)
	}
	a.P.Weight, b.P.Weight = float64(aw2), float64(both-aw2)
	m.resizePanes()
	return nil
}

// paneShown reports whether p, one of t's panes, is on screen when t is.
func (m *Model) paneShown(t *core.Tab, p *core.PaneTab) bool {
	_, w, _ := m.tabArea(t)
	for _, c := range m.PaneCols(t, w) {
		if c.P == p {
			return true
		}
	}
	return false
}

// tabArea is where tab t is drawn, its panes and their row: the column it
// starts at, and its width and height. A tab not on screen is given the
// active tab's, where it appears when gone to.
func (m *Model) tabArea(t *core.Tab) (x, w, h int) {
	aw, ah := m.agentArea()
	x = m.agentX()
	if t != nil && m.splitShown() && t == m.split {
		lw, rw := m.splitWidths()
		x, aw = m.sideW()+lw+1, rw
		if m.splitLeft {
			x, aw = m.sideW(), lw
		}
	}
	return x, aw, ah
}

// paneTop is the rows above the program in pane p, inside its frame of
// w×h: an agent's task panel, a review's state and keys.
func (m *Model) paneTop(p *core.PaneTab, w, h int) int {
	switch {
	case p.Kind == core.PaneAgent:
		return len(m.taskRows(p.A, w, h))
	case p.Kind == core.PaneReview && p.A != nil:
		return reviewerHeadRows
	}
	return 0
}

// paneRect is where the program in t's pane p is drawn on the screen: its
// top-left corner and its size. One not on screen is given the size it
// would have in place of the pane with the keyboard.
func (m *Model) paneRect(t *core.Tab, p *core.PaneTab) (x, y, w, h int) {
	ax, aw, ah := m.tabArea(t)
	ch := max(ah-m.rowH(t), 3)
	cols := m.PaneCols(t, aw)
	c := cols[0]
	if i := slices.IndexFunc(cols, func(o core.PaneCol) bool { return o.P == p }); i >= 0 {
		c = cols[i]
	} else if i := slices.IndexFunc(cols, func(o core.PaneCol) bool { return o.P == t.Focused() }); i >= 0 {
		c = cols[i]
	}
	iw, ih := max(c.W-2, 1), max(ch-2, 1)
	top := m.paneTop(p, iw, ih)
	return ax + c.X + 1, m.barRows() + m.rowH(t) + 1 + top, iw, max(ih-top, 1)
}

// paneSize is the size of the program in agent a's pane.
func (m *Model) paneSize(a *core.Tab) (int, int) {
	o := a.Owner()
	p := a.PaneOf()
	if p == nil {
		p = o.Focused()
	}
	_, _, w, h := m.paneRect(o, p)
	return w, h
}

// shellSize is the size of the terminal in t's pane p: a column narrower
// than the pane, so the end of a long prompt and the cursor at it are not
// against the frame, nor cut off where the terminal draws a character
// (an icon in the prompt) wider than nerdr counts it.
func (m *Model) shellSize(t *core.Tab, p *core.PaneTab) (int, int) {
	_, _, w, h := m.paneRect(t, p)
	return max(w-core.ShellMargin, 1), h
}

// focusPane gives p, one of t's panes, the keyboard: it is shown, and
// counts as used now.
func (m *Model) focusPane(t *core.Tab, p *core.PaneTab) tea.Cmd {
	if t.On != p {
		m.scrolling = false
		t.Scroll = 0
	}
	t.On = p
	p.Small = false
	p.Used = time.Now()
	if a := p.A; a != nil && p.Kind != core.PaneShell {
		a.Seen()
		a.QueuePaused = false
	}
	m.setFocus(core.FocusAgent)
	m.focusTerms(t)
	return m.resize()
}

// focusTerms tells t's programs which of them has the keyboard, for those
// that draw it (a cursor, a focus ring).
func (m *Model) focusTerms(t *core.Tab) {
	f := t.Focused()
	for _, p := range t.Panes() {
		if tp := p.Term(); tp != nil {
			tp.Focus(p == f && t == m.Cur() && !m.blurred)
		}
	}
}

// dropPane takes p out of t's panes; the keyboard goes to the one used
// last when it had it, mostly the one it was opened from.
func (m *Model) dropPane(t *core.Tab, p *core.PaneTab) {
	ps := t.Panes()
	i := slices.Index(ps, p)
	if i < 0 || p == t.OwnPane() {
		return
	}
	if t.On == p {
		var next *core.PaneTab
		for _, q := range ps {
			if q != p && (next == nil || q.Used.After(next.Used)) {
				next = q
			}
		}
		t.On = next
		next.Used = time.Now()
		next.Small = false
		m.scrolling = false
		t.Scroll = 0
	}
	t.PaneList = slices.Delete(ps, i, i+1)
	m.focusTerms(t)
	m.resizePanes()
}

// minimisePane folds p into the row, the keyboard going to the next pane
// shown; the tab's last pane shown stays.
func (m *Model) minimisePane(t *core.Tab, p *core.PaneTab) tea.Cmd {
	if p.Small {
		return m.focusPane(t, p)
	}
	var other *core.PaneTab
	_, w, _ := m.tabArea(t)
	for _, c := range m.PaneCols(t, w) {
		if c.P != p {
			other = c.P
		}
	}
	if other == nil {
		for _, q := range t.Panes() {
			if q != p && !q.Small {
				other = q
			}
		}
	}
	if other == nil {
		m.toast("the last pane shown stays · " + m.pk("t t") + " opens a terminal beside it")
		return nil
	}
	p.Small = true
	if t.Focused() == p {
		return m.focusPane(t, other)
	}
	return m.resize()
}

// askClosePane closes the pane with the keyboard, asking first when an
// agent in it is at work or a terminal has a foreground command.
func (m *Model) askClosePane() tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	p := t.Focused()
	if p.Kind == core.PaneShell && p.Shell != nil && p.Shell.Busy() {
		m.dialog = &confirm{
			title:     "Stop the process and close the terminal?",
			lines:     []string{"A process is still running in this terminal."},
			defaultNo: true,
			yesLabel:  "Stop and close", noLabel: "Cancel",
			yes: func(m *Model) tea.Cmd {
				if m.TabIndex(t) < 0 || !slices.Contains(t.Panes(), p) {
					return nil
				}
				return fromCore(m.ClosePane(t, p))
			},
		}
		return nil
	}
	if p.Kind == core.PaneShell || p.A == nil || !p.A.Light.AtWork() || p == t.OwnPane() {
		return fromCore(m.ClosePane(t, p))
	}
	m.dialog = &confirm{
		title: "Stop " + m.paneName(t, p) + "?",
		lines: []string{closeLine(p.A)},
		yes: func(m *Model) tea.Cmd {
			if m.TabIndex(t) < 0 || !slices.Contains(t.Panes(), p) {
				return nil
			}
			return fromCore(m.ClosePane(t, p))
		},
	}
	return nil
}

// openNewAgent offers the agents on PATH to start beside the active tab's,
// in the same worktree.
func (m *Model) openNewAgent() tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	var items []menuItem
	for _, k := range agents.Available(m.Cfg.Agents) {
		items = append(items, menuItem{label: string(k), key: strconv.Itoa(len(items) + 1),
			run: func(m *Model) tea.Cmd { return fromCore(m.NewAgent(t, k)) }})
	}
	if len(items) == 0 {
		m.toast("no agent on PATH")
		return nil
	}
	items = append([]menuItem{{label: "A new conversation in " + filepath.Base(t.Dir) + ", beside " + string(t.Agent)}}, items...)
	m.dialog = &menu{title: "New agent in " + t.Name, items: items, x: m.width / 3, y: m.height / 3, hi: 1}
	return nil
}

// paneName is what p is called in its frame and in the row.
func (m *Model) paneName(t *core.Tab, p *core.PaneTab) string {
	switch p.Kind {
	case core.PaneShell:
		name := "terminal"
		if p.N > 1 {
			name += " " + strconv.Itoa(p.N)
		}
		if p.Shell != nil {
			if title := core.ShellTitle(p.Shell.Title(), t.Dir); title != "" {
				name += ": " + ansi.Truncate(title, 24, "…")
			}
		}
		return name
	case core.PaneReview:
		if p.A == nil {
			return "review of " + m.AgentLabel(t, p.Of) + "…"
		}
		return string(p.A.Agent) + " reviews " + m.AgentLabel(t, p.Of)
	}
	return m.AgentLabel(t, p.A)
}

// agentName is how a notification names agent a: its tab's name, and
// which of its agents it is when not the tab's own.
func (m *Model) agentName(a *core.Tab) string {
	o := a.Owner()
	if o == a {
		return a.Name
	}
	if p := a.PaneOf(); p != nil {
		return o.Name + " · " + m.paneName(o, p)
	}
	return o.Name + " · " + string(a.Agent)
}

// paneRow is the row over t's panes, w wide: the minimised ones, each
// with its light, to be brought back; and the buttons for new panes on
// the right. The panes shown are named in their frames.
func (m *Model) paneRow(t *core.Tab, w int) string {
	bar := lipgloss.NewStyle().Background(tabBarBg)
	_, aw, _ := m.tabArea(t)
	cols := m.PaneCols(t, aw)
	var small []*core.PaneTab
	for _, p := range t.Panes() {
		if !slices.ContainsFunc(cols, func(c core.PaneCol) bool { return c.P == p }) {
			small = append(small, p)
		}
	}
	var items []barItem
	for _, p := range small {
		bg := tabBarBg
		st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("245"))
		tip := m.paneName(t, p) + " · minimised"
		part := st.Render(" ")
		s, ok := p.Status()
		switch {
		case ok:
			part += m.lightTip(s, bg, "") + st.Render(" ")
		case p.Kind == core.PaneShell:
			part += lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("114")).Render("$") + st.Render(" ")
		}
		part += st.Render("▸" + m.paneName(t, p) + " ")
		part = m.tip(part, tip+" · click: go there · right-click: more · middle-click: close")
		items = append(items, barItem{st: s, s: m.zone(part, func(m *Model, ev tea.MouseMsg) tea.Cmd { return m.paneMouse(t, p, ev) })})
	}
	right := ""
	if m.mouseOn() {
		right = m.newPaneButtons(t, tabBarBg)
	}
	if m.paneBars == nil {
		m.paneBars = map[*core.Tab]*barRow{}
	}
	row := m.paneBars[t]
	if row == nil {
		row = &barRow{}
		m.paneBars[t] = row
	}
	left := m.scrollBar(items, 0, row, w-lipgloss.Width(right), "pane", func(m *Model, i int) tea.Cmd {
		if i < len(small) {
			return m.goToPane(t, small[i])
		}
		return nil
	})
	return joinBar(left, right, w, bar)
}

// newPaneButtons are the buttons that open a terminal, an agent or a code
// review in a new pane, last, on background bg.
func (m *Model) newPaneButtons(t *core.Tab, bg lipgloss.Color) string {
	st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("81")).Bold(true)
	sp := lipgloss.NewStyle().Background(bg).Render(" ")
	btn := func(label, keys, tip string, run func(m *Model) tea.Cmd) string {
		return m.tip(m.onClick(st.Render("+"+label), func(m *Model) tea.Cmd {
			if m.TabIndex(t) >= 0 && t != m.Cur() {
				m.selectTab(m.TabIndex(t))
			}
			return run(m)
		}), tip+" · "+m.pk(keys))
	}
	return btn("terminal", "t t", "A terminal in the worktree, in a new pane", func(m *Model) tea.Cmd { return fromCore(m.NewShell()) }) + sp +
		btn("agent", "t a", "Another agent in the same worktree, in a new conversation", (*Model).openNewAgent) + sp +
		btn("review", "t r", "A second agent reviews the work of the agent whose pane has the keyboard", (*Model).newReviewPane) + sp
}

// goToPane goes to tab t and gives its pane p the keyboard.
func (m *Model) goToPane(t *core.Tab, p *core.PaneTab) tea.Cmd {
	var cmd tea.Cmd
	if i := m.TabIndex(t); i >= 0 && t != m.Cur() {
		cmd = m.selectTab(i)
	}
	return tea.Batch(cmd, m.focusPane(t, p))
}

// goToAgent goes to agent a, a tab's own or another in one of its panes:
// its tab, with its pane given the keyboard.
func (m *Model) goToAgent(a *core.Tab) tea.Cmd {
	o := a.Owner()
	p := a.PaneOf()
	if p == nil {
		return m.selectTab(m.TabIndex(o))
	}
	return m.goToPane(o, p)
}

// paneMouse handles the mouse on pane p in t's row: a click goes to it, a
// middle click closes it, a right click shows what can be done to it.
func (m *Model) paneMouse(t *core.Tab, p *core.PaneTab, ev tea.MouseMsg) tea.Cmd {
	if ev.Action != tea.MouseActionPress || isWheel(ev) {
		return nil
	}
	switch ev.Button {
	case tea.MouseButtonLeft:
		return m.goToPane(t, p)
	case tea.MouseButtonMiddle:
		return tea.Batch(m.goToPane(t, p), m.askClosePane())
	case tea.MouseButtonRight:
		cmd := m.goToPane(t, p)
		m.openPaneMenu(t, p, ev.X, ev.Y+1)
		return cmd
	}
	return nil
}

// openPaneMenu shows what can be done to pane p of t's, at (x, y).
func (m *Model) openPaneMenu(t *core.Tab, p *core.PaneTab, x, y int) {
	small := "Minimise"
	if p.Small {
		small = "Show"
	}
	items := []menuItem{
		{label: m.paneName(t, p)},
		{label: small, key: "t m", run: func(m *Model) tea.Cmd { return m.minimisePane(t, p) }},
		{label: "Close", key: "t x", run: func(m *Model) tea.Cmd { return m.askClosePane() }},
		{label: "New panes"},
		{label: "Terminal", key: "t t", run: func(m *Model) tea.Cmd { return fromCore(m.NewShell()) }},
		{label: "Agent…", key: "t a", run: func(m *Model) tea.Cmd { return m.openNewAgent() }},
		{label: "Code review", key: "t r", run: func(m *Model) tea.Cmd { return m.newReviewPane() }, off: m.commandOff("t r")},
	}
	if p == t.OwnPane() {
		items[2].off = "the tab's first agent stays as long as the tab"
	}
	m.dialog = &menu{title: "Pane", items: items, x: x, y: y, hi: 1}
}

// paneEnd is the buttons at the right of pane p's frame: minimise and
// close, but on a tab's only pane; and, while there is no row over the
// panes to have them, those for new panes on the last pane shown.
func (m *Model) paneEnd(t *core.Tab, p *core.PaneTab) string {
	if !m.mouseOn() {
		return ""
	}
	if len(t.Panes()) == 1 {
		return m.newPaneButtons(t, "")
	}
	out := ""
	if _, w, _ := m.tabArea(t); !m.rowShown(t) {
		if cols := m.PaneCols(t, w); cols[len(cols)-1].P == p {
			out = m.newPaneButtons(t, "") + " "
		}
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	out += m.tip(m.onClick(st.Render("−"), func(m *Model) tea.Cmd { return m.minimisePane(t, p) }), "Minimise · "+m.pk("t m"))
	if p != t.OwnPane() {
		out += " " + m.tip(m.onClick(st.Render("×"), func(m *Model) tea.Cmd {
			cmd := m.goToPane(t, p)
			return tea.Batch(cmd, m.askClosePane())
		}), "Close · "+m.pk("t x"))
	}
	return out
}

// tabLines draws tab t into a w×h area: the row of its minimised panes
// when it has any, and the panes shown, each in its frame.
func (m *Model) tabLines(t *core.Tab, w, h int) []string {
	if t == nil {
		return framed(m.paneLines(nil, nil, w-2, h-2), w, h, "", m.focus == core.FocusAgent)
	}
	var out []string
	if m.rowShown(t) {
		out = append(out, m.paneRow(t, w))
	}
	ch := h - len(out)
	cols := m.PaneCols(t, w)
	var body []string
	for i, c := range cols {
		lines := m.paneColumn(t, c.P, c.W, ch)
		if i == 0 {
			body = lines
			continue
		}
		body = besides(body, " ", lines)
	}
	return append(out, body...)
}

// paneColumn draws t's pane p in its frame, w×h.
func (m *Model) paneColumn(t *core.Tab, p *core.PaneTab, w, h int) []string {
	title := ""
	if one := len(t.Panes()) == 1; !one || m.splitShown() {
		title = m.paneName(t, p)
		if s, ok := p.Status(); ok {
			title = m.tip(light(s), statusTip(s)) + " " + title
		}
		if m.splitShown() && one {
			title += dim(" · " + t.Name)
		}
	}
	active := m.focus == core.FocusAgent && t == m.Cur() && p == t.Focused()
	iw, ih := w-2, h-2
	var body []string
	switch p.Kind {
	case core.PaneAgent:
		body = m.taskLines(p.A, iw, ih)
		body = append(body, m.paneLines(t, p, iw, max(ih-len(body), 1))...)
	case core.PaneReview:
		body = m.reviewPaneLines(t, p, iw, ih)
	default:
		body = m.paneLines(t, p, iw, ih)
	}
	return framedEnd(body, w, h, title, m.paneEnd(t, p), active)
}

// paneLines draws the program in t's pane p into a w×h area: its agent or
// its terminal. Only the pane with the keyboard shows a cursor and can be
// scrolled back.
func (m *Model) paneLines(t *core.Tab, p *core.PaneTab, w, h int) []string {
	if t == nil {
		msg := []string{"", "  No tabs open.", "", "  Press " + keyHint(m.Cfg.Prefix) + " then " + keyHint(m.shownKeys("c")) + " (or enter) to start one,",
			"  or click " + m.onClick(buttonStyle.Render(" + new tab "), (*Model).openWizard) + "."}
		return fitLines(msg, w, h)
	}
	a := p.A
	if p.Kind != core.PaneShell && (a == nil || a.Pane == nil) {
		msg := []string{""}
		switch {
		case a == nil:
		case a.Err != "":
			msg = []string{"", "  " + warn(a.Err), "", "  " + keyHint("r") + " / enter: try again   " +
				keyHint(m.Cfg.Prefix) + " " + keyHint(m.shownKeys("x")) + ": close the tab"}
		default:
			msg = []string{"", "  Starting " + string(a.Agent) + " in " + a.Dir + " …"}
		}
		return fitLines(msg, w, h)
	}
	tp := p.Term()
	if tp == nil {
		return fitLines(nil, w, h)
	}
	keys := t == m.Cur() && p == t.Focused()
	scrolling := keys && m.scrolling
	scroll := 0
	if scrolling {
		scroll = t.Scroll
	}
	lines := fitLines(tp.Render(scroll, keys && m.focus == core.FocusAgent && !m.scrolling && m.dialog == nil), w, h)
	if p.Kind != core.PaneShell && a.Light == core.StExited {
		note := " " + string(a.Agent) + " has exited"
		if a.Err != "" {
			note += " (" + a.Err + ")"
		}
		note += " — enter / r / click: start it again, continuing the conversation"
		if p == t.OwnPane() {
			note += "   " + m.pk("x") + ": close tab "
		} else {
			note += "   " + m.pk("t x") + ": close the pane "
		}
		lines[h-1] = m.onClick(lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("231")).
			Render(padTo(ansi.Truncate(note, w, "…"), w)), func(m *Model) tea.Cmd { return m.restart(a) })
	}
	if scrolling {
		n := tp.ScrollbackLen()
		note := " scrollback " + strconv.Itoa(t.Scroll) + "/" + strconv.Itoa(n) + " · ↑↓ pgup pgdn g G wheel · q or click here: back "
		if tp.AltScreen() {
			// It keeps no scrollback for nerdr: the keys scroll it.
			note = " scrolling " + m.paneName(t, p) + "'s own screen · ↑↓ pgup pgdn wheel · q or click here: back "
		}
		lines[0] = m.onClick(lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("231")).
			Render(padTo(ansi.Truncate(note, w, "…"), w)), func(m *Model) tea.Cmd {
			m.scrollKey(keyMsg("q"))
			return nil
		})
	}
	return lines
}

// paneAt is the pane of t's at column x and row y of its area, and
// whether (x, y) is on its program rather than its frame or the rows
// above it, with (x, y) then in the program.
func (m *Model) paneAt(t *core.Tab, x, y int) (p *core.PaneTab, px, py int, inside bool) {
	ax, aw, _ := m.tabArea(t)
	if y < m.rowH(t) {
		return nil, 0, 0, false
	}
	for _, c := range m.PaneCols(t, aw) {
		if x >= c.X && x < c.X+c.W {
			p = c.P
		}
	}
	if p == nil {
		return nil, 0, 0, false
	}
	rx, ry, rw, rh := m.paneRect(t, p)
	px, py = x+ax-rx, y+m.barRows()-ry
	return p, px, py, px >= 0 && py >= 0 && px < rw && py < rh
}

// cycleFocus moves the keyboard on: to the next pane of the active tab
// shown, then to the split's other tab or the review panel, and round.
func (m *Model) cycleFocus() tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	_, w, _ := m.tabArea(t)
	cols := m.PaneCols(t, w)
	if m.focus == core.FocusReview {
		return m.focusPane(t, cols[0].P)
	}
	i := slices.IndexFunc(cols, func(c core.PaneCol) bool { return c.P == t.Focused() })
	switch {
	case i+1 < len(cols):
		return m.focusPane(t, cols[i+1].P)
	case m.splitTab() != nil && m.splitShown():
		return m.selectTab(m.TabIndex(m.splitTab()))
	case !m.reviewOpen && len(cols) > 1:
		return m.focusPane(t, cols[0].P)
	}
	m.setFocus(core.FocusReview)
	return m.resize()
}

// closeLine is what closing the pane of a, at work, costs.
func closeLine(a *core.Tab) string {
	if a.Light == core.StBackground {
		return string(a.Agent) + " has a process running in the background. Closing its pane stops it."
	}
	return string(a.Agent) + " is working. Closing its pane stops it mid-turn."
}
