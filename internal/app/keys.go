package app

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// handleKey routes a key that reached bubbletea: a prefix command, a key
// for an open dialog or the review panel, or — when it slipped past the
// input router while focus was changing — a key meant for the agent.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.prefix && (msg.String() == "pgdown" || msg.String() == "pgup") {
		if msg.String() == "pgdown" {
			m.prefixPage++
		} else {
			m.prefixPage = max(0, m.prefixPage-1)
		}
		return nil
	}
	if m.prefix {
		m.prefix = false
		g := m.keyGroup
		m.keyGroup = ""
		if g == "" {
			msg = m.swapPressed(msg)
		}
		k := msg.String()
		if panelKey(k) {
			m.nav = false
			return m.handleKey(msg)
		}
		if g != "" {
			return m.groupKey(g, msg)
		}
		if m.nav {
			m.nav = false
			switch {
			case k == "enter" || k == "esc" || m.isPrefix(msg):
				return nil // staying in the tab gone to
			case !navKey(k) && !prefixCommand(k):
				// Typed to the agent: the walk is over.
				return m.handleKey(msg)
			}
			// A command for the tab walked to — x to close it, say — runs
			// at once, without enter and the prefix again first.
		}
		open := m.dialog != nil
		cmd := m.command(msg)
		if navKey(k) && !open && m.dialog == nil {
			// Walking on: the prefix stays on for the next step.
			m.prefix, m.nav = true, true
		}
		return cmd
	}
	if m.isPrefix(msg) {
		m.startPrefix()
		return nil
	}
	if m.dialog != nil {
		return m.dialog.key(m, msg)
	}
	if msg.String() == "f5" && m.togglePluginUI() {
		return nil
	}
	if panelKey(msg.String()) {
		// The review panel's menu, whichever side has the keyboard — and
		// out of the scrollback, which would take them for its own. The
		// panel gets the keyboard: the screen asked for is there to be used.
		if m.scrolling {
			m.scrollKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
		if !m.reviewOpen {
			m.reviewOpen = true
		}
		cmd := m.reviewKey(msg)
		m.setFocus(focusReview)
		return tea.Batch(cmd, m.resize())
	}
	if m.scrolling {
		m.scrollKey(msg)
		return nil
	}
	if m.focus == focusReview && msg.String() == "?" &&
		(m.panelMessages() || m.panelSkills() || m.panelPlugins() || m.panelAllPRs() || m.panelIssues()) {
		// ? is help, as in the rest of the panel.
		return m.openPalette()
	}
	if m.focus == focusReview && m.panelMessages() {
		return m.messagesKey(msg)
	}
	if m.focus == focusReview && m.panelSkills() {
		return m.skillsKey(msg)
	}
	if m.focus == focusReview && m.panelPlugins() {
		return m.pluginsKey(msg)
	}
	if m.focus == focusReview && m.panelAllPRs() {
		return tea.Batch(m.allPRsKey(msg), m.syncPRPanel())
	}
	if m.focus == focusReview && m.panelIssues() {
		return tea.Batch(m.issuesKey(msg), m.syncPRPanel())
	}
	if m.focus == focusReview && m.panelBranchPR() {
		cmd, done := m.branchKey(msg.String())
		if done {
			return cmd
		}
		return tea.Batch(cmd, m.reviewKey(msg))
	}
	if m.focus == focusReview {
		return m.reviewKey(msg)
	}
	t := m.cur()
	if t == nil {
		if msg.String() == "enter" || msg.String() == "c" {
			return m.openWizard()
		}
		return nil
	}
	f := t.focused()
	if f.kind == paneReview && f.a == nil {
		return m.reviewPickKey(t, f, msg)
	}
	if a := f.a; f.kind != paneShell && a != nil && (a.status == stExited || a.pane == nil) {
		if msg.String() == "enter" || msg.String() == "r" {
			return m.restart(a)
		}
		return nil
	}
	if p := f.term(); p != nil {
		// Keys reach here while the router is switching over.
		if b := keyBytes(msg); len(b) > 0 {
			p.Input(b)
		}
	}
	return nil
}

// reviewKey hands a key to the review panel.
func (m *Model) reviewKey(msg tea.KeyMsg) tea.Cmd {
	rm, cmd := m.review.Update(msg)
	m.review = rm.(review.Model)
	return tea.Batch(cmd, m.syncPRPanel())
}

// panelKey reports whether k is one of the review panel's screens, F1 to
// F5, which work whichever side has the keyboard.
func panelKey(k string) bool {
	switch k {
	case "f1", "f2", "f3", "f4", "f5":
		return true
	}
	return false
}

// functionKeys is whether hints name the panel's screens F1 to F5.
func (m *Model) functionKeys() bool {
	return m.cfg.FunctionKeys == nil || *m.cfg.FunctionKeys
}

// screenKey is how a hint names the review panel's screen n (1 to 4):
// F1 to F5, or the prefix keys for it with function_keys off.
func (m *Model) screenKey(n int) string {
	if m.functionKeys() {
		return "F" + strconv.Itoa(n)
	}
	return m.pk("e " + strconv.Itoa(n))
}

// screenMenuKey is screenKey for a menu, whose keys come after the
// prefix: "F4", or "e 4".
func (m *Model) screenMenuKey(n int) string {
	if m.functionKeys() {
		return "F" + strconv.Itoa(n)
	}
	return "e " + strconv.Itoa(n)
}

// screenMenuKeys are the keys the review panel's menu shows before each
// screen's name, and the hint for all of them: none with function_keys
// on, for the panel's own F1 to F5.
func (m *Model) screenMenuKeys() ([]string, string) {
	if m.functionKeys() {
		return nil, ""
	}
	var keys []string
	for n := 1; n <= len(review.ScreenNames()); n++ {
		keys = append(keys, strings.ReplaceAll(m.shownKeys("e "+strconv.Itoa(n)), " ", ""))
	}
	return keys, m.pk("e") + " 1-5"
}

// keyPane is the program that has the keyboard: the one in the active
// tab's pane with it; nil when the review panel has it, or none does.
func (m *Model) keyPane() *term.Pane {
	t := m.cur()
	if t == nil || m.focus == focusReview {
		return nil
	}
	return t.screen()
}

// startPrefix turns the prefix on for a new command. The arrows start on
// the group row when there are groups to choose between, else on the tabs.
func (m *Model) startPrefix() {
	m.prefixPage = 0
	m.keyGroup, m.groupWalk = "", false
	m.prefix, m.nav = true, false
	// The arrows start on the tab row, among the repository's own tabs —
	// unless it has only the one, when ← → can only mean the next
	// repository.
	m.onGroups = len(m.groups()) > 1 && len(m.groupTabs(m.curGroup())) < 2
}

// navKey is whether k, after the prefix, goes to another tab or moves this
// one: then the prefix stays on for the next, so walking five tabs along is
// the prefix and five arrows, until enter. A command runs for the tab walked to; any
// other key goes to the agent.
func navKey(k string) bool {
	switch k {
	case "right", "left", "down", "up", "[", "]", "shift+left", "shift+right",
		"1", "2", "3", "4", "5", "6", "7", "8", "9", "0":
		return true
	}
	return false
}

// prefixCommand is whether k is a key command runs something for. Keep it
// in step with run's switch and keyGroups.
func prefixCommand(k string) bool {
	switch k {
	case "c", "C", "x", "tab", "o", "<", ",", ">", ".",
		"b", "pgup", "s", "q", "?", ":", "t",
		"m", "M", "l", "w", "n", "f", " ", "r", "h", "p", "g", "u", "/":
		return true
	}
	return isGroup(k)
}

// groupKey runs the key pressed in group g. esc goes back to the prefix's
// own keys; a key the group does not have ends it, rather than going to
// the agent: it was meant as a command.
func (m *Model) groupKey(g string, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	walking := m.groupWalk
	m.groupWalk = false
	switch {
	case k == "esc":
		m.prefix, m.prefixPage = true, 0
		return nil
	case m.isPrefix(msg) && walking:
		// A new command after the steps.
		m.startPrefix()
		return nil
	case m.isPrefix(msg):
		return nil
	}
	if g == "t" {
		// The arrows step along the panes, and with shift move the one
		// with the keyboard; the group stays on for more, until enter or
		// another key.
		arrow := map[string]string{"left": "←", "right": "→", "shift+left": "⇧←", "shift+right": "⇧→"}
		if a, ok := arrow[k]; ok {
			cmd := m.run("t "+a, msg)
			m.prefix, m.keyGroup, m.groupWalk = true, g, true
			return cmd
		}
		if k == "enter" {
			return nil
		}
	}
	run, ok := groupCommand(g, k)
	if !ok && walking {
		// Typed to the pane walked to: the walk is over.
		return m.handleKey(msg)
	}
	if !ok {
		m.toast(m.pk(g) + " " + k + ": no such command · " + m.pk("?") + " lists them all")
		return nil
	}
	return m.run(run, msg)
}

// runCommand runs a command by its keys after the prefix, as
// commandGroups and keyGroups have them: for the menus and the palette.
func (m *Model) runCommand(run string) tea.Cmd {
	if panelKey(run) {
		return m.handleKey(keyMsg(run))
	}
	if _, k, ok := strings.Cut(run, " "); ok {
		return m.run(run, keyMsg(k))
	}
	return m.command(keyMsg(run))
}

// swappedKey is the letter of a prefix that is also a command's key
// after it: ctrl+b, say, while b scrolls back. That command moves to q,
// the key quit leaves, and quit to the prefix's letter, so the prefix's
// letter twice quits, as ctrl+q q does. "" when the prefix's letter is no
// command's.
func (m *Model) swappedKey() string {
	l, ok := strings.CutPrefix(m.cfg.PrefixKey(), "ctrl+")
	if !ok || len(l) != 1 || l < "a" || l > "z" || l == "q" {
		return ""
	}
	if !prefixCommand(l) && !navKey(l) {
		return ""
	}
	return l
}

// swapKey turns the first key after the prefix between the key pressed
// and the command's own key; it is its own inverse, so it serves both.
func (m *Model) swapKey(k string) string {
	switch s := m.swappedKey(); {
	case s == "":
	case k == s:
		return "q"
	case k == "q":
		return s
	}
	return k
}

// swapPressed is the key pressed after the prefix as the command's own.
func (m *Model) swapPressed(msg tea.KeyMsg) tea.KeyMsg {
	if k := msg.String(); m.swapKey(k) != k {
		return keyMsg(m.swapKey(k))
	}
	return msg
}

// shownKeys are a command's keys after the prefix ("a t", "x") as they
// are pressed: the first one swapped as swappedKey says.
func (m *Model) shownKeys(keys string) string {
	first, rest, ok := strings.Cut(keys, " ")
	if ok {
		return m.swapKey(first) + " " + rest
	}
	return m.swapKey(first)
}

// pk is the prefix and the keys after it, as they are pressed, for hints.
func (m *Model) pk(keys string) string {
	return m.cfg.Prefix + " " + m.shownKeys(keys)
}

func (m *Model) isPrefix(msg tea.KeyMsg) bool {
	return strings.EqualFold(msg.String(), m.cfg.PrefixKey())
}

// command runs the key pressed after the prefix.
func (m *Model) command(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if m.isPrefix(msg) {
		// The prefix twice cancels, like esc. It does not pass the key on,
		// as tmux does: pressing it again to put the hint away is natural,
		// and an agent may bind the key (ctrl+g, say, opens claude's external
		// editor and waits for it — the agent looks hung). s sends it on purpose.
		return nil
	}
	if m.dialog != nil && k == "esc" {
		m.dialog = nil
		return nil
	}
	if isGroup(k) {
		// The group's own keys next, in the hint line.
		m.prefix, m.keyGroup, m.prefixPage = true, k, 0
		return nil
	}
	return m.run(k, msg)
}

// run runs the command whose keys after the prefix are id ("x", "i p"),
// pressed as msg, the last of them.
func (m *Model) run(id string, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	// A command takes the place of the dialog open: the last one asked
	// for wins, so the prefix and f over the inbox opens the switcher.
	m.dialog = nil
	switch id {
	case "c":
		return m.openWizard()
	case "C":
		return m.stackOnTab()
	case "x":
		return m.openClose()
	case "right", "left":
		if m.sideW() > 0 {
			// The sidebar's lists are side by side in the keys: ← → go
			// from one to the other.
			m.onGroups = !m.onGroups
			return nil
		}
		delta := 1
		if k == "left" {
			delta = -1
		}
		if !m.onGroups && len(m.groupTabs(m.curGroup())) < 2 && len(m.groups()) > 1 {
			m.onGroups = true // a tab alone in its repository has no neighbour to go to
		}
		if m.onGroups {
			return m.stepGroup(delta)
		}
		return m.stepInGroup(delta)
	case "down", "up":
		if m.sideW() > 0 {
			// ↑ ↓ walk the sidebar's list the arrows are on.
			delta := 1
			if k == "up" {
				delta = -1
			}
			if m.onGroups {
				return m.stepGroup(delta)
			}
			return m.stepInGroup(delta)
		}
		m.onGroups = k == "up" && len(m.groups()) > 1
		return nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.selectInGroup(int(k[0] - '1'))
	case "0":
		return m.selectInGroup(-1)
	case "]":
		return m.stepGroup(1)
	case "[":
		return m.stepGroup(-1)
	case "tab":
		return m.cycleFocus()
	case "a a", "esc":
		m.setFocus(focusAgent)
		return m.resize()
	case "z k":
		return m.togglePanelSmall()
	case "z e":
		m.reviewOpen = !m.reviewOpen
		if !m.reviewOpen {
			m.reviewZoom = false
			m.setFocus(focusAgent)
		}
		return m.resize()
	case "e 1", "e 2", "e 3", "e 4", "e 5":
		// F1-F5 without the function keys: a Mac laptop's are the
		// brightness and volume unless fn is held, and a shell or an
		// editor keeps them for itself. The screen is the id's, not the key
		// pressed: e e runs e 1.
		return m.handleKey(keyMsg("f" + id[len("e "):]))
	case "o":
		return m.openWorktreeEditor()
	case "z z":
		m.reviewZoom, m.sideZoom = !m.reviewZoom, false
		if m.reviewZoom {
			m.reviewOpen = true
			m.setFocus(focusReview)
		}
		return m.resize()
	case "<", ",", ">", ".":
		width := &m.reviewWidth
		if m.splitShown() {
			width = &m.splitWidth
		}
		if k == "<" || k == "," {
			*width = min(*width+5, 80)
		} else {
			*width = max(*width-5, 20)
		}
		return m.resize()
	case "z v":
		return m.toggleSplit()
	case "z =", "z +", "z a":
		return m.setLayout(k)
	case "z l":
		return m.openLayouts()
	case "z s":
		return m.swapSides()
	case "b", "pgup":
		if t := m.cur(); t != nil && t.screen() != nil {
			m.scrolling = true
			t.scroll = 0
			if k == "pgup" {
				m.scrollKey(msg)
			}
		}
		return nil
	case "a r":
		t := m.curAgent()
		if t == nil {
			return nil
		}
		if t.status != stWorking {
			return m.restart(t)
		}
		// Restarting cuts the turn short: what the agent has not written to
		// its log yet is lost.
		m.dialog = &confirm{
			title: "Restart " + m.agentName(t) + "?",
			lines: []string{
				string(t.Agent) + " is working. Restarting stops it mid-turn;",
				"the conversation continues, but the turn is cut short.",
			},
			yes: func(m *Model) tea.Cmd {
				if !m.live(t) {
					return nil
				}
				return m.restart(t)
			},
		}
		return nil
	case "p":
		// Send the prefix key itself to the agent (or the shell).
		if p := m.keyPane(); p != nil {
			if b, err := m.cfg.PrefixByte(); err == nil {
				p.Write([]byte{b})
			}
		}
		return nil
	case "q":
		m.dialog = &confirm{
			title: "Quit nerdr?",
			lines: []string{
				"The agents are stopped. Their tabs and conversations are",
				"restored the next time you start nerdr in " + m.root + ".",
			},
			yes: func(m *Model) tea.Cmd { return m.quit() },
		}
		return nil
	case "t t":
		return m.newShell()
	case "t a":
		return m.openNewAgent()
	case "t r", "a c":
		return m.newReviewPane()
	case "t x":
		return m.askClosePane()
	case "t m":
		if t := m.cur(); t != nil {
			return m.minimisePane(t, t.focused())
		}
		return nil
	case "t ←":
		return m.stepPane(-1)
	case "t →":
		return m.stepPane(1)
	case "t ⇧←":
		return m.movePane(-1)
	case "t ⇧→":
		return m.movePane(1)
	case "a s":
		return m.openSwitchAgent()
	case "a x":
		return m.stopReview()
	case "m":
		return m.openMessage(false)
	case "M":
		return m.openMessage(true)
	case "l":
		return m.openPromptPicker()
	case "i i":
		return m.showBranchPR()
	case "i p":
		return m.showAllPRs()
	case "i b":
		m.openTabPRs()
		return nil
	case "i o":
		return m.showIssues()
	case "f", " ":
		return m.openSwitcher()
	case "r":
		return m.openRename()
	case "shift+left":
		return m.moveTab(-1)
	case "shift+right":
		return m.moveTab(1)
	case "g":
		m.dialog = m.newCallsDialog()
		return nil
	case "u":
		return m.openUsage()
	case "/":
		return m.openSearch()
	case "z d":
		m.showDetails = !m.showDetails
		return nil
	case "z t":
		m.sideBar = !m.sideBar
		if m.sideBar && m.sideW() == 0 {
			m.toast("The window is too narrow for the tabs on the left")
		}
		return m.resize()
	case "z m":
		return m.toggleSideSmall()
	case "z b":
		if m.sideW() > 0 {
			m.toast("The repositories stay in the sidebar; " + m.pk("z m") + " makes it small")
			return nil
		}
		m.hideGroups = !m.hideGroups
		if len(m.groups()) < 2 {
			m.toast("Only one repository: its row shows once there are two")
		}
		return nil
	case "w":
		return m.openInbox()
	case "n":
		return m.nextWaiting()
	case "a n":
		return m.openNote(m.cur())
	case "a h":
		return m.openSent()
	case "h":
		return m.openSummary()
	case "?", ":":
		return m.openPalette()
	case "s":
		return m.openSettings()
	}
	return nil
}

// scrollKey moves through the active tab's scrollback.
func (m *Model) scrollKey(msg tea.KeyMsg) {
	t := m.cur()
	if t == nil || t.screen() == nil {
		m.scrolling = false
		return
	}
	w, h := m.tabSize(t)
	if p := t.screen(); p.AltScreen() {
		m.altScrollKey(p, msg, w, h)
		return
	}
	n := t.screen().ScrollbackLen()
	switch msg.String() {
	case "up", "k":
		t.scroll++
	case "down", "j":
		t.scroll--
	case "pgup", "b", "ctrl+u", "u":
		t.scroll += h - 2
	case "pgdown", "f", "ctrl+d", "d", " ":
		t.scroll -= h - 2
	case "g", "home":
		t.scroll = n
	case "G", "end":
		t.scroll = 0
	case "q", "esc", "enter":
		t.scroll = 0
		m.scrolling = false
		return
	}
	t.scroll = min(max(t.scroll, 0), n)
}

// altScrollKey is scrollKey for a program on the alternate screen, which
// keeps no scrollback for nerdr to show — a full-screen agent scrolls its
// own: the keys go to it as a terminal sends the wheel and the page keys
// there.
func (m *Model) altScrollKey(p *term.Pane, msg tea.KeyMsg, w, h int) {
	wheel := func(btn ansi.MouseButton, arrow string) {
		for range 3 {
			if !p.SendMouse(term.MouseEvent{Button: btn, X: w / 2, Y: h / 2}) {
				p.Input([]byte(arrow))
			}
		}
	}
	switch msg.String() {
	case "up", "k":
		wheel(ansi.MouseWheelUp, "\x1b[A")
	case "down", "j":
		wheel(ansi.MouseWheelDown, "\x1b[B")
	case "pgup", "b", "ctrl+u", "u":
		p.Input([]byte("\x1b[5~"))
	case "pgdown", "f", "ctrl+d", "d", " ":
		p.Input([]byte("\x1b[6~"))
	case "q", "esc", "enter":
		m.scrolling = false
	}
}

// restart starts the tab's agent again, continuing its conversation.
func (m *Model) restart(t *tab) tea.Cmd {
	if t.pane != nil {
		old := t.pane
		go old.Close()
	}
	t.fallbackTried = false
	m.scrolling = false
	return m.start(t, true)
}

// keyBytes encodes a key the way a terminal would have sent it. It is
// only a fallback for keys that reach bubbletea while the router is
// switching to pass-through, so the common keys are enough.
func keyBytes(msg tea.KeyMsg) []byte {
	var out []byte
	if msg.Alt {
		out = append(out, 0x1b)
	}
	switch msg.Type {
	case tea.KeyRunes:
		s := string(msg.Runes)
		if msg.Paste {
			s = "\x1b[200~" + s + "\x1b[201~"
		}
		return append(out, s...)
	case tea.KeySpace:
		return append(out, ' ')
	case tea.KeyEnter:
		return append(out, '\r')
	case tea.KeyTab:
		return append(out, '\t')
	case tea.KeyShiftTab:
		return append(out, "\x1b[Z"...)
	case tea.KeyBackspace:
		return append(out, 0x7f)
	case tea.KeyEsc:
		return append(out, 0x1b)
	case tea.KeyUp:
		return append(out, "\x1b[A"...)
	case tea.KeyDown:
		return append(out, "\x1b[B"...)
	case tea.KeyRight:
		return append(out, "\x1b[C"...)
	case tea.KeyLeft:
		return append(out, "\x1b[D"...)
	case tea.KeyHome:
		return append(out, "\x1b[H"...)
	case tea.KeyEnd:
		return append(out, "\x1b[F"...)
	case tea.KeyPgUp:
		return append(out, "\x1b[5~"...)
	case tea.KeyPgDown:
		return append(out, "\x1b[6~"...)
	case tea.KeyDelete:
		return append(out, "\x1b[3~"...)
	}
	// Control keys: bubbletea numbers them by the byte they are.
	if msg.Type >= 0 && msg.Type < 0x20 {
		return append(out, byte(msg.Type))
	}
	return nil
}
