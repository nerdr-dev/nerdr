// Package app is nerdr's screen: a row of tabs, each an agent working in
// its own git worktrees, with the review panel beside it.
package app

import (
	"os"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// Model is the whole program's state. It is used through a pointer: the
// panes' goroutines hold on to it (notify), so it must not be copied.
type Model struct {
	core.Core
	// laidRows and laidSide are the bar rows and the sidebar's width at
	// the end of the last Update, laidOut once they are; see Update.
	laidRows, laidSide int
	laidOut            bool
	// tabLooks and paneLooks are how the tabs and panes are shown; see
	// look.go.
	tabLooks  map[*core.Tab]*tabLook
	paneLooks map[*core.PaneTab]*paneLook
	// Herdr is the herdr pane nerdr runs in, when it does: the client's
	// terminal, which core does not know of.
	Herdr *herdr.Client
	// herdrBefore is the state reported to herdr when the last look at the
	// agents began; see flushNotes.
	herdrBefore string
	// Active is the index in Tabs of the tab in front, the client's own:
	// core knows only what Front says.
	Active int
	// Loop runs core on its own goroutine, once the program has started; nil
	// in tests that drive Update by hand.
	Loop *core.Loop
	// screens keeps copies of core's screens sent as rows, to draw and
	// send keys to (NERDR_HOLDER=1); nil when the programs are drawn from
	// core's own. See SetScreens.
	screens    *core.ScreenClient
	prefixPage int
	// keyGroup is the group of commands the prefix went into ("i", say),
	// waiting for the command's key; "" at the top.
	keyGroup string
	// groupWalk is set while the group stays on after an arrow stepped
	// along the panes (t ←): a key that is none of its commands is then
	// typed to the pane walked to, as after walking along the tabs.
	groupWalk bool

	// convIndex is the agents' conversations read for the search; see
	// search.go.
	convIndex *agents.Index

	// cfgMod is the config file's time when last taken in; restartNoted
	// the settings read only at start that the file has changed, and to
	// what, told once (see reloadConfig).
	cfgMod       time.Time
	restartNoted map[string]string
	router       *input.Router

	started time.Time // when the last input was first asked for: input counts from then

	// lastInGroup is the id of the tab last active in each group, so
	// moving to a group returns to where you were in it.
	lastInGroup map[string]core.TabRef

	review review.Model
	// reviewView is the review panel as last drawn. It is drawn again from
	// it when reviewReuse says the last message was one that leaves the
	// panel as it was: panes drawing, which an agent at work does many
	// times a second, and the ticks that look at the tabs. The panel is the
	// costly part of the screen. See reuseCheck.
	reviewView  string
	reviewReuse bool
	// reviewGen is core's Gen when the panel was last drawn anew: core,
	// on its own goroutine, may have changed what it shows since.
	reviewGen  uint64
	reviewOpen bool
	// screenTab is the tab whose screen the review panel shows; see
	// pinReview.
	screenTab  *core.Tab
	reviewZoom bool
	// panelSmall is the review panel made small, to its F-keys: shown as
	// a column on the right while folded, opened by an F-key, and folded
	// again when the keyboard goes back to the agent. See rail.go.
	panelSmall bool
	// panelAuto is set while the panel, kept open, is small only to make
	// room for the active tab's panes, and opens again once they fit
	// beside it; panelKept while it was opened by hand all the same. See
	// fitPanel.
	panelAuto, panelKept bool

	// sideZoom is set while the panel is full width because F1's files
	// were put beside the diff in a panel too narrow for that; putting
	// them back above makes it narrow again.
	sideZoom    bool
	reviewWidth int // percent
	focus       core.FocusArea

	// split is the tab shown beside the active one, splitLeft whether it
	// is on the left, and splitWidth the right-hand tab's share of the
	// window in percent; see split.go.
	split      *core.Tab
	splitLeft  bool
	splitWidth int
	// reviewerCursor is the agent picked in a review pane to review with;
	// see reviewerpanel.go.
	reviewerCursor int
	// reviewPick is the model being picked for the agent picked there, nil
	// while the agent is; agentModels the models each agent listed; see
	// reviewmodels.go.
	reviewPick *modelPick

	// panelScreens is where the review panel was when you last left each tab
	// (F1 to F5), to go back to when you come to it again; paneBars what each
	// tab's row of panes showed when last drawn.
	panelScreens map[*core.Tab]review.Screen
	paneBars     map[*core.Tab]*barRow

	// blurred is set while the terminal (herdr) has taken focus away from
	// nerdr's window: then even the active tab is not being looked at.
	blurred bool

	// autoMessage is whether a new message is ticked to go by itself: as
	// the last one written was. msgCursor is the message selected in F3.
	autoMessage bool
	msgCursor   int
	// saved is F3's other view, the saved prompts; see prompts.go.
	saved savedView
	// skills is the F4 screen's; see skillspanel.go.
	skills skillsView
	// plugins is the F5 screen's state, and the plugins' screens started.
	plugins pluginsView

	// prefix is set after the prefix key, until the command key arrives;
	// nav keeps it on after a key that went to another tab, until enter.
	prefix bool
	nav    bool
	// onGroups is whether the arrows after the prefix walk the group row
	// (← → between repositories) rather than the tab row (← → between the
	// group's tabs); ↓ and ↑ move between the two. See startPrefix.
	onGroups bool
	// scrolling is the scrollback view of the active tab's pane with the
	// keyboard.
	scrolling bool

	// dialog is whatever modal is open: the new-tab wizard, a
	// confirmation, the help. nil when none is.
	dialog dialog

	// editor is a program (vim, from the review panel's "o") shown over
	// everything until it exits.
	editor *term.Pane

	width, height int

	// The mouse; see mouse.go and zones.go. zoneFns and zoneRects are
	// the last drawn screen's zones, and zoneTips their tips (see
	// tooltip.go); those from dialogZones on are the open dialog's, from
	// hintZones on the prefix hint's.
	zoneFns                []zoneFn
	zoneTips               []string
	hover                  hover
	zoneRects              []zoneRect
	dialogZones, hintZones int
	// frame is the screen as last drawn, and sameFrame says the last
	// message left it as it was: the pointer moving with no tip coming or
	// going, which the terminal reports for every cell it crosses.
	frame     string
	sameFrame bool
	// frameGen is core's Gen when the frame was drawn.
	frameGen uint64
	mouse    mouseMode
	drag     dragKind
	// dragTab and dragDiv are, while a divider between panes is dragged,
	// its tab and which divider: the one after its dragDiv'th pane shown.
	dragTab *core.Tab
	dragDiv int
	// sel is the text being chosen in selPane with the mouse, drawn while
	// selShown; see drawPane.
	sel      term.Selection
	selPane  core.Program
	selShown bool
	// drawnPanes are the programs' screens as last drawn; see drawPane.
	drawnPanes map[core.Program]*drawnPane
	// textSel is the text in a dialog being selected; see textsel.go.
	textSel *textSel
	// lastWheelStep is when the wheel last stepped to another tab or
	// group, lastWheel when it last turned at all, and wheelHeld says the
	// wheel is not heard until it has been still; see wheelStep and
	// wheelHeard.
	lastWheelStep, lastWheel time.Time
	wheelHeld                bool

	prAll prAll // everyone's pull requests, while the list shows them; see prall.go
	// prPanel is all your pull requests as the review panel's F2 shows
	// them, and prPanelShown whether it did at the last look; see
	// prpanel.go.
	prPanel *prList

	prPanelShown bool
	// branchPanel is what F2 shows about the active tab's branches, as the
	// tab panel does, and branchShown whether F2 showed it at the last
	// look; see prpanel.go.
	branchPanel *tabPanel
	branchShown bool
	// issuePanel is the issues as F2's third view shows them, and
	// issuePanelShown whether it did at the last look; see issuepanel.go.
	issuePanel      *issueList
	issuePanelShown bool
	// prAsking is the PRs a message for an agent is being made for, by
	// prKey, so a second f does not make another.
	prAsking map[string]bool

	// sideWidth is the columns the sidebar's edge was dragged to, its
	// frames included; 0 for as wide as its names need.
	sideWidth int

	toastText  string
	toastUntil time.Time
	// deniedSaid is how many denials had been noted when the last was
	// said in a toast (see toastDenied).
	deniedSaid int
	// inApp is what tabs behind the one shown have to say this tick, and
	// inAppTabs the tabs saying it; see notify.go. toastTabs are the tabs
	// the toast shown tells of, toastOf the text it was for them.
	inApp     []string
	inAppTabs []*core.Tab
	toastTabs []*core.Tab
	toastOf   string

	// notes are the notifications due this tick; see notify.go.
	notes []core.Note

	// entered is the tab last come to; see attention.go.
	entered *core.Tab
	// landed is when the active tab was come to, and looking is set until
	// it has been looked at; see seenAfter.
	landed  time.Time
	looking bool

	showDetails bool
	hideGroups  bool // the repository row put away; see showGroups
	sideBar     bool // the repositories and tabs left of the body; see sidebar.go
	sideSmall   bool // and made small, to their lights

	// groupRow and tabRows are what the bars showed when last drawn, the
	// tab row's per group; see barscroll.go. roomAsked is set while a new
	// tab goes ahead after the offer to make room for it.
	groupRow  barRow
	tabRows   map[string]*barRow
	roomAsked bool

	shutdown sync.Once

	// afterSetup is what waits for the user to say whether repositories'
	// .nerdr.toml may be done, by the token askSetup gave it.
	afterSetup map[int]func(m *Model) tea.Cmd
	lastSetup  int
}

// New builds the model for a session rooted at root.
func New(cfg config.Config, root string, router *input.Router, sess state.Session) *Model {
	m := &Model{
		Cfg:         cfg,
		Root:        root,
		WtRoot:      workspace.WorktreesRoot(root, cfg.WorktreesDir),
		router:      router,
		Herdr:       herdr.Detect(),
		review:      review.NewEmbedded(),
		reviewWidth: cfg.PanelWidth,
		splitWidth:  50,
		Wake:        make(chan struct{}, 1),
		lastInGroup: map[string]core.TabRef{},
		showDetails: *cfg.TabDetails,
		sideBar:     cfg.TabBar == "left" || cfg.TabBar == "left-small",
		sideSmall:   cfg.TabBar == "left-small",
		mouse:       mouseCells,
		width:       120,
		height:      40,
	}
	m.review = m.review.SetFocused(false).SetWideKey(m.pk("z z")).SetScreenKeys(m.screenMenuKeys())
	m.review, _ = m.review.SetDiffColors(cfg.DiffColors)
	m.review = m.review.SetPRRefresh(m.GithubEvery(core.PrPanelEvery))
	if st, err := os.Stat(config.Path()); err == nil {
		m.cfgMod = st.ModTime()
	}
	lightSymbols = cfg.StatusLights == "symbols"
	m.startLayout()
	if !m.mouseOn() {
		m.mouse = mouseOff
	}
	now := time.Now()
	m.Restore(sess, now)
	if sess.Active >= 0 && sess.Active < len(m.Tabs) {
		m.Active = sess.Active
	}
	m.entered = m.Cur() // as enter does; core marks it active
	for i, t := range m.Tabs {
		if sess.Split != "" && t.ID == sess.Split && i != m.Active && t.GroupName() == m.curGroup() {
			m.split, m.splitLeft = t, sess.SplitLeft
			m.reviewOpen = false
		}
	}
	m.sideWidth = sess.SideWidth
	m.Client = m
	return m
}

// Init starts the restored tabs' agents, or the new-tab wizard when there
// are none.
func (m *Model) Init() tea.Cmd {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	// Core's own ticks wait on Mu until this is done.
	if m.Loop != nil {
		m.Loop.Start()
	}
	// A title of its own, which a terminal that names its tab after the
	// program running in it can show in place of the one running last
	// under nerdr: its shells and git.
	cmds := []tea.Cmd{tea.SetWindowTitle("nerdr"), m.review.Init()}
	if m.Cfg.Install == "" {
		// The first start: where the worktrees, the skills and plugins and the notes
		// go, before anything else. The new-tab dialog follows when there
		// are no tabs.
		var then func(m *Model) tea.Cmd
		if len(m.Tabs) == 0 {
			then = (*Model).openWizard
		}
		cmds = append(cmds, m.openSetup(then))
	} else {
		m.do(core.ApplySkills{})
	}
	if len(m.Tabs) == 0 {
		if m.dialog == nil {
			cmds = append(cmds, m.openWizard())
		}
	} else {
		cmds = append(cmds, m.restoreStarts()...)
		cmds = append(cmds, m.pinReview(), fromCore(core.PruneOrders(m.Tabs)))
		m.toast("restored " + core.Plural(len(m.Tabs), "tab"))
		if t := m.Cur(); t != nil && t.Spinoff != nil && m.dialog == nil {
			cmds = append(cmds, m.openSpinoff(t))
		}
	}
	m.syncInput()
	return tea.Batch(cmds...)
}

// isKey reports whether msg is the key k.
func isKey(msg tea.Msg, k string) bool {
	km, ok := msg.(tea.KeyMsg)
	return ok && km.String() == k
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if p, ok := msg.(core.PanicMsg); ok {
		panic(p.String()) // here, where the terminal is put back
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	msgs := m.panelMessages()
	if !m.laidOut {
		m.laidRows, m.laidSide, m.laidOut = m.barRows(), m.sideW(), true
	}
	switch msg.(type) {
	case core.FrameMsg, core.DetectMsg, core.InfoTickMsg, core.RedrawMsg, core.ScreensMsg:
		m.reviewReuse = m.Gen == m.reviewGen
	default:
		m.reviewReuse = false
	}
	m.sameFrame = false
	cmd := m.update(msg)
	m.askReviewDone()
	if m.barRows() != m.laidRows || m.sideW() != m.laidSide {
		// The repository row came or went: a second repository's first
		// tab, its last one closed, or b. Or the sidebar came, went or
		// changed its width with a tab's name. Measured against the last
		// Update, not this one's start: core, on its own goroutine, may
		// have changed them in between.
		cmd = tea.Batch(cmd, m.resize())
	}
	m.laidRows, m.laidSide = m.barRows(), m.sideW()
	if m.panelMessages() && (!msgs || isKey(msg, "f3") && m.dialog == nil) {
		// Arriving on F3, the keyboard goes with it — and on F3 pressed
		// again from the agent: the arrows and space are for the list then,
		// not the agent, whose ↑ brings back the message it was last sent.
		m.setFocus(core.FocusReview)
		m.msgCursor = core.NoMessage
	}
	if m.split != nil && m.splitTab() == nil {
		// Its tab was closed, became the active one, or the active tab is
		// in another repository.
		m.split = nil
		m.resizePanes()
	}
	if s := m.review.PopToast(); s != "" {
		m.toast(s) // the panel has no status bar of its own
	}
	// Off screen, the panel's file list is left until it is back.
	var shown tea.Cmd
	m.review, shown = m.review.SetShown(m.reviewZoom || m.reviewOpen && !m.splitShown())
	m.review = m.review.SetAlone(m.reviewZoom)
	m.syncInput()
	return m, tea.Batch(cmd, shown, m.syncMouse())
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	if m.Quitting {
		return m.whileQuitting(msg)
	}
	if cmd, ok := m.Handle(msg); ok {
		return fromCore(cmd)
	}
	switch msg := msg.(type) {
	case core.ChangedMsg, core.RedrawMsg:
		return nil // only to draw what core has done

	case tea.WindowSizeMsg:
		if msg.Width == m.width && msg.Height == m.height {
			return nil // the size asked for again below, unchanged
		}
		m.width, m.height = msg.Width, msg.Height
		// Terminal.app can drop the last size of a resize, leaving nerdr
		// drawn narrower than the window with the old screen showing
		// beside it: the screen is cleared, and the size asked for again
		// once the resize has settled.
		return tea.Batch(m.resize(), tea.ClearScreen, tea.Tick(core.ResizeSettle, func(time.Time) tea.Msg {
			return tea.WindowSize()()
		}))

	case core.ScreensMsg:
		m.checkCopied()
		return nil

	case core.FrameMsg:
		if m.screens != nil {
			m.screens.Drop(m.Runs)
		}
		m.checkEditor()
		m.do(core.CheckPrograms{})
		m.checkCopied()
		return nil

	case core.DetectMsg:
		return m.detect()

	case usageReadMsg:
		m.onUsageRead(msg)
		return nil

	case core.ToastMsg:
		m.toast(string(msg))
		return nil

	case searchReadMsg:
		m.onSearchRead(msg)
		return nil

	case core.InfoTickMsg:
		m.toastDenied()
		// Core has looked at what goes on in the background; see core.InfoTick.
		return tea.Batch(m.configCheck(), m.syncPRPanel())

	case sentMsg:
		m.onSent(msg)
		return nil

	case input.PrefixMsg:
		m.startPrefix()
		if m.router != nil {
			m.router.PrefixSeen()
		}
		return nil

	case tea.FocusMsg:
		core.Debugf("focus")
		m.blurred = false
		m.wheelHeld = true // a glide begun elsewhere may still be coming
		cmd := m.onHerdrFocus()
		if t := m.Cur(); t != nil {
			m.focusTerms(t)
			if m.dwelt() {
				m.do(core.SeeAgent{A: m.Ref(t), Glance: true})
			}
		}
		m.reportHerdr()
		return cmd

	case tea.BlurMsg:
		core.Debugf("blur")
		m.blurred = true
		if t := m.Cur(); t != nil {
			m.focusTerms(t)
		}
		return nil

	case tea.KeyMsg:
		m.hideTip()
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tipMsg:
		m.onTip(msg)
		return nil

	case review.LayoutMsg:
		return m.onPanelLayout(msg.Beside)

	case review.LeaveMsg:
		m.setFocus(core.FocusAgent)
		return nil

	case review.HelpMsg:
		return m.openPalette()

	case review.CopyMsg:
		m.copyText(msg.Text)
		return nil

	case review.EditMsg:
		return m.openEditor(msg)

	case wizardPRsMsg:
		m.onWizardPRs(msg)
		return nil

	case forkFetchedMsg:
		return m.onForkFetched(msg)

	case prAllMsg:
		m.onPRAll(msg)
		return nil

	case prAskMsg:
		m.onPRAsk(msg)
		return nil

	case review.AgentMsg:
		return m.askAgent(msg)

	case review.ExplainMsg:
		m.dialog = &explainDialog{e: msg}
		return nil

	case commitFilesMsg:
		m.onCommitFiles(msg)
		return nil

	case wizardMsg:
		if w, ok := m.dialog.(*wizard); ok && w == msg.w {
			return w.handle(m, msg)
		}
		return m.createdBehind(msg)

	case planMsg:
		if w, ok := m.dialog.(*wizard); ok {
			return w.handlePlan(m, msg)
		}
		return nil

	case closeInfoMsg:
		if c, ok := m.dialog.(*closeTab); ok && c.t == msg.t {
			c.dirty, c.unpushed, c.loaded = msg.dirty, msg.unpushed, true
			c.mergedHeads, c.heads = msg.mergedHeads, msg.heads
		}
		return nil

	case repoCheckedMsg:
		if m.dialog == msg.d {
			msg.d.onChecked(msg)
		}
		return nil

	case closeShowMsg:
		if m.dialog == msg.c {
			msg.c.shown = true
		}
		return nil

	case promptEnterMsg:
		m.promptEnter(msg.t)
		return nil

	case prActionMsg:
		return m.prDone(msg)

	case newRepoMsg:
		if w, ok := m.dialog.(*wizard); ok && w == msg.w {
			return w.handleNewRepo(m, msg)
		}
		if msg.path != "" {
			m.toast("made " + msg.path)
		} else if msg.err != nil {
			m.toast(msg.err.Error())
		}
		return nil

	case issueMsg:
		if w, ok := m.dialog.(*wizard); ok {
			return w.handleIssue(msg)
		}
		return nil

	case setupProgressMsg:
		if w, ok := m.dialog.(*wizard); ok && w == msg.w && w.step == stepCreating {
			w.busy = msg.s
			return listenProgress(w)
		}
		return nil // hidden: its setup does not wait for anyone to listen

	case quitReadyMsg:
		return tea.Quit
	}

	// Everything else is the review panel's own business: its ticks, the
	// results of its background loads.
	var cmd tea.Cmd
	var rm tea.Model
	rm, cmd = m.review.Update(msg)
	m.review = rm.(review.Model)
	return cmd
}

// whileQuitting handles msg once the agents are being stopped. Nothing
// is looked at or started any more: an agent stopped would be taken for
// one that died, and one that was resuming its conversation started anew
// in its place, its conversation forgotten. Which conversation each agent
// is in is still taken, and saved, for the next start to resume: one it
// moved to just before (a /clear), one found for it. A report waiting
// for an answer is told why there is none.
func (m *Model) whileQuitting(msg tea.Msg) tea.Cmd {
	if _, ok := m.Handle(msg); ok {
		return nil
	}
	switch msg := msg.(type) {
	case quitReadyMsg:
		return tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.resize()
	}
	return nil
}

// syncInput tells the input router where keys go now: straight to the
// active agent, or through bubbletea to nerdr.
func (m *Model) syncInput() {
	if m.router == nil {
		return
	}
	// An editor, and a full-screen program in a terminal, have F1-F5 for
	// themselves: htop, mc and vim use them. A shell at its prompt does
	// not, and leaves them to the panel's screens. The prefix leads to the
	// screens either way (e 1-4).
	m.router.PassFKeys(false)
	if m.editor != nil {
		if exited, _, _ := m.editor.Exited(); !exited {
			m.router.PassFKeys(true)
			m.router.Passthrough(m.editor.Input)
			return
		}
	}
	t := m.Cur()
	if m.Quitting || m.prefix || m.dialog != nil || m.scrolling || t == nil {
		m.router.Passthrough(nil)
		return
	}
	if p := m.pluginUIPane(); p != nil {
		// A plugin's screen in F5; F1-F5 and the prefix stay nerdr's.
		m.router.Passthrough(p.Input)
		return
	}
	p := m.keyPane()
	f := m.focused(t)
	a := f.A
	if p == nil || (f.Kind != core.PaneShell && a != nil && a.Light == core.StExited) {
		m.router.Passthrough(nil)
		return
	}
	if exited, _, _ := p.Exited(); exited {
		m.router.Passthrough(nil)
		return
	}
	if f.Kind != core.PaneShell && a != nil {
		// Core follows what is typed to an agent but Claude, whose
		// prompts it reads from its log: the program it gives then does.
		m.do(core.KeysTo{Ref: core.ProgramRef{Pane: m.PaneRef(f)}})
		p = m.keyPane()
	}
	m.router.PassFKeys(f.Kind == core.PaneShell && p.AltScreen())
	m.router.Passthrough(p.Input)
}

// detect refreshes every agent's traffic light and passes the result on.
func (m *Model) detect() tea.Cmd { return m.do(core.Detect{}) }

// tabShown reports whether t is on screen: in front, or beside it.
func (m *Model) tabShown(t *core.Tab) bool {
	return t == m.Cur() || t == m.splitTab() && m.splitShown()
}

// detected is the TUI's part once core has looked at every agent: news of
// the tabs shown has done its job, and what was gathered to tell is told.
func (m *Model) detected(now time.Time) tea.Cmd {
	if m.editor == nil && !m.blurred {
		for _, t := range m.Tabs {
			if m.tabShown(t) {
				m.toastSeen(t)
			}
		}
	}
	m.do(core.KeepAwake{LastInput: m.lastInput(now)})
	m.reportHerdr()
	m.flushNotes(m.herdrBefore)
	return nil
}

// agentVisible reports whether agent a is on screen, in a pane of the tab
// shown, and you are looking.
func (m *Model) agentVisible(a *core.Tab) bool {
	o := a.Owner()
	if o == m.Cur() && !m.dwelt() {
		return false // only passed on the way: see seenAfter
	}
	if o != m.Cur() && (o != m.splitTab() || !m.splitShown()) {
		return false
	}
	p := a.PaneOf()
	return p != nil && m.paneShown(o, p) && m.editor == nil && !m.blurred
}

// resize lays everything out again for the current window size.
func (m *Model) resize() tea.Cmd {
	m.resizePanes()
	rw, rh := m.reviewSize()
	m.review = m.review.SetMenu(m.panelMenu())
	var cmd tea.Cmd
	var rm tea.Model
	rm, cmd = m.review.Update(tea.WindowSizeMsg{Width: rw, Height: rh})
	m.review = rm.(review.Model)
	if m.editor != nil {
		m.editor.Resize(m.width, m.height)
	}
	return cmd
}

func (m *Model) resizePanes() {
	m.fitPanel()
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			switch {
			case p.Kind == core.PaneShell:
				if pr := m.program(p); pr != nil {
					pr.Resize(m.shellSize(t, p))
				}
			case p.A != nil && p.A.HasProgram():
				if pr := m.program(p); pr != nil {
					pr.Resize(m.paneSize(p.A))
				}
			}
		}
	}
}

// agentArea is the active tab's part of the body, its panes' frames and
// row included: below the bars, left of the review panel and the column
// between them, or its side of the split.
func (m *Model) agentArea() (int, int) {
	w := m.mainW()
	if m.splitShown() {
		lw, rw := m.splitWidths()
		w = lw
		if m.splitLeft {
			w = rw
		}
	} else if m.reviewOpen && !m.reviewZoom {
		rw, _ := m.reviewArea()
		w = m.mainW() - rw - 1
	}
	return max(w, 12), max(m.height-m.barRows(), 5)
}

// mainW is the columns of the body left of the menu's column.
func (m *Model) mainW() int { return m.bodyW() - m.railCols() }

// agentSize is the area the active tab's agent draws in: its area inside
// the frame.
func (m *Model) agentSize() (int, int) {
	w, h := m.agentArea()
	return w - 2, h - 2
}

// reviewSize is the area the review panel draws in.
func (m *Model) reviewSize() (int, int) {
	w, h := m.reviewArea()
	return w, max(h-m.panelTop(), 1)
}

// reviewArea is the review panel's part of the body, its frame included:
// panel_width of the body, or all of it left of the menu's column.
func (m *Model) reviewArea() (int, int) {
	if m.reviewZoom {
		return m.mainW(), max(m.height-m.barRows(), 3)
	}
	w := m.mainW() * m.reviewWidth / 100
	return min(max(w, 30), m.mainW()-13), max(m.height-m.barRows(), 3)
}

// pinReview points the review panel at the active tab's worktrees.
func (m *Model) pinReview() tea.Cmd {
	t := m.Cur()
	var pins []review.Pin
	if t != nil {
		pins = tabPins(t)
	}
	// The panel's screen (F1 to F5) is the tab's own. It is kept on the tab
	// it was chosen in, not on whichever is active by now: a tab opened or
	// closed changes the active one before the panel hears of it.
	moved := t != m.screenTab
	if moved && m.screenTab != nil && m.TabIndex(m.screenTab) >= 0 {
		if m.panelScreens == nil {
			m.panelScreens = map[*core.Tab]review.Screen{}
		}
		m.panelScreens[m.screenTab] = m.review.Screen()
	}
	var cmd tea.Cmd
	m.review, cmd = m.review.Pin(pins)
	if moved && t != nil {
		cmd = tea.Batch(cmd, m.review.SetScreen(m.panelScreens[t]), m.syncPRPanel())
	}
	m.screenTab = t
	var unseen map[string]map[string]bool
	if t != nil {
		unseen = t.Unseen
	}
	m.review = m.review.SetUnseen(unseen)
	m.shareOverlaps()
	return cmd
}

// shareOverlaps tells the review panel which of the active tab's files
// other tabs change too.
func (m *Model) shareOverlaps() {
	var shared map[string][]string
	if t := m.Cur(); t != nil {
		for k, names := range m.Shared[t.ID] {
			if shared == nil {
				shared = map[string][]string{}
			}
			shared[review.SharedKey(k.Repo, k.File)] = names
		}
	}
	m.review = m.review.SetShared(shared)
	var counts []int
	if t := m.Cur(); t != nil {
		for i := range t.Worktrees {
			n := -1
			if i < len(t.Info.Wts) && t.Info.Known() && t.Info.Wts[i].Err == "" {
				n = len(t.Info.Wts[i].Stats.Changed)
			}
			counts = append(counts, n)
		}
	}
	m.review = m.review.SetPinCounts(counts)
}

// setFocus moves the keyboard between the agent and the review panel.
func (m *Model) setFocus(f core.FocusArea) {
	if f == core.FocusAgent || f == core.FocusReview && !m.review.Plugins() {
		m.plugins.uiFocus = ""
	}
	if f == core.FocusReview && !m.reviewOpen {
		m.reviewOpen = true
		m.resizePanes()
	}
	if f == core.FocusAgent {
		m.reviewZoom = false
		if m.panelSmall {
			m.reviewOpen = false // folded to its F-keys again
		}
		m.resizePanes()
	}
	m.focus = f
	m.review = m.review.SetFocused(f == core.FocusReview)
}

// selectTab makes tab i the active one.
func (m *Model) selectTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.Tabs) {
		return nil
	}
	if i != m.Active {
		m.plugins.uiFocus = ""
	}
	if old := m.Cur(); old != nil && i != m.Active {
		for _, p := range old.Panes() {
			if pr := m.program(p); pr != nil {
				pr.Focus(false)
			}
		}
	}
	if s := m.splitTab(); s != nil && m.Tabs[i] == s {
		// The other tab on screen: the keyboard moves across, and each
		// keeps its side.
		m.split, m.splitLeft = m.Cur(), !m.splitLeft
	}
	splitEnded := m.split != nil && m.split.GroupName() != m.Tabs[i].GroupName()
	if splitEnded {
		m.split = nil // only tabs of one repository share the screen
	}
	m.Active = i
	m.scrolling = false
	t := m.Tabs[i]
	tracked := m.track() // before seen: a finished agent is summed up as finished
	m.lastInGroup[t.GroupName()] = m.Ref(t)
	m.landed, m.looking = time.Now(), true
	m.focusTerms(t)
	if m.split != nil || splitEnded {
		m.resizePanes() // the tab may have changed sides, or have the window
	}
	m.saveLayout()
	m.reportHerdr()
	var ask tea.Cmd
	if m.dialog == nil && t.Spinoff != nil {
		ask = m.openSpinoff(t)
	}
	return tea.Batch(tracked, m.pinReview(), ask)
}

// look marks the active tab looked at once it has been so for seenAfter
// with you looking: not away in another window or behind an editor.
func (m *Model) look() {
	t := m.Cur()
	if !m.looking || t == nil || !m.dwelt() || m.blurred || m.editor != nil {
		return
	}
	m.looking = false
	// The user has seen what became of the last message too.
	m.do(core.SeeAgent{A: m.Ref(t)})
}

// dwelt reports whether the active tab has been so long enough to count as
// looked at.
func (m *Model) dwelt() bool {
	return time.Since(m.landed) >= core.SeenAfter
}

func (m *Model) toast(s string) {
	m.toastText = s
	m.toastUntil = time.Now().Add(4 * time.Second)
}

// Shutdown stops every agent. The session file keeps the tabs, so the next
// start resumes them. It runs with the program no longer updating the
// model: after it has ended, or in a test.
func (m *Model) Shutdown() {
	if m.Loop != nil {
		m.Loop.Stop()
	}
	m.shutdown.Do(func() {
		m.Mu.Lock()
		panes := m.stopping()
		m.Mu.Unlock()
		core.StopPanes(panes)
		m.ShutdownHolder()
		m.Herdr.Release()
	})
}

// stopping saves the session and lists every program to stop, there and
// then: what reads the model is done before the programs are stopped,
// which takes a while and runs out of the screen's way when quitting.
func (m *Model) stopping() []*term.Pane {
	m.Quitting = true
	m.SaveSession()
	var panes []*term.Pane
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			if tp := p.Term(); tp != nil {
				panes = append(panes, tp)
			}
		}
	}
	if m.editor != nil {
		panes = append(panes, m.editor)
	}
	return append(panes, m.PluginUIPanes()...)
}

// onPanelLayout follows F1's file list going beside the diff or back above
// it: beside, in a panel narrower than both need, the panel goes full
// width, and back above, to the width it had.
func (m *Model) onPanelLayout(beside bool) tea.Cmd {
	switch {
	case beside && !m.reviewZoom:
		if w, _ := m.reviewSize(); w >= review.BesideWidth {
			return nil
		}
		m.reviewZoom, m.sideZoom = true, true
		m.toast("files beside the diff: the panel is full width (" + m.pk("z z") + " gives the agent its room back)")
	case !beside && m.sideZoom && m.reviewZoom:
		m.reviewZoom = false
	default:
		m.sideZoom = false
		return nil
	}
	if !m.reviewZoom {
		m.sideZoom = false
	}
	return m.resize()
}

// tabPins is what the review panel shows for the tab.
func tabPins(t *core.Tab) []review.Pin {
	var out []review.Pin
	for _, w := range t.Worktrees {
		out = append(out, review.Pin{Repo: w.Repo, Path: w.Path})
	}
	return out
}
