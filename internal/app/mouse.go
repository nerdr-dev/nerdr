package app

import (
	"encoding/base64"
	"os"
	"slices"
	"time"
	"unicode/utf8"

	"nerdr/internal/review"
	"nerdr/internal/term"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The mouse, as herdr has it: click a group or a tab to go to it, right-
// click one for what can be done to it, ≡ for everything else, + for a new
// tab. Every dialog's keys are buttons, its lists can be clicked, and a
// click outside a menu closes it. The wheel scrolls whatever it is over;
// the rule between the agent and the review panel can be dragged.
//
// An agent that asks for the mouse (opencode does) gets what lands on it,
// in its own coordinates. For one that does not, the wheel scrolls back
// through its output and dragging selects text, which is copied when the
// button is let go — the terminal's own selection is still there with
// shift held.

// dragKind is what a held button is dragging.
type dragKind int

const (
	dragNone   dragKind = iota
	dragRule            // the rule between agent and review panel
	dragAgent           // a press passed to the pane with the keyboard; so is the rest
	dragSelect          // selecting text in the output of the pane with the keyboard
	dragReview          // a press in the review panel: a scrollbar may be dragged
	dragSide            // the sidebar's edge, setting its width
	dragPanes           // the divider between two panes side by side
)

// mouseMode is which mouse reports the terminal is asked for.
type mouseMode int

const (
	mouseOff mouseMode = iota
	mouseCells
	mouseAll
)

// wantMouse is the reporting nerdr needs now: what the editor asks for
// while it is up, else every movement, for the tooltips and for what
// needAllMotion says wants it.
func (m *Model) wantMouse() mouseMode {
	if !m.mouseOn() {
		return mouseOff
	}
	if m.editor != nil {
		if m.editor.WantsAllMotion() {
			return mouseAll
		}
		return mouseCells
	}
	return mouseAll
}

// needAllMotion reports whether anything but the tooltips wants to hear
// of the pointer moving with no button held: a menu (to follow the
// pointer), or an agent that asked for it.
func (m *Model) needAllMotion() bool {
	if _, ok := m.dialog.(*menu); ok {
		return true
	}
	p := m.keyPane()
	return p != nil && p.WantsAllMotion()
}

func (m *Model) mouseOn() bool { return m.cfg.Mouse != nil && *m.cfg.Mouse }

// syncMouse asks the terminal for the reports wantMouse says, if that has
// changed.
func (m *Model) syncMouse() tea.Cmd {
	want := m.wantMouse()
	if want == m.mouse {
		return nil
	}
	m.mouse = want
	switch want {
	case mouseAll:
		return tea.Sequence(tea.DisableMouse, tea.EnableMouseAllMotion)
	case mouseCells:
		return tea.Sequence(tea.DisableMouse, tea.EnableMouseCellMotion)
	}
	return tea.DisableMouse
}

func (m *Model) handleMouse(ev tea.MouseMsg) tea.Cmd {
	if m.editor != nil {
		m.editor.SendMouse(paneMouse(ev, 0, 0))
		return nil
	}
	if m.drag != dragNone {
		return m.dragMouse(ev)
	}
	if isWheel(ev) && !m.wheelHeard() {
		return nil
	}
	before := m.hover
	tip := m.hoverMouse(ev)
	if plainMotion(ev) && !m.needAllMotion() {
		// A tip that is up follows the pointer, so only with none up does
		// the screen stay as it was.
		h := m.hover
		m.sameFrame = !before.shown && !h.shown && h.tip == before.tip && h.seq == before.seq
		return tip
	}
	return tea.Batch(tip, m.routeMouse(ev))
}

// routeMouse passes a mouse event on to what is under it.
func (m *Model) routeMouse(ev tea.MouseMsg) tea.Cmd {
	press := ev.Action == tea.MouseActionPress && !isWheel(ev)
	if press && m.prefix {
		// A click puts the prefix hint away, unless it is on one of its
		// commands.
		if id := m.zoneAt(ev.X, ev.Y); id < 0 || id < m.hintZones {
			m.prefix, m.nav = false, false
		}
	}

	if m.dialog != nil {
		return m.dialogMouse(ev)
	}
	// The wheel over the bars steps through the groups and the tabs,
	// wherever on them it is.
	if isWheel(ev) && ev.Y < m.tabRow() {
		if !m.wheelStep() {
			return nil
		}
		return m.stepGroup(wheelDir(ev))
	}
	if isWheel(ev) && ev.Y < m.barRows() {
		if !m.wheelStep() {
			return nil
		}
		return m.stepTab(wheelDir(ev))
	}
	if isWheel(ev) && m.panelSkills() && ev.X >= m.sideW() {
		// Anywhere on F4, the list's rows included, the wheel scrolls the
		// skill's text, as pgup and pgdown do.
		if area, _ := m.areaAt(ev.X); area == areaReview {
			m.skills.scroll = max(m.skills.scroll+3*wheelDir(ev), 0)
			return nil
		}
	}
	if press && ev.Y >= m.barRows() && m.onSideEdge(ev.X) {
		switch ev.Button {
		case tea.MouseButtonLeft:
			m.drag = dragSide
		case tea.MouseButtonRight:
			// Back to as wide as the names need.
			m.sideWidth = 0
			m.saveSession()
			return m.resize()
		}
		return nil
	}
	if id := m.zoneAt(ev.X, ev.Y); id >= 0 {
		return m.zoneFns[id](m, ev)
	}
	if ev.Y < m.barRows() {
		return nil
	}
	if ev.X < m.sideW() {
		return m.sideWheel(ev)
	}

	area, x := m.areaAt(ev.X)
	y := ev.Y - m.barRows()
	switch area {
	case areaRule:
		if press && ev.Button == tea.MouseButtonLeft {
			m.drag = dragRule
		}
		return nil
	case areaSplit:
		_, r := m.sides()
		return m.sideMouse(r, ev, x, y)
	case areaRail:
		return nil
	case areaReview:
		ev.X, ev.Y = x, y-m.panelTop()
		var cmd tea.Cmd
		if press && ev.Button == tea.MouseButtonLeft {
			// Its drag goes on reaching the panel off its edge, and it
			// hears the button let go wherever that is.
			m.drag = dragReview
		}
		if press && m.focus != focusReview {
			m.setFocus(focusReview)
			cmd = m.resize()
		}
		rm, rcmd := m.review.Update(ev)
		m.review = rm.(review.Model)
		return tea.Batch(cmd, rcmd, m.syncPRPanel())
	}
	l, _ := m.sides()
	return m.sideMouse(l, ev, x, y)
}

// sideMouse handles the mouse over tab t, at (x, y) in its area. Over a
// pane without the keyboard — of the tab of the split without it, or of
// this one — a click or the wheel gives it the keyboard first, and the
// pointer merely passing over it is ignored.
//
// On a pane's frame, or the panel above its agent, a click only gives the
// pane the keyboard.
func (m *Model) sideMouse(t *tab, ev tea.MouseMsg, x, y int) tea.Cmd {
	if t == nil {
		return nil
	}
	if i := m.dividerAt(t, x, y); i >= 0 && ev.Action == tea.MouseActionPress {
		switch ev.Button {
		case tea.MouseButtonLeft:
			m.drag, m.dragTab, m.dragDiv = dragPanes, t, i
		case tea.MouseButtonRight:
			// Back to sharing the room evenly.
			for _, p := range t.panes() {
				p.weight = 0
			}
			m.resizePanes()
		}
		return nil
	}
	p, px, py, inside := m.paneAt(t, x, y)
	if p == nil {
		return nil
	}
	var cmd tea.Cmd
	if t != m.cur() || p != t.focused() || m.focus != focusAgent {
		if ev.Action != tea.MouseActionPress {
			return nil
		}
		if t != m.cur() {
			cmd = m.selectTab(m.tabIndex(t))
		}
		cmd = tea.Batch(cmd, m.focusPane(t, p))
		if !isWheel(ev) || !inside {
			return cmd
		}
	}
	if !inside || p.term() == nil {
		return cmd
	}
	return tea.Batch(cmd, m.agentMouse(ev, px, py))
}

// dialogMouse handles the mouse while a dialog is open: its zones work, a
// wheel moves its cursor, and a click outside closes it when that loses
// nothing.
func (m *Model) dialogMouse(ev tea.MouseMsg) tea.Cmd {
	press := ev.Action == tea.MouseActionPress && !isWheel(ev)
	switch d := m.dialog.(type) {
	case *summaryDialog:
		if press {
			m.dialog = nil // any key closes it, and so does any click
			return nil
		}
	case *closeTab:
		if d.hidden() {
			// Not drawn yet, so it has no zones: a click is one outside.
			if press {
				m.dialog = nil
			}
			return nil
		}
	}
	id := m.zoneAt(ev.X, ev.Y)
	switch {
	case ev.Button == tea.MouseButtonWheelUp:
		return m.dialog.key(m, keyMsg("up"))
	case ev.Button == tea.MouseButtonWheelDown:
		return m.dialog.key(m, keyMsg("down"))
	case id >= m.dialogZones:
		return m.zoneFns[id](m, ev)
	case press:
		switch m.dialog.(type) {
		case *wizard, *messageDialog, *renamer, *noteDialog, *commitDialog:
			// Holds something typed; esc it is.
		default:
			m.dialog = nil
		}
	}
	return nil
}

// dragMouse carries on what a press started, until the button is let go.
func (m *Model) dragMouse(ev tea.MouseMsg) tea.Cmd {
	release := ev.Action == tea.MouseActionRelease
	switch m.drag {
	case dragRule:
		if release {
			m.drag = dragNone
			return nil
		}
		bw := m.mainW()
		pct := ((m.sideW()+bw-ev.X-1)*100 + bw/2) / max(bw, 1)
		pct = min(max(pct, 20), 80)
		width := &m.reviewWidth
		if m.splitShown() {
			width = &m.splitWidth
		}
		if pct == *width {
			return nil
		}
		*width = pct
		return m.resize()

	case dragSide:
		if release {
			m.drag = dragNone
			m.saveSession()
			return nil
		}
		return m.dragSideTo(ev.X)

	case dragPanes:
		if release {
			m.drag, m.dragTab = dragNone, nil
			return nil
		}
		ax, _, _ := m.tabArea(m.dragTab)
		return m.dragDivider(m.dragTab, m.dragDiv, ev.X-ax)

	case dragAgent:
		if release {
			m.drag = dragNone
		}
		if t := m.cur(); t != nil && t.screen() != nil {
			rx, ry, rw, rh := m.paneRect(t, t.focused())
			x := min(max(ev.X-rx, 0), rw-1)
			y := min(max(ev.Y-ry, 0), rh-1)
			t.screen().SendMouse(paneMouse(ev, x, y))
		}
		return nil

	case dragReview:
		if release {
			m.drag = dragNone
		}
		_, x := m.areaAt(ev.X)
		if a, _ := m.areaAt(ev.X); a != areaReview {
			x = 0
		}
		ev.X, ev.Y = x, ev.Y-m.barRows()-m.panelTop()
		rm, cmd := m.review.Update(ev)
		m.review = rm.(review.Model)
		return cmd

	case dragSelect:
		t := m.cur()
		if t == nil || t.screen() == nil || t.screen() != m.selPane {
			m.drag = dragNone
			return nil
		}
		p := t.screen()
		rx, ry, aw, ah := m.paneRect(t, t.focused())
		x := min(max(ev.X-rx, 0), aw-1)
		y := ev.Y - ry
		// Dragging past the top or bottom scrolls on.
		if y < 0 && m.scrolling {
			t.scroll = min(t.scroll+1, p.ScrollbackLen())
		} else if y >= ah && m.scrolling {
			t.scroll = max(t.scroll-1, 0)
		}
		y = min(max(y, 0), ah-1)
		m.sel.To = p.PointAt(x, y, m.shownScroll(t))
		p.Select(&m.sel)
		if release {
			m.drag = dragNone
			m.copySelection(p)
		}
		return nil
	}
	m.drag = dragNone
	return nil
}

// agentMouse handles the mouse over the agent (or its shell), at (x, y)
// in the pane.
func (m *Model) agentMouse(ev tea.MouseMsg, x, y int) tea.Cmd {
	t := m.cur()
	press := ev.Action == tea.MouseActionPress && !isWheel(ev)
	var cmd tea.Cmd
	if press && m.focus != focusAgent {
		m.setFocus(focusAgent)
		cmd = m.resize()
	}
	if t == nil || t.screen() == nil {
		return cmd
	}
	p := t.screen()
	if press {
		p.Select(nil)
	}
	// With shift held, a program that has the mouse still lets nerdr
	// select: that is how terminals do it.
	if !m.scrolling && !ev.Shift && p.SendMouse(paneMouse(ev, x, y)) {
		if press {
			m.drag = dragAgent
		}
		return cmd
	}

	switch {
	case ev.Button == tea.MouseButtonWheelUp:
		m.wheelScroll(t, 3)
	case ev.Button == tea.MouseButtonWheelDown:
		m.wheelScroll(t, -3)
	case press && ev.Button == tea.MouseButtonLeft:
		pt := p.PointAt(x, y, m.shownScroll(t))
		m.sel = term.Selection{From: pt, To: pt}
		m.selPane = p
		m.drag = dragSelect
	case press && ev.Button == tea.MouseButtonRight:
		m.openMenu(ev.X, ev.Y)
	}
	return cmd
}

// wheelScroll scrolls the tab's output by n lines (up when positive). A
// program on the alternate screen has no scrollback: it gets the arrow
// keys, as terminals do for the wheel there.
func (m *Model) wheelScroll(t *tab, n int) {
	p := t.screen()
	sb := p.ScrollbackLen()
	if sb == 0 {
		if p.AltScreen() {
			k := "\x1b[A"
			if n < 0 {
				k, n = "\x1b[B", -n
			}
			for range n {
				p.Input([]byte(k))
			}
		}
		return
	}
	t.scroll = min(max(t.scroll+n, 0), sb)
	m.scrolling = t.scroll > 0
}

// shownScroll is how far back the tab is shown.
func (m *Model) shownScroll(t *tab) int {
	if m.scrolling && t == m.cur() {
		return t.scroll
	}
	return 0
}

// copySelection puts the selected text on the clipboard: through the
// terminal (OSC 52, which reaches the clipboard of the machine in front of
// you over SSH and through herdr), and the local clipboard's own way too
// where there is one.
func (m *Model) copySelection(p *term.Pane) {
	text := p.SelectedText()
	p.Select(nil)
	m.copyText(text)
}

// copyText puts text on the clipboard, as copySelection does.
func (m *Model) copyText(text string) {
	if text == "" {
		return
	}
	n := plural(utf8.RuneCountInString(text), "character")
	if toClipboard(text) {
		m.toast("copied " + n)
		return
	}
	// Only the terminal can put it there, and many (GNOME Terminal,
	// Terminal.app) leave OSC 52 alone, so do not claim it got there.
	m.toast("sent " + n + " to the terminal's clipboard; if it is not there, install xclip, xsel or wl-clipboard")
}

// copyWindow is how soon after the user last typed, clicked or moved the
// mouse a program's OSC 52 is put on the clipboard.
const copyWindow = 5 * time.Second

// checkCopied puts on the clipboard what a program has copied with OSC 52,
// as one that takes the mouse does with what is chosen in it. Only from
// the program the user is in, just after they did something there: a copy
// is theirs to make. Any program can print OSC 52 — an agent, one in a
// sandbox among them, or a file it shows — and what is put on the
// clipboard unasked is what the user pastes next, into a shell perhaps.
// The rest is let go.
func (m *Model) checkCopied() {
	var from []*term.Pane
	if m.editor != nil {
		from = append(from, m.editor)
	} else {
		for _, t := range []*tab{m.cur(), m.splitTab()} {
			if t != nil {
				from = append(from, t.screen())
			}
		}
	}
	recent := m.router != nil && time.Since(m.router.LastInput()) < copyWindow
	panes := []*term.Pane{m.editor}
	for _, t := range m.tabs {
		for _, p := range t.panes() {
			panes = append(panes, p.term())
		}
	}
	for _, p := range panes {
		if p == nil {
			continue
		}
		text := p.TakeCopied()
		switch {
		case text == "":
		case recent && slices.Contains(from, p):
			m.copyText(text)
		default:
			debugf("osc 52: %d bytes from a program not in use, not put on the clipboard", len(text))
		}
	}
}

// toClipboard is how copyText reaches the clipboard, and reports whether
// it reached the local clipboard as well as the terminal's; tests put their
// own. Without xclip, xsel or wl-copy on Linux there is no local one.
var toClipboard = func(text string) bool {
	_, _ = os.Stdout.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
	if os.Getenv("SSH_CONNECTION") != "" || clipboard.Unsupported {
		return false
	}
	go func() { _ = clipboard.WriteAll(text) }()
	return true
}

// screenArea is a part of the body.
type screenArea int

const (
	areaAgent screenArea = iota // the agent, or the left of the split
	areaRule
	areaReview
	areaSplit // the right of the split
	areaRail  // the review panel's column of screens, and the column before it
)

// areaAt is the part of the body at column x of the screen, and x within
// it. Left of the body, over the sidebar, it is the agent's, at its edge.
func (m *Model) areaAt(x int) (screenArea, int) {
	x = max(x-m.sideW(), 0)
	if mw := m.mainW(); x >= mw && m.railShown() {
		return areaRail, x - mw
	}
	switch {
	case m.reviewZoom:
		return areaReview, x
	case m.splitShown():
		lw, _ := m.splitWidths()
		switch {
		case x < lw:
			return areaAgent, x
		case x == lw:
			return areaRule, 0
		}
		return areaSplit, x - lw - 1
	case !m.reviewOpen:
		return areaAgent, x
	}
	aw := m.agentWidth()
	switch {
	case x < aw:
		return areaAgent, x
	case x == aw:
		return areaRule, 0
	}
	return areaReview, x - aw - 1
}

// agentWidth is the columns of the active tab's area, its frame included.
func (m *Model) agentWidth() int {
	w, _ := m.agentArea()
	return w
}

// wheelGap is the least time between two tabs or groups the wheel steps
// to. A wheel that spins on, or a trackpad still gliding — which macOS
// may send on to a window the pointer is over, nerdr's in the background
// too — reports far more often, and each step is a whole tab switch: they
// would pile up and go on long after the hand has stopped.
const wheelGap = 80 * time.Millisecond

// wheelStep reports whether the wheel may step to another tab or group
// now, and if so counts the step.
func (m *Model) wheelStep() bool {
	now := time.Now()
	if now.Sub(m.lastWheelStep) < wheelGap {
		return false
	}
	m.lastWheelStep = now
	return true
}

// wheelQuiet is how long the wheel must be still, after nerdr gets the
// focus back, before it is heard again.
const wheelQuiet = 250 * time.Millisecond

// wheelHeard reports whether a turn of the wheel counts. Not while nerdr
// is in the background — macOS sends the wheel on to the window under the
// pointer whichever has the focus — and, once it has the focus again, not
// until the wheel has been still a moment: what is still coming then is a
// glide begun elsewhere, not a hand on nerdr.
func (m *Model) wheelHeard() bool {
	now := time.Now()
	last := m.lastWheel
	m.lastWheel = now
	if m.blurred {
		return false
	}
	if m.wheelHeld {
		if now.Sub(last) < wheelQuiet {
			return false
		}
		m.wheelHeld = false
	}
	return true
}

func isWheel(ev tea.MouseMsg) bool { return tea.MouseEvent(ev).IsWheel() }

func wheelDir(ev tea.MouseMsg) int {
	if ev.Button == tea.MouseButtonWheelUp || ev.Button == tea.MouseButtonWheelLeft {
		return -1
	}
	return 1
}

// paneMouse is ev for a pane, at (x, y) in it.
func paneMouse(ev tea.MouseMsg, x, y int) term.MouseEvent {
	out := term.MouseEvent{
		Button: ansi.MouseButton(ev.Button),
		X:      x, Y: y,
		Shift: ev.Shift, Alt: ev.Alt, Ctrl: ev.Ctrl,
	}
	switch ev.Action {
	case tea.MouseActionRelease:
		out.Kind = term.MouseRelease
	case tea.MouseActionMotion:
		out.Kind = term.MouseMotion
	}
	return out
}

// The helpers dialogs draw clickable things with.

// onClick is a zone that runs fn when clicked with the left button.
func (m *Model) onClick(s string, fn func(m *Model) tea.Cmd) string {
	return m.zone(s, func(m *Model, ev tea.MouseMsg) tea.Cmd {
		if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
			return fn(m)
		}
		return nil
	})
}

// press is a click that does what key k does in the open dialog.
func press(k string) func(m *Model) tea.Cmd {
	return func(m *Model) tea.Cmd {
		if m.dialog == nil {
			return nil
		}
		return m.dialog.key(m, keyMsg(k))
	}
}

// keys draws a dialog's row of keys, "enter continue · esc cancel", each
// a button for its key. The pairs are key, what it does; a key of "" is a
// note that is only text (alt+enter is no use as a button).
func (m *Model) keys(pairs ...string) string {
	var out string
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += dim(" · ")
		}
		k, what := pairs[i], pairs[i+1]
		if k == "" {
			out += dim(what)
			continue
		}
		label := k
		if k == " " {
			label = "space"
		}
		out += m.onClick(keyHint(label)+" "+dim(what), press(k))
	}
	return out
}

// button is key k's hint followed by what it does, a button for it.
func (m *Model) button(k, what string) string {
	return m.onClick(keyHint(k)+" "+what, press(k))
}
