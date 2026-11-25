package app

import (
	"strconv"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/reviewsource"

	tea "github.com/charmbracelet/bubbletea"
)

// View panes are the review panel's code changes (F1) and plugins
// in panes of a tab's own, beside its agents and terminals: as many of
// each as you like, each a panel of its own, with its own file, diff,
// repository and scroll, or its own plugin.
//
// The panel's code is written for one panel, m.review and what goes with
// it. A view pane's panel is put in its place while the pane is drawn, or
// handles a key, a click or a message of its own (inView), and taken out
// again after; the dialog's is kept aside meanwhile. Within, the panel
// has the keyboard if the pane has it. What the panel's commands give
// back comes as a viewMsg, to be handled in the same pane's panel.

// panelState is what one panel is: the model, how it was last drawn, and
// its plugins screen.
type panelState struct {
	review   review.Model
	view     string
	gen, seq uint64
	fit      []string
	fitOf    reviewFitKey
	plugins  pluginsView
}

// viewPane is a view pane's panel, and the size it is drawn at.
type viewPane struct {
	st panelState
	// ask is what the panel reads the repositories through, on the
	// machine of the pane's tab, when that is not this process.
	ask  *reviewsource.Asker
	mc   *fleet.Machine
	w, h int
	// dirty is something having happened in the panel since it was last
	// drawn; focused whether it had the keyboard then; shown whether its
	// pane is on screen, which a panel off it leaves its files alone for.
	dirty, focused, shown bool
}

// viewMsg is a message for view pane p's panel: what a command of its
// gave back.
type viewMsg struct {
	p   *core.PaneTab
	msg tea.Msg
}

// swapPanel puts the panel s in m's place, and m's in s: done twice, each
// is back.
func (m *Model) swapPanel(s *panelState) {
	m.review, s.review = s.review, m.review
	m.reviewView, s.view = s.view, m.reviewView
	m.reviewGen, s.gen = s.gen, m.reviewGen
	m.reviewSeq, s.seq = s.seq, m.reviewSeq
	m.reviewFit, s.fit = s.fit, m.reviewFit
	m.reviewFitOf, s.fitOf = s.fitOf, m.reviewFitOf
	m.plugins, s.plugins = s.plugins, m.plugins
}

// tagCmd has what cmd gives back come to view pane p's panel. Bubble
// Tea's own messages, and core's, are no panel's.
func tagCmd(p *core.PaneTab, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			out := make(tea.BatchMsg, 0, len(b))
			for _, c := range b {
				if c != nil {
					out = append(out, tagCmd(p, c))
				}
			}
			return out
		}
		if _, told := msg.(fleet.Told); msg == nil || told || ofBubbleTea(msg) {
			return msg
		}
		return viewMsg{p: p, msg: msg}
	}
}

// panelTab is the tab the panel is of: a view pane's own, or the active
// one for the dialog.
func (m *Model) panelTab() *core.Tab {
	if m.viewTab != nil {
		return m.viewTab
	}
	if m.global.shown {
		return nil // the global tab is no tab's
	}
	return m.Cur()
}

// panelOwner is the tab whose worktrees the panel reads: a view pane's
// own, or the one the dialog was pinned to.
func (m *Model) panelOwner() *core.Tab {
	if m.viewTab != nil {
		return m.viewTab
	}
	return m.screenTab
}

// viewFocused reports whether view pane p of t has the keyboard.
func (m *Model) viewFocused(t *core.Tab, p *core.PaneTab) bool {
	return m.focus == core.FocusAgent && t == m.Cur() && m.focused(t) == p
}

// inView runs fn with view pane p's panel in the panel's place, as if it
// were the only one: shown, and with the keyboard if p has it. Its
// commands come back to p's panel. The panel giving the keyboard back to
// the agent gives it to t's own.
func (m *Model) inView(t *core.Tab, p *core.PaneTab, fn func() tea.Cmd) tea.Cmd {
	if m.viewIn == p {
		return fn()
	}
	v := m.views[p]
	if v == nil || m.viewIn != nil {
		return nil
	}
	focused := m.viewFocused(t, p)
	left := v.focused && !focused
	if focused != v.focused {
		v.focused, v.dirty = focused, true
	}
	m.swapPanel(&v.st)
	if left {
		// The keyboard gone from the pane leaves its plugin screen, as
		// from the dialog: coming back does not go into it.
		m.plugins.uiFocus = ""
	}
	m.viewIn, m.viewTab = p, t
	focus, dlg, later := m.focus, m.panelDlg, m.later
	m.panelDlg, m.later = false, nil
	m.focus = core.FocusAgent
	if focused {
		m.focus = core.FocusReview
	}
	m.review = m.review.SetFocused(focused)
	dialog := m.dialog
	got := fn()
	if m.dialog != nil && m.dialog != dialog {
		// A question of the panel's, as whether to allow a plugin: what
		// is chosen in it is the panel's. The others opened from it (the
		// palette, a message to the agent) are the window's.
		m.dialogView, m.dialogFor = nil, nil
		if _, own := m.dialog.(*confirm); own {
			m.dialogView, m.dialogFor = p, m.dialog
		}
	}
	cmd := tea.Batch(append(m.later, got)...)
	now := m.focus
	toast := ""
	if !m.viewDraw {
		v.dirty = true
		toast = m.review.PopToast() // the panel has no status bar of its own
	}
	m.viewIn, m.viewTab = nil, nil
	m.swapPanel(&v.st)
	m.focus, m.panelDlg, m.later = focus, dlg, later
	cmd = tagCmd(p, cmd)
	if m.pluginsStale {
		m.pluginsChanged()
	}
	if toast != "" {
		m.toast(toast)
	}
	switch {
	case focused && now != core.FocusReview:
		// The panel let the keyboard go.
		m.focus = now
		if now == core.FocusAgent {
			cmd = tea.Batch(cmd, m.focusPane(t, t.OwnPane()))
		}
	case !focused && now == core.FocusReview:
		// The panel took the keyboard, a click on a row of it, say: its
		// pane has it.
		if i := m.fleet.TabIndex(t); i >= 0 && t != m.Cur() {
			cmd = tea.Batch(cmd, m.selectTab(i))
		}
		cmd = tea.Batch(cmd, m.focusPane(t, p))
	}
	return cmd
}

// viewOf is view pane p's panel, made the first time it is asked for:
// pointed at t's worktrees, on p's screen.
func (m *Model) viewOf(t *core.Tab, p *core.PaneTab) *viewPane {
	if v := m.views[p]; v != nil {
		return v
	}
	if m.views == nil {
		m.views = map[*core.PaneTab]*viewPane{}
	}
	v := &viewPane{dirty: true, shown: true}
	var colors tea.Cmd
	v.st.review, colors = m.freshReview()
	if m.reviewAsk != nil {
		// Its own tab's machine, whichever tab is active.
		mc := m.machineOf(t)
		m.lastAsk++
		v.ask = reviewsource.NewAskerNamed("view"+strconv.Itoa(m.lastAsk), func(job string, q reviewsource.Query) bool {
			return mc.Conn != nil && mc.Conn.Send(core.ReadForReview{Job: job, Q: q})
		})
		v.st.review = v.st.review.SetSource(v.ask)
		v.mc = mc
	}
	m.views[p] = v
	m.later = append(m.later, m.inView(t, p, func() tea.Cmd {
		var pin, shown tea.Cmd
		m.review, pin = m.review.Pin(tabPins(t))
		screen := m.review.SetScreen(review.ScreenAt(p.Screen))
		m.review = m.review.SetFixed(true) // its pane is of this screen
		m.review, shown = m.review.SetShown(true)
		m.review = m.review.SetUnseen(t.Unseen)
		m.shareOverlapsOf(t)
		if p.N > 1 {
			m.review = m.review.SetName(m.machineOf(t).Core.PaneName(t, p)) // "changes 2"
		}
		if dir, ok := m.viewPick[t]; ok && p.Screen == core.ViewPlugins {
			m.pickPlugin(t, dir)
			delete(m.viewPick, t)
		}
		return tea.Batch(m.review.Init(), colors, pin, screen, shown)
	}))
	return v
}

// freshReview is a panel set up as the dialog's is.
func (m *Model) freshReview() (review.Model, tea.Cmd) {
	var src review.Source = review.Local{}
	if m.reviewAsk != nil {
		src = m.reviewAsk
	}
	r := review.NewEmbedded(src).SetFocused(false).SetScreenKeys(m.screenMenuKeys())
	r = r.SetPRRefresh(m.home().Core.GithubEvery(core.PrPanelEvery))
	return r.SetDiffColors(m.cfg().DiffColors)
}

// newView opens screen i, the code changes or the plugins, in a new pane
// of the active tab's, with the keyboard.
func (m *Model) newView(i int) tea.Cmd {
	t := m.Cur()
	if t == nil || !paneScreen(i) {
		return nil
	}
	return m.do(core.NewView{T: m.fleet.Ref(t), Screen: i})
}

// viewOfScreen is t's view pane of screen i: the one used last.
func (m *Model) viewOfScreen(t *core.Tab, i int) *core.PaneTab {
	var best *core.PaneTab
	for _, p := range t.Panes() {
		if p.Kind == core.PaneView && p.Screen == i && (best == nil || m.pl(p).used.After(m.pl(best).used)) {
			best = p
		}
	}
	return best
}

// viewHome is the tab view pane p is in, nil once it is gone.
func (m *Model) viewHome(p *core.PaneTab) *core.Tab {
	for _, t := range m.fleet.Tabs() {
		for _, q := range t.Panes() {
			if q == p {
				return t
			}
		}
	}
	return nil
}

// eachView runs fn in the panel of every view pane there is.
func (m *Model) eachView(fn func(t *core.Tab, p *core.PaneTab) tea.Cmd) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.fleet.Tabs() {
		for _, p := range t.Panes() {
			if p.Kind == core.PaneView && m.views[p] != nil {
				cmds = append(cmds, m.inView(t, p, func() tea.Cmd { return fn(t, p) }))
			}
		}
	}
	return tea.Batch(cmds...)
}

// onViewMsg is a message for a view pane's panel; dropped once the pane
// is gone.
func (m *Model) onViewMsg(mc *fleet.Machine, msg viewMsg) tea.Cmd {
	t := m.viewHome(msg.p)
	if t == nil || m.views[msg.p] == nil {
		return nil
	}
	return m.inView(t, msg.p, func() tea.Cmd { return m.updateOn(mc, msg.msg) })
}

// sizeView gives view pane p's panel the size w×h it is drawn at.
func (m *Model) sizeView(t *core.Tab, p *core.PaneTab, w, h int) tea.Cmd {
	v := m.viewOf(t, p)
	if v.w == w && v.h == h {
		return nil
	}
	v.w, v.h = w, h
	return m.inView(t, p, func() tea.Cmd {
		rm, cmd := m.review.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m.review = rm.(review.Model)
		return cmd
	})
}

// sizeViews gives the view panes shown their sizes.
func (m *Model) sizeViews() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range []*core.Tab{m.Cur(), m.splitTab()} {
		if t == nil || (t == m.splitTab() && !m.splitShown()) {
			continue
		}
		_, aw, ah := m.tabArea(t)
		for _, c := range m.paneCols(t, aw) {
			if c.P.Kind == core.PaneView {
				cmds = append(cmds, m.sizeView(t, c.P, c.W, max(ah-m.rowH(t), 3)))
			}
		}
	}
	return tea.Batch(cmds...)
}

// viewLines draws view pane p of t, w×h, its own frames included, with
// the pane's buttons at the right of its top edge.
func (m *Model) viewLines(t *core.Tab, p *core.PaneTab, w, h int) []string {
	if cmd := m.sizeView(t, p, w, h); cmd != nil {
		m.later = append(m.later, cmd)
	}
	v := m.views[p]
	var lines []string
	reuse := m.reviewReuse
	m.viewDraw = true
	m.inView(t, p, func() tea.Cmd {
		m.reviewReuse = !v.dirty
		lines = m.panelLines(w, h)
		return nil
	})
	m.viewDraw = false
	m.reviewReuse = reuse
	v.dirty = false
	active := m.viewFocused(t, p)
	return endOver(lines, w, m.paneEnd(t, p, topFree(lines, w)-4), active)
}

// viewMouse is the mouse at (x, y) in view pane p of t, its frame
// included: the panel's.
func (m *Model) viewMouse(t *core.Tab, p *core.PaneTab, ev tea.MouseMsg, x, y int) tea.Cmd {
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		m.drag, m.dragView = dragView, p
	}
	ev.X, ev.Y = x, y
	return m.inView(t, p, func() tea.Cmd {
		rm, cmd := m.review.Update(ev)
		m.review = rm.(review.Model)
		return tea.Batch(cmd, m.syncPRPanel())
	})
}

// viewXY is where screen cell (sx, sy) is in view pane p of t's drawing,
// its frame included.
func (m *Model) viewXY(t *core.Tab, p *core.PaneTab, sx, sy int) (int, int) {
	rx, ry, _, _ := m.paneRect(t, p)
	return sx - (rx - 1), sy - (ry - 1)
}

// ensureViews makes the panels of the active tab's view panes that have
// none yet, their commands to go with the message being handled.
func (m *Model) ensureViews() {
	if m.viewIn != nil {
		return
	}
	for _, t := range []*core.Tab{m.Cur(), m.splitTab()} {
		if t == nil {
			continue
		}
		for _, p := range t.Panes() {
			if p.Kind == core.PaneView {
				m.viewOf(t, p)
			}
		}
	}
	m.showViews()
}

// inDialogView runs fn, a key or a click in the dialog, in the panel of
// the view pane that opened it, if one did.
func (m *Model) inDialogView(fn func() tea.Cmd) tea.Cmd {
	if m.dialog != m.dialogFor {
		m.dialogView, m.dialogFor = nil, nil // another dialog in its place
	}
	p := m.dialogView
	if p == nil || m.viewIn != nil {
		return fn()
	}
	t := m.viewHome(p)
	if t == nil || m.views[p] == nil {
		m.dialogView = nil
		return fn()
	}
	return m.inView(t, p, fn)
}

// askersOn are the readers asking machine mc: the dialog's while its tab
// is there, and those of the view panes of its tabs.
func (m *Model) askersOn(mc *fleet.Machine) []*reviewsource.Asker {
	var out []*reviewsource.Asker
	if m.reviewAsk != nil && m.reviewOn.Load() == mc {
		out = append(out, m.reviewAsk)
	}
	for _, v := range m.views {
		if v.ask != nil && v.mc == mc {
			out = append(out, v.ask)
		}
	}
	return out
}

// askers are the dialog's reader of the repositories and every view
// pane's: an answer is for the one whose job it names.
func (m *Model) askers() []*reviewsource.Asker {
	var out []*reviewsource.Asker
	if m.reviewAsk != nil {
		out = append(out, m.reviewAsk)
	}
	for _, v := range m.views {
		if v.ask != nil {
			out = append(out, v.ask)
		}
	}
	return out
}

// showViews tells each view pane's panel whether its pane is on screen:
// a tab not shown, or a pane minimised, leaves its files alone. One the
// keyboard has left lets go of its plugin screen.
func (m *Model) showViews() {
	for p, v := range m.views {
		t := m.viewHome(p)
		if t == nil {
			continue
		}
		if v.focused && !m.viewFocused(t, p) {
			// The keyboard has left the pane: see inView.
			m.later = append(m.later, m.inView(t, p, func() tea.Cmd { return nil }))
		}
		on := (t == m.Cur() || t == m.splitTab() && m.splitShown()) && m.paneShown(t, p)
		if on == v.shown {
			continue
		}
		v.shown = on
		m.later = append(m.later, m.inView(t, p, func() tea.Cmd {
			var cmd tea.Cmd
			m.review, cmd = m.review.SetShown(on)
			return cmd
		}))
	}
}

// eachPanelOn runs fn in the dialog's panel and every view pane's whose
// tab is on machine mc: what mc's core says of its worktrees is theirs.
func (m *Model) eachPanelOn(mc *fleet.Machine, fn func() tea.Cmd) tea.Cmd {
	var cmd tea.Cmd
	if t := m.screenTab; t == nil || m.machineOf(t) == mc {
		cmd = fn()
	}
	return tea.Batch(cmd, m.eachView(func(t *core.Tab, _ *core.PaneTab) tea.Cmd {
		if m.machineOf(t) != mc {
			return nil
		}
		return fn()
	}))
}
