package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
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
		if m.railShown() {
			page := max(m.bodyH()-4, 1)
			if msg.String() == "pgup" {
				page = -page
			}
			m.scrollKeys(page)
			return nil
		}
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
		return m.startPrefix()
	}
	if m.dialog != nil {
		if msg.String() == "ctrl+o" {
			if cmd, ok := m.nextMachine(); ok {
				return cmd
			}
		}
		return m.inDialogView(func() tea.Cmd { return m.dialog.key(m, msg) })
	}
	if m.global.shown && msg.String() == "ctrl+o" && m.globalNextMachine() {
		return nil
	}
	if panelKey(msg.String()) {
		// The review panel's menu, whichever side has the keyboard — and
		// out of the scrollback, which would take them for its own. The
		// panel gets the keyboard: the screen asked for is there to be used.
		if m.scrolling {
			m.scrollKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
		return m.screenPressed(msg)
	}
	if m.scrolling {
		m.scrollKey(msg)
		return nil
	}
	if m.focus == core.FocusReview && msg.String() == "?" &&
		(m.panelMessages() || m.panelSkills() || m.panelPlugins() || m.panelHistory() || m.panelAllPRs() || m.panelIssues()) {
		// ? is help, as in the rest of the panel.
		return m.openPalette()
	}
	if m.focus == core.FocusReview && m.panelMessages() {
		return m.messagesKey(msg)
	}
	if m.focus == core.FocusReview && m.panelSkills() {
		return m.skillsKey(msg)
	}
	if m.focus == core.FocusReview && m.panelPlugins() {
		return m.pluginsKey(msg)
	}
	if m.focus == core.FocusReview && m.panelHistory() {
		return m.historyKey(msg)
	}
	if m.focus == core.FocusReview && m.panelAllPRs() {
		return tea.Batch(m.allPRsKey(msg), m.syncPRPanel())
	}
	if m.focus == core.FocusReview && m.panelIssues() {
		return tea.Batch(m.issuesKey(msg), m.syncPRPanel())
	}
	if m.focus == core.FocusReview && m.panelBranchPR() {
		cmd, done := m.branchKey(msg.String())
		if done {
			return cmd
		}
		return tea.Batch(cmd, m.reviewKey(msg))
	}
	if m.focus == core.FocusReview {
		return m.reviewKey(msg)
	}
	t := m.Cur()
	if t == nil {
		if msg.String() == "enter" || msg.String() == "c" {
			return m.openWizard()
		}
		return nil
	}
	if mc := m.machineOf(t); mc != nil && grey(mc) {
		return m.greyKey(mc, msg.String())
	}
	f := m.focused(t)
	if f.Kind == core.PaneView && m.viewIn == nil && m.focus == core.FocusAgent {
		// The pane's panel, as if it had the keyboard as the dialog has.
		return m.inView(t, f, func() tea.Cmd { return m.handleKey(msg) })
	}
	if f.Kind == core.PaneReview && f.A == nil {
		return m.reviewPickKey(t, f, msg)
	}
	if a := f.A; f.Kind != core.PaneShell && a != nil && (a.Light == core.StExited || !a.HasProgram()) {
		if msg.String() == "enter" || msg.String() == "r" {
			return m.restart(a)
		}
		return nil
	}
	if p := m.program(f); p != nil {
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
	_, ok := review.ScreenOfFKey(k)
	return ok
}

// functionKeys is whether hints name the panel's screens F1 to F5.
func (m *Model) functionKeys() bool {
	return m.cfg().FunctionKeys == nil || *m.cfg().FunctionKeys
}

// screenKey is how a hint names the review panel's screen of F-key n (1
// to 5): F1 to F5, or the prefix keys for it with function_keys off.
func (m *Model) screenKey(n int) string {
	if m.functionKeys() {
		return "F" + strconv.Itoa(n)
	}
	return m.pk(fkeyCommand(n))
}

// screenFKey is the F-key each of these commands presses: the way to the
// review panel's screens without the function keys. F2's is g g, the
// branch's pull request, which has a command of its own.
var screenFKey = map[string]string{"g c": "f1", "e": "f3", "k": "f4", "h": "f5"}

// fkeyCommand is the command that opens the screen of F-key n (1 to 5).
func fkeyCommand(n int) string {
	return [...]string{"", "g c", "g g", "e", "k", "h"}[max(0, min(n, 5))]
}

// screenMenuKey is screenKey for a menu, whose keys come after the
// prefix: "F4", or "k".
func (m *Model) screenMenuKey(n int) string {
	if m.functionKeys() {
		return "F" + strconv.Itoa(n)
	}
	return m.shownKeys(fkeyCommand(n))
}

// screenMenuKeys are the keys the review panel's menu shows before each
// screen's name, and the hint for all of them: none with function_keys
// on, for the panel's own F1 to F5. The plugins, which have no F-key,
// have none either.
func (m *Model) screenMenuKeys() ([]string, string) {
	if m.functionKeys() {
		return nil, ""
	}
	var keys []string
	for i := range review.ScreenNames() {
		k := ""
		if f := review.ScreenFKey(i); f != "" {
			k = strings.ReplaceAll(m.shownKeys(fkeyCommand(int(f[1]-'0'))), " ", "")
		}
		keys = append(keys, k)
	}
	return keys, m.cfg().Prefix + " and the key by its name"
}

// keyPane is the program that has the keyboard: the one in the active
// tab's pane with it; nil when the review panel has it, or none does.
func (m *Model) keyPane() core.Program {
	t := m.Cur()
	if t == nil || m.focus == core.FocusReview {
		return nil
	}
	return m.screen(t)
}

// startPrefix turns the prefix on for a new command. The arrows start on
// the group row when there are groups to choose between, else on the tabs.
func (m *Model) startPrefix() tea.Cmd {
	m.prefixPage, m.prefixAt = 0, time.Now()
	m.keyGroup, m.groupWalk = "", false
	m.prefix, m.nav = true, false
	// The arrows start on the tab row, among the repository's own tabs —
	// unless it has only the one, when ← → can only mean the next
	// repository.
	m.onGroups = len(m.fleet.Groups()) > 1 && len(m.fleet.GroupTabs(m.curGroup())) < 2
	return m.hintDue()
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
		"m", "M", "w", "n", "f", " ", "r", "h", "p", "u", "/", "j", "k", "e", "d", "i", "v":
		return true
	}
	return core.IsGroup(k)
}

// groupKey runs the key pressed in group g. esc or backspace goes back to the prefix's
// own keys; a key the group does not have ends it, rather than going to
// the agent: it was meant as a command.
func (m *Model) groupKey(g string, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	walking := m.groupWalk
	m.groupWalk = false
	switch {
	case k == "esc" || k == "backspace":
		m.prefix, m.prefixPage = true, 0
		return nil
	case m.isPrefix(msg) && walking:
		// A new command after the steps.
		return m.startPrefix()
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
	run, ok := core.GroupCommand(g, k)
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
	l, ok := strings.CutPrefix(m.cfg().PrefixKey(), "ctrl+")
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
	return m.cfg().Prefix + " " + m.shownKeys(keys)
}

func (m *Model) isPrefix(msg tea.KeyMsg) bool {
	return strings.EqualFold(msg.String(), m.cfg().PrefixKey())
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
	if core.IsGroup(k) {
		// The group's own keys next, in the hint line.
		m.prefix, m.keyGroup, m.prefixPage = true, k, 0
		return nil
	}
	return m.run(k, msg)
}

// run runs the command whose keys after the prefix are id ("x", "g p"),
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
		if !m.onGroups && len(m.fleet.GroupTabs(m.curGroup())) < 2 && len(m.fleet.Groups()) > 1 {
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
		m.onGroups = k == "up" && len(m.fleet.Groups()) > 1
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
		m.setFocus(core.FocusAgent)
		return m.resize()
	case "t c":
		return m.togglePane()
	case "v":
		return m.toggleGlobal()
	case "t p":
		if m.global.shown {
			// In the global tab, between its list and the plugin's screen,
			// as in a plugins pane.
			m.togglePluginUI()
			return nil
		}
		return m.screenPane(core.ViewPlugins)
	case "g c", "e", "k", "h":
		// F1-F5 without the function keys: a Mac laptop's are the
		// brightness and volume unless fn is held, and a shell or an
		// editor keeps them for itself. The screen is the id's, not the key
		// pressed.
		return m.handleKey(keyMsg(screenFKey[id]))
	case "o":
		return m.openWorktreeEditor()
	case "<", ",", ">", ".":
		if !m.splitShown() {
			return nil
		}
		width := &m.splitWidth
		if k == "<" || k == "," {
			*width = min(*width+5, 80)
		} else {
			*width = max(*width-5, 20)
		}
		return m.resize()
	case "l v":
		return m.toggleSplit()
	case "l s":
		return m.swapSides()
	case "b", "pgup":
		if t := m.Cur(); t != nil && m.screen(t) != nil {
			m.scrolling = true
			m.tl(t).scroll = 0
			if k == "pgup" {
				m.scrollKey(msg)
			}
		}
		return nil
	case "a r":
		t := m.CurAgent()
		if t == nil {
			return nil
		}
		if !t.Light.AtWork() {
			return m.restart(t)
		}
		// Restarting cuts the turn short: what the agent has not written to
		// its log yet is lost.
		m.dialog = &confirm{
			title: "Restart " + m.machineOf(t).Core.AgentName(t) + "?",
			lines: restartLines(t),
			yes: func(m *Model) tea.Cmd {
				if !m.machineOf(t).Core.Live(t) {
					return nil
				}
				return m.restart(t)
			},
		}
		return nil
	case "j":
		return m.openUnseen()
	case "i":
		// Send the prefix key itself to the agent (or the shell).
		if p := m.keyPane(); p != nil {
			if b, err := m.cfg().PrefixByte(); err == nil {
				p.Write([]byte{b})
			}
		}
		return nil
	case "q":
		// A core of the TUI's own with servers asks which to quit, too.
		if _, away := m.quitSides(); m.Remote() || len(away) > 0 {
			m.dialog = closeOrQuit{}
			return nil
		}
		m.dialog = &confirm{
			title: "Quit nerdr?",
			lines: []string{
				"The agents are stopped. Their tabs and conversations are",
				"restored the next time you start nerdr in " + m.home().Core.Root + ".",
			},
			// This machine's alone, as the dialog says: a server that
			// connects again while it is open is not stopped unnamed.
			yes: func(m *Model) tea.Cmd { return m.quit(false) },
		}
		return nil
	case "t t":
		return m.newShell()
	case "t a":
		return m.openNewAgent()
	case "t r", "a c":
		return m.newReviewPane()
	case "t x":
		if m.focus == core.FocusReview && m.panelShown() {
			return m.closePanel()
		}
		return m.askClosePane()
	case "t m":
		if t := m.Cur(); t != nil {
			return m.minimisePane(t, m.focused(t))
		}
		return nil
	case "t ←":
		return fromCore(m.stepPane(-1))
	case "t →":
		return fromCore(m.stepPane(1))
	case "t ⇧←":
		return fromCore(m.movePane(-1))
	case "t ⇧→":
		return fromCore(m.movePane(1))
	case "a s":
		return m.openSwitchAgent()
	case "a m":
		return m.openMove()
	case "a x":
		return m.stopReview()
	case "m":
		return m.openMessage(false)
	case "M":
		return m.openMessage(true)
	case "p":
		return m.openPromptPicker()
	case "g g":
		return m.showBranchPR()
	case "g p":
		return m.showAllPRs()
	case "g b":
		m.openTabPRs()
		return nil
	case "g r":
		return m.openRepos()
	case "g o":
		return m.showIssues()
	case "f", " ":
		return m.openSwitcher()
	case "r":
		return m.openRename()
	case "shift+left":
		return m.moveTab(-1)
	case "shift+right":
		return m.moveTab(1)
	case "g l":
		d, cmd := m.newCallsDialog()
		m.dialog = d
		return cmd
	case "u":
		return m.openUsage()
	case "d":
		return m.openMachines()
	case "/":
		return m.openSearch()
	case "l d":
		m.showDetails = !m.showDetails
		return nil
	case "l g":
		m.bottomBar = !m.bottomBar
		return m.resize()
	case "l t":
		m.sideBar = !m.sideBar
		if m.sideBar && m.sideW() == 0 {
			m.toast("The window is too narrow for the tabs on the left")
		}
		return m.resize()
	case "l m":
		return m.toggleSideSmall()
	case "l b":
		if m.sideW() > 0 {
			m.toast("The repositories stay in the sidebar; " + m.pk("l m") + " makes it small")
			return nil
		}
		m.hideGroups = !m.hideGroups
		if len(m.fleet.Groups()) < 2 {
			m.toast("Only one repository: its row shows once there are two")
		}
		return nil
	case "w":
		return m.openInbox()
	case "n":
		return m.nextWaiting()
	case "a n":
		return m.openNote(m.Cur())
	case "a h":
		return m.openSent()
	case "a w":
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
	t := m.Cur()
	if t == nil || m.screen(t) == nil {
		m.scrolling = false
		return
	}
	w, h := m.tabSize(t)
	if p := m.screen(t); p.AltScreen() {
		m.altScrollKey(p, msg, w, h)
		return
	}
	n := m.screen(t).ScrollbackLen()
	switch msg.String() {
	case "up", "k":
		m.tl(t).scroll++
	case "down", "j":
		m.tl(t).scroll--
	case "pgup", "b", "ctrl+u", "u":
		m.tl(t).scroll += h - 2
	case "pgdown", "f", "ctrl+d", "d", " ":
		m.tl(t).scroll -= h - 2
	case "g", "home":
		m.tl(t).scroll = n
	case "G", "end":
		m.tl(t).scroll = 0
	case "q", "esc", "enter":
		m.tl(t).scroll = 0
		m.scrolling = false
		return
	}
	m.tl(t).scroll = min(max(m.tl(t).scroll, 0), n)
}

// altScrollKey is scrollKey for a program on the alternate screen, which
// keeps no scrollback for nerdr to show — a full-screen agent scrolls its
// own: the keys go to it as a terminal sends the wheel and the page keys
// there.
func (m *Model) altScrollKey(p core.Program, msg tea.KeyMsg, w, h int) {
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
func (m *Model) restart(t *core.Tab) tea.Cmd {
	if m.machineOf(t).Core.ReposHold(t) {
		return nil
	}
	m.scrolling = false
	return m.do(core.Restart{A: m.fleet.Ref(t)})
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

// restartLines is what a restart of t, at work, costs.
func restartLines(t *core.Tab) []string {
	if t.Light == core.StBackground {
		return []string{
			string(t.Agent) + " has a process running in the background.",
			"The conversation continues, but restarting stops the process.",
		}
	}
	return []string{
		string(t.Agent) + " is working. Restarting stops it mid-turn;",
		"the conversation continues, but the turn is cut short.",
	}
}
