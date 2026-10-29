// Package app is nerdr's screen: a row of tabs, each an agent working in
// its own git worktrees, with the review panel beside it.
package app

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/usage"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

type focusArea int

const (
	focusAgent focusArea = iota
	focusReview
)

// resizeSettle is how long after a resize the size is asked for again.
const resizeSettle = 300 * time.Millisecond

// Model is the whole program's state. It is used through a pointer: the
// panes' goroutines hold on to it (notify), so it must not be copied.
type Model struct {
	prefixPage int
	// keyGroup is the group of commands the prefix went into ("i", say),
	// waiting for the command's key; "" at the top.
	keyGroup string
	// groupWalk is set while the group stays on after an arrow stepped
	// along the panes (t ←): a key that is none of its commands is then
	// typed to the pane walked to, as after walking along the tabs.
	groupWalk bool

	// claudeLimits is the last of what a claude agent said of its
	// account's limits, and when; see usage.go.
	claudeLimits *usage.Report
	claudeAt     time.Time
	// convIndex is the agents' conversations read for the search; see
	// search.go.
	convIndex *agents.Index

	cfg    config.Config
	root   string // the folder nerdr was started in
	wtRoot string // where new worktrees are made
	// cfgMod is the config file's time when last taken in; restartNoted
	// the settings read only at start that the file has changed, and to
	// what, told once (see reloadConfig).
	cfgMod       time.Time
	restartNoted map[string]string
	router       *input.Router
	herdr        *herdr.Client

	// keeper keeps the machine awake while keep_awake asks; made the first
	// time it is wanted. See keepAwake.
	keeper    keeper
	keeperErr string
	lastBusy  time.Time
	started   time.Time // when keepAwake first ran: input counts from then

	tabs   []*tab
	active int
	// lastInGroup is the id of the tab last active in each group, so
	// moving to a group returns to where you were in it.
	lastInGroup map[string]string

	review review.Model
	// reviewView is the review panel as last drawn. It is drawn again from
	// it when reviewReuse says the last message was one that leaves the
	// panel as it was: panes drawing, which an agent at work does many
	// times a second, and the ticks that look at the tabs. The panel is the
	// costly part of the screen. See reuseCheck.
	reviewView  string
	reviewReuse bool
	reviewOpen  bool
	// screenTab is the tab whose screen the review panel shows; see
	// pinReview.
	screenTab  *tab
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
	// skillUse is how much the agents have used each skill, for F4; see
	// skilluse.go.
	skillUse map[string]*skillTotal
	// sideZoom is set while the panel is full width because F1's files
	// were put beside the diff in a panel too narrow for that; putting
	// them back above makes it narrow again.
	sideZoom    bool
	reviewWidth int // percent
	focus       focusArea

	// split is the tab shown beside the active one, splitLeft whether it
	// is on the left, and splitWidth the right-hand tab's share of the
	// window in percent; see split.go.
	split      *tab
	splitLeft  bool
	splitWidth int
	// reviewerCursor is the agent picked in a review pane to review with;
	// see reviewerpanel.go.
	reviewerCursor int
	// reviewPick is the model being picked for the agent picked there, nil
	// while the agent is; agentModels the models each agent listed; see
	// reviewmodels.go.
	reviewPick  *modelPick
	agentModels map[agents.Kind]modelList

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
	mouse     mouseMode
	drag      dragKind
	// dragTab and dragDiv are, while a divider between panes is dragged,
	// its tab and which divider: the one after its dragDiv'th pane shown.
	dragTab *tab
	dragDiv int
	sel     term.Selection
	selPane *term.Pane
	// textSel is the text in a dialog being selected; see textsel.go.
	textSel *textSel
	// lastWheelStep is when the wheel last stepped to another tab or
	// group, lastWheel when it last turned at all, and wheelHeld says the
	// wheel is not heard until it has been still; see wheelStep and
	// wheelHeard.
	lastWheelStep, lastWheel time.Time
	wheelHeld                bool

	// prWatch is the watching of your pull requests; see prwatch.go.
	prWatch prWatch
	prAll   prAll // everyone's pull requests, while the list shows them; see prall.go
	// prPanel is all your pull requests as the review panel's F2 shows
	// them, and prPanelShown whether it did at the last look; see
	// prpanel.go.
	prPanel *prList
	// issueWatch is the watching of the issues assigned to you; see
	// issuewatch.go.
	issueWatch   issueWatch
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
	// groupNames is the group name given to each folder of worktrees in
	// several repositories, kept when its tab is closed; see addTab.
	groupNames map[string]string
	// sandboxedDirs are the worktree folders where a sandboxed agent has
	// run, kept across tab close and reopen.
	sandboxedDirs map[string]bool
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
	inAppTabs []*tab
	toastTabs []*tab
	toastOf   string

	// infoBusy is set while a tab's branches are being looked at; see
	// info.go. overlaps is, per tab id, the other tabs changing some of
	// the same files, and shared the same by file, for the review panel's
	// file list. showDetails shows that state in the tab bar.
	// hookSock is the socket herdr's integrations in the agents report to,
	// and hookBin nerdr itself, for those that report through the CLI.
	hookSock, hookBin string
	// hookSockErr is why there is no hookSock, when there is not.
	hookSockErr string
	// codexHooks are the arguments that give codex nerdr's hooks, once
	// codex trusts them, and codexAsked whether it has been asked; see
	// codexHookArgs.
	codexHooks []string
	codexAsked bool

	// notes are the notifications due this tick; see notify.go.
	notes []note

	// entered is the tab last come to; see attention.go.
	entered *tab
	// landed is when the active tab was come to, and looking is set until
	// it has been looked at; see seenAfter.
	landed  time.Time
	looking bool

	infoBusy    bool
	overlaps    map[string][]overlap
	shared      map[string]map[string][]string
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

	wake chan struct{}
	// quitting is set once the program is on its way out.
	quitting bool
	shutdown sync.Once
}

// New builds the model for a session rooted at root.
func New(cfg config.Config, root string, router *input.Router, sess state.Session) *Model {
	m := &Model{
		cfg:         cfg,
		root:        root,
		wtRoot:      workspace.WorktreesRoot(root, cfg.WorktreesDir),
		router:      router,
		herdr:       herdr.Detect(),
		review:      review.NewEmbedded(),
		reviewWidth: cfg.PanelWidth,
		splitWidth:  50,
		wake:        make(chan struct{}, 1),
		lastInGroup: map[string]string{},
		overlaps:    map[string][]overlap{},
		showDetails: *cfg.TabDetails,
		sideBar:     cfg.TabBar == "left" || cfg.TabBar == "left-small",
		sideSmall:   cfg.TabBar == "left-small",
		mouse:       mouseCells,
		width:       120,
		height:      40,
	}
	m.review = m.review.SetFocused(false).SetWideKey(m.pk("z z")).SetScreenKeys(m.screenMenuKeys())
	m.review, _ = m.review.SetDiffColors(cfg.DiffColors)
	m.review = m.review.SetPRRefresh(m.githubEvery(prPanelEvery))
	if st, err := os.Stat(config.Path()); err == nil {
		m.cfgMod = st.ModTime()
	}
	lightSymbols = cfg.StatusLights == "symbols"
	m.startLayout()
	if !m.mouseOn() {
		m.mouse = mouseOff
	}
	now := time.Now()
	for _, st := range sess.Tabs {
		t := &tab{Tab: st, status: stStarting}
		trustWorktrees(t)
		t.TakeQueue()
		m.restoreAgents(t)
		if t.LastActive.IsZero() {
			t.LastActive = now // from before it was kept: not cold on that account
		}
		m.tabs = append(m.tabs, t)
	}
	if sess.Active >= 0 && sess.Active < len(m.tabs) {
		m.active = sess.Active
	}
	if t := m.cur(); t != nil {
		m.entered, t.LastActive = t, now // as enter does
	}
	for i, t := range m.tabs {
		if sess.Split != "" && t.ID == sess.Split && i != m.active && t.group() == m.curGroup() {
			m.split, m.splitLeft = t, sess.SplitLeft
			m.reviewOpen = false
		}
	}
	m.loadPRWatch(sess.PRs)
	m.loadIssueWatch(sess.Issues)
	m.groupNames = sess.GroupNames
	m.sandboxedDirs = sess.SandboxedDirs
	m.sideWidth = sess.SideWidth
	if m.groupNames == nil {
		m.groupNames = map[string]string{}
	}
	if m.sandboxedDirs == nil {
		m.sandboxedDirs = map[string]bool{}
	} else {
		for dir := range m.sandboxedDirs {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				delete(m.sandboxedDirs, dir)
			}
		}
	}
	for _, t := range m.tabs {
		m.markSandboxed(t)
	}
	return m
}

// Init starts the restored tabs' agents, or the new-tab wizard when there
// are none.
func (m *Model) Init() tea.Cmd {
	// A title of its own, which a terminal that names its tab after the
	// program running in it can show in place of the one running last
	// under nerdr: its shells and git.
	cmds := []tea.Cmd{tea.SetWindowTitle("nerdr"), m.review.Init(), waitFrame(m.wake), detectTick(), infoTick()}
	if m.cfg.Install == "" {
		// The first start: where the worktrees, the skills and plugins and the notes
		// go, before anything else. The new-tab dialog follows when there
		// are no tabs.
		var then func(m *Model) tea.Cmd
		if len(m.tabs) == 0 {
			then = (*Model).openWizard
		}
		cmds = append(cmds, m.openSetup(then))
	} else if note := m.applySkills(); note != "" {
		m.toast(note)
	}
	if len(m.tabs) == 0 {
		if m.dialog == nil {
			cmds = append(cmds, m.openWizard())
		}
	} else {
		cmds = append(cmds, m.restoreStarts()...)
		cmds = append(cmds, m.pinReview(), pruneOrders(m.tabs))
		m.toast("restored " + plural(len(m.tabs), "tab"))
		if t := m.cur(); t != nil && t.Spinoff != nil && m.dialog == nil {
			cmds = append(cmds, m.openSpinoff(t))
		}
	}
	m.syncInput()
	return tea.Batch(cmds...)
}

// pruneOrders drops the reading-order entries and walkthroughs of
// branches that are gone from the repositories of tabs, once at start, so
// the files do not grow for ever and a branch name used again does not
// find them; see order.Prune and walkthrough.Prune.
func pruneOrders(tabs []*tab) tea.Cmd {
	var paths []string
	for _, t := range tabs {
		for _, w := range t.Worktrees {
			paths = append(paths, w.Path)
		}
	}
	return func() tea.Msg {
		done := map[string]bool{}
		for _, p := range paths {
			if f := order.File(p); f != "" && !done[f] {
				done[f] = true
				_, _ = order.Prune(p)
			}
			if f := walkthrough.File(p); f != "" && !done[f] {
				done[f] = true
				_, _ = walkthrough.Prune(p)
			}
		}
		return nil
	}
}

type frameMsg struct{}
type detectMsg struct{}

// waitFrame waits for a pane to draw, then gives it a moment to finish
// the burst so one frame shows the lot.
func waitFrame(wake chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-wake
		time.Sleep(16 * time.Millisecond)
		return frameMsg{}
	}
}

func detectTick() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return detectMsg{} })
}

// isKey reports whether msg is the key k.
func isKey(msg tea.Msg, k string) bool {
	km, ok := msg.(tea.KeyMsg)
	return ok && km.String() == k
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msgs, rows, side := m.panelMessages(), m.barRows(), m.sideW()
	switch msg.(type) {
	case frameMsg, detectMsg, infoTickMsg:
		m.reviewReuse = true
	default:
		m.reviewReuse = false
	}
	m.sameFrame = false
	cmd := m.update(msg)
	m.askReviewDone()
	if m.barRows() != rows || m.sideW() != side {
		// The repository row came or went: a second repository's first
		// tab, its last one closed, or b. Or the sidebar came, went or
		// changed its width with a tab's name.
		cmd = tea.Batch(cmd, m.resize())
	}
	if m.panelMessages() && (!msgs || isKey(msg, "f3") && m.dialog == nil) {
		// Arriving on F3, the keyboard goes with it — and on F3 pressed
		// again from the agent: the arrows and space are for the list then,
		// not the agent, whose ↑ brings back the message it was last sent.
		m.setFocus(focusReview)
		m.msgCursor = noMessage
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
	if m.quitting {
		return m.whileQuitting(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width == m.width && msg.Height == m.height {
			return nil // the size asked for again below, unchanged
		}
		m.width, m.height = msg.Width, msg.Height
		// Terminal.app can drop the last size of a resize, leaving nerdr
		// drawn narrower than the window with the old screen showing
		// beside it: the screen is cleared, and the size asked for again
		// once the resize has settled.
		return tea.Batch(m.resize(), tea.ClearScreen, tea.Tick(resizeSettle, func(time.Time) tea.Msg {
			return tea.WindowSize()()
		}))

	case frameMsg:
		m.checkEditor()
		m.checkShells()
		m.checkPluginUIs()
		m.checkCopied()
		return waitFrame(m.wake)

	case detectMsg:
		return tea.Batch(m.detect(), detectTick())

	case hooks.Report:
		msg = m.vouch(msg)
		if t := m.agentByHook(msg.Pane); t != nil {
			t.heard = true // it reaches nerdr, whatever it asks
		}
		if why := m.sandboxRefusal(msg); why != "" {
			answerer(msg)(hooks.Answer{Err: why})
			return nil
		}
		switch msg.Method {
		case hooks.Spinoff:
			return m.onSpinoff(msg)
		case hooks.Handoff:
			return m.onHandoff(msg)
		case hooks.Review:
			return m.onReview(msg)
		case hooks.RequestReview:
			return m.onReady(msg)
		case hooks.Message:
			return m.onMessage(msg)
		case hooks.NewAgent:
			return m.onNewAgent(msg)
		case hooks.Usage:
			m.onUsage(msg)
			return nil
		case hooks.Query:
			return m.onQuery(msg)
		case hooks.Terminal:
			return m.onTerminal(msg)
		case hooks.Close:
			return m.onClose(msg)
		case hooks.NewTab:
			return m.onNewTab(msg)
		case hooks.Status:
			m.onStatus(msg)
			return nil
		case hooks.Plugin:
			return m.onPlugin(msg)
		}
		return m.onHook(msg)

	case spinoffMsg:
		return m.spinoffMade(msg)

	case usageReadMsg:
		m.onUsageRead(msg)
		return nil

	case toastMsg:
		m.toast(string(msg))
		return nil

	case tabMadeMsg:
		return m.tabMade(msg)

	case searchReadMsg:
		m.onSearchRead(msg)
		return nil

	case treeMsg:
		return m.onTree(msg)

	case focusFilesMsg:
		m.focusFiles(msg)
		return nil
	case infoTickMsg:
		m.toastDenied()
		return tea.Batch(m.configCheck(), m.refreshInfo(), m.tickPRs(time.Now()), m.tickIssues(time.Now()), m.syncPRPanel(),
			m.sleepIdle(time.Now()), infoTick())

	case infoMsg:
		m.setInfo(msg)
		return m.refreshInfo()

	case headsMsg:
		m.setHeads(msg)
		return nil

	case summaryMsg:
		m.onSummary(msg.s)
		return nil

	case claimMsg:
		m.onClaim(msg)
		return nil

	case goalMsg:
		msg.t.goalLooking = false
		m.setGoal(msg.t, msg.goal)
		m.setLatest(msg.t, msg.latest)
		return nil

	case sentMsg:
		m.onSent(msg)
		return nil

	case skillUseMsg:
		m.onSkillUse(msg)
		return nil

	case recapMsg:
		if !m.live(msg.t) {
			return nil
		}
		return m.lookUpGoal(msg.t)

	case input.PrefixMsg:
		m.startPrefix()
		return nil

	case tea.FocusMsg:
		debugf("focus")
		m.blurred = false
		m.wheelHeld = true // a glide begun elsewhere may still be coming
		cmd := m.onHerdrFocus()
		if t := m.cur(); t != nil {
			m.focusTerms(t)
			if m.dwelt() {
				t.seen()
			}
		}
		m.reportHerdr()
		return cmd

	case tea.BlurMsg:
		debugf("blur")
		m.blurred = true
		if t := m.cur(); t != nil {
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
		m.setFocus(focusAgent)
		return nil

	case review.HelpMsg:
		return m.openPalette()

	case review.CopyMsg:
		m.copyText(msg.Text)
		return nil

	case review.EditMsg:
		return m.openEditor(msg)

	case prWatchMsg:
		m.onPRWatch(msg)
		return nil

	case issueWatchMsg:
		m.onIssueWatch(msg)
		return nil

	case issueAllMsg:
		m.onIssueAll(msg)
		return nil

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

	case launchMsg:
		if !m.live(msg.t) || msg.kind != msg.t.Agent {
			return nil // closed or switched while its conversation was being looked up
		}
		if msg.handoff != "" {
			// First, ahead of what was waiting for the agent before.
			msg.t.Messages = append([]state.Message{{Text: msg.handoff, Auto: true, Sandboxed: msg.sandboxed}}, msg.t.Messages...)
			m.saveSession()
		}
		return m.spawn(msg.t, msg.l, msg.resume)

	case modelsMsg:
		return m.onModels(msg)

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

	case reposChangedMsg:
		return m.onReposChanged(msg)

	case closeShowMsg:
		if m.dialog == msg.c {
			msg.c.shown = true
		}
		return nil

	case sendEnterMsg:
		m.sendEnter(msg.t)
		return nil

	case promptEnterMsg:
		m.promptEnter(msg.t, msg.pane)
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

	case tabClosedMsg:
		if msg.err != "" {
			m.toast(msg.err)
		} else if msg.note != "" {
			m.toast(msg.note)
		}
		return nil

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
	switch msg := msg.(type) {
	case quitReadyMsg:
		return tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.resize()
	case frameMsg:
		return waitFrame(m.wake)
	case hooks.Report:
		if msg.Reply != nil {
			answerer(msg)(hooks.Answer{Err: "nerdr is quitting"})
			return nil
		}
		if m.sandboxRefusal(m.vouch(msg)) != "" {
			return nil
		}
		switch msg.Method {
		case hooks.ReportAgent, hooks.ReportAgentSession:
			m.hookSession(msg)
		}
	case claimMsg:
		m.onClaim(msg)
	}
	return nil
}

// cur is the active tab, or nil when there are none.
func (m *Model) cur() *tab {
	if m.active < 0 || m.active >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.active]
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
	t := m.cur()
	if m.quitting || m.prefix || m.dialog != nil || m.scrolling || t == nil {
		m.router.Passthrough(nil)
		return
	}
	if p := m.pluginUIPane(); p != nil {
		// A plugin's screen in F5; F1-F5 and the prefix stay nerdr's.
		m.router.Passthrough(p.Input)
		return
	}
	p := m.keyPane()
	f := t.focused()
	a := f.a
	if p == nil || (f.kind != paneShell && a != nil && p == a.pane && a.status == stExited) {
		m.router.Passthrough(nil)
		return
	}
	if exited, _, _ := p.Exited(); exited {
		m.router.Passthrough(nil)
		return
	}
	if f.kind != paneShell && a != nil && p == a.pane && a.Agent != agents.Claude {
		// Claude's prompts are read from its log; the other agents' are
		// taken from what is typed.
		if a.typed == nil {
			a.typed = &typedLine{}
		}
		typed := a.typed
		m.router.Passthrough(func(b []byte) {
			typed.feed(b)
			p.Input(b)
		})
		return
	}
	m.router.PassFKeys(f.kind == paneShell && p.AltScreen())
	m.router.Passthrough(p.Input)
}

// detect refreshes every agent's traffic light and passes the result on.
func (m *Model) detect() tea.Cmd {
	now := time.Now()
	herdrBefore := m.herdr.State()
	m.look()
	var cmds []tea.Cmd
	for i, t := range m.tabs {
		shown := i == m.active || (t == m.splitTab() && m.splitShown())
		if shown {
			t.shownAt = now
		}
		cmds = append(cmds, m.detectAgent(t, shown, now))
		for _, a := range t.extraAgents() {
			cmds = append(cmds, m.detectAgent(a, shown, now))
		}
		m.reportWhenBegun(t, now)
		if shown && m.editor == nil && !m.blurred {
			m.toastSeen(t)
		}
	}
	m.keepAwake(now)
	m.reportHerdr()
	m.flushNotes(herdrBefore)
	return tea.Batch(cmds...)
}

// agentVisible reports whether agent a is on screen, in a pane of the tab
// shown, and you are looking.
func (m *Model) agentVisible(a *tab) bool {
	o := a.owner()
	if o == m.cur() && !m.dwelt() {
		return false // only passed on the way: see seenAfter
	}
	if o != m.cur() && (o != m.splitTab() || !m.splitShown()) {
		return false
	}
	p := a.paneOf()
	return p != nil && m.paneShown(o, p) && m.editor == nil && !m.blurred
}

// detectAgent is detect for agent a, the own of a tab or another in one
// of its panes; shown is whether its tab is on screen.
func (m *Model) detectAgent(t *tab, shown bool, now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	if t.status == stAsleep {
		return nil // see sleep.go
	}
	visible := m.agentVisible(t)
	was := t.status
	t.classify(visible, now)
	if t.status != was && !shown {
		switch t.status {
		case stDone:
			t.owner().away.done++
		case stBlocked:
			t.owner().away.blocked++
		}
	}
	if t.status == stWorking {
		t.LastActive = now
		t.owner().LastActive = now
		if was != stWorking {
			cmds = append(cmds, m.lookUpGoal(t), m.promptLater(t)) // it has been asked something
			if t.typed != nil {
				t.typed.worked()
			}
		}
	}
	if t.typed != nil {
		if s := t.typed.take(); s != "" {
			t.noteSaid(s)
			m.setGoal(t, s)
			m.setLast(t, s)
		}
	}
	if t.status != was {
		debugf("agent %s: %s -> %s (visible %v, blurred %v)", m.agentName(t), was.label(), t.status.label(), visible, m.blurred)
		m.notifyChange(t, visible, now)
		if was == stWorking {
			// Whatever the agent did is worth a look in the tab bar.
			t.owner().lookAgain(false)
		}
	}
	if t.status == stExited && was != stExited {
		cmds = append(cmds, m.onExit(t))
	}
	if was == stWorking && (t.status == stIdle || t.status == stDone) {
		m.tellCutOff(t)
		// The log has the turn's prompt by now, whenever it was written.
		m.findingsDone(t)
		cmds = append(cmds, m.afterTurn(t), m.lookUpGoal(t), m.recapLater(t), m.countSkills(t), m.claimSession(t))
	}
	cmds = append(cmds, m.detectReviewer(t, now))
	m.checkSent(t, now)
	cmds = append(cmds, m.checkHandoff(t, now), m.deliverQueue(t, now))
	if visible && t.status == stDone {
		t.seen()
	}
	return tea.Batch(cmds...)
}

// onExit handles an agent that has stopped. One that was resuming an old
// conversation and died at once most likely could not find it (claude
// says so and exits when the conversation was never saved): start a new
// one in its place, once.
func (m *Model) onExit(t *tab) tea.Cmd {
	if t.pane == nil {
		return nil
	}
	_, err, ran := t.pane.Exited()
	if t.resumed && !t.fallbackTried && ran < 8*time.Second && err != nil {
		t.fallbackTried = true
		old := t.pane
		go old.Close()
		t.SessionID, t.ResumeArgv = "", nil
		cmd := m.start(t, false)
		if t.pane != nil && t.pane != old {
			m.toast(t.Name + ": could not resume, started a new conversation")
			m.saveSession()
		}
		return cmd
	}
	if err != nil {
		t.err = err.Error()
	}
	return nil
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
	for _, t := range m.tabs {
		for _, p := range t.panes() {
			switch {
			case p.kind == paneShell && p.shell != nil:
				p.shell.Resize(m.shellSize(t, p))
			case p.a != nil && p.a.pane != nil:
				p.a.pane.Resize(m.paneSize(p.a))
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
	t := m.cur()
	var pins []review.Pin
	if t != nil {
		pins = t.pins()
	}
	// The panel's screen (F1 to F5) is the tab's own. It is kept on the tab
	// it was chosen in, not on whichever is active by now: a tab opened or
	// closed changes the active one before the panel hears of it.
	moved := t != m.screenTab
	if moved && m.screenTab != nil {
		m.screenTab.panelScreen = m.review.Screen()
	}
	var cmd tea.Cmd
	m.review, cmd = m.review.Pin(pins)
	if moved && t != nil {
		cmd = tea.Batch(cmd, m.review.SetScreen(t.panelScreen), m.syncPRPanel())
	}
	m.screenTab = t
	var unseen map[string]map[string]bool
	if t != nil {
		unseen = t.unseen
	}
	m.review = m.review.SetUnseen(unseen)
	m.shareOverlaps()
	return cmd
}

// shareOverlaps tells the review panel which of the active tab's files
// other tabs change too.
func (m *Model) shareOverlaps() {
	var shared map[string][]string
	if t := m.cur(); t != nil {
		shared = m.shared[t.ID]
	}
	m.review = m.review.SetShared(shared)
	var counts []int
	if t := m.cur(); t != nil {
		for i := range t.Worktrees {
			n := -1
			if i < len(t.info.wts) && t.info.known() && t.info.wts[i].err == "" {
				n = len(t.info.wts[i].stats.Changed)
			}
			counts = append(counts, n)
		}
	}
	m.review = m.review.SetPinCounts(counts)
}

// setFocus moves the keyboard between the agent and the review panel.
func (m *Model) setFocus(f focusArea) {
	if f == focusAgent || f == focusReview && !m.review.Plugins() {
		m.plugins.uiFocus = ""
	}
	if f == focusReview && !m.reviewOpen {
		m.reviewOpen = true
		m.resizePanes()
	}
	if f == focusAgent {
		m.reviewZoom = false
		if m.panelSmall {
			m.reviewOpen = false // folded to its F-keys again
		}
		m.resizePanes()
	}
	m.focus = f
	m.review = m.review.SetFocused(f == focusReview)
}

// selectTab makes tab i the active one.
func (m *Model) selectTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	if i != m.active {
		m.plugins.uiFocus = ""
	}
	if old := m.cur(); old != nil && i != m.active {
		for _, p := range old.panes() {
			if tp := p.term(); tp != nil {
				tp.Focus(false)
			}
		}
	}
	if s := m.splitTab(); s != nil && m.tabs[i] == s {
		// The other tab on screen: the keyboard moves across, and each
		// keeps its side.
		m.split, m.splitLeft = m.cur(), !m.splitLeft
	}
	splitEnded := m.split != nil && m.split.group() != m.tabs[i].group()
	if splitEnded {
		m.split = nil // only tabs of one repository share the screen
	}
	m.active = i
	m.scrolling = false
	t := m.tabs[i]
	tracked := m.track() // before seen: a finished agent is summed up as finished
	m.lastInGroup[t.group()] = t.ID
	m.landed, m.looking = time.Now(), true
	m.focusTerms(t)
	if m.split != nil || splitEnded {
		m.resizePanes() // the tab may have changed sides, or have the window
	}
	m.saveSession()
	m.reportHerdr()
	var ask tea.Cmd
	if m.dialog == nil && t.Spinoff != nil {
		ask = m.openSpinoff(t)
	}
	return tea.Batch(tracked, m.pinReview(), ask)
}

// seenAfter is how long a tab must be the active one before it counts as
// looked at: tabs passed on the way to another, up or down the list, keep
// their green lights.
const seenAfter = time.Second

// look marks the active tab looked at once it has been so for seenAfter
// with you looking: not away in another window or behind an editor.
func (m *Model) look() {
	t := m.cur()
	if !m.looking || t == nil || !m.dwelt() || m.blurred || m.editor != nil {
		return
	}
	m.looking = false
	t.seen()
	t.queuePaused = false // the user has seen what became of the last message
}

// dwelt reports whether the active tab has been so long enough to count as
// looked at.
func (m *Model) dwelt() bool {
	return time.Since(m.landed) >= seenAfter
}

// toastMsg is a toast from work done out of the screen's way.
type toastMsg string

func (m *Model) toast(s string) {
	m.toastText = s
	m.toastUntil = time.Now().Add(4 * time.Second)
}

// Shutdown stops every agent. The session file keeps the tabs, so the next
// start resumes them. It runs with the program no longer updating the
// model: after it has ended, or in a test.
func (m *Model) Shutdown() {
	m.shutdown.Do(func() { stopPanes(m.stopping(), m.herdr) })
}

// stopping saves the session and lists every program to stop, there and
// then: what reads the model is done before the programs are stopped,
// which takes a while and runs out of the screen's way when quitting.
func (m *Model) stopping() []*term.Pane {
	m.quitting = true
	m.saveSession()
	var panes []*term.Pane
	for _, t := range m.tabs {
		for _, p := range t.panes() {
			if tp := p.term(); tp != nil {
				panes = append(panes, tp)
			}
		}
	}
	if m.editor != nil {
		panes = append(panes, m.editor)
	}
	return append(panes, m.pluginUIPanes()...)
}

// stopPanes stops panes side by side, and tells herdr nerdr is gone. It
// does not touch the model.
func stopPanes(panes []*term.Pane, h *herdr.Client) {
	var wg sync.WaitGroup
	for _, p := range panes {
		wg.Add(1)
		go func() { p.Close(); wg.Done() }()
	}
	wg.Wait()
	h.Release()
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
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
