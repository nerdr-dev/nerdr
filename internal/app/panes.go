package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"

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

// paneKind is what runs in a pane.
type paneKind int

const (
	// paneAgent is an agent: the tab's own, or another beside it.
	paneAgent paneKind = iota
	// paneShell is a terminal in the worktree.
	paneShell
	// paneReview is a code review of one of the tab's agents.
	paneReview
)

// paneTab is one of a tab's panes.
type paneTab struct {
	kind paneKind
	// a is the agent in the pane: the tab itself in its first pane,
	// another agent, or the reviewer; nil in a review pane while the
	// reviewer is being picked.
	a *tab
	// of is, in a review pane, the agent whose work it reviews.
	of *tab
	// shell is the terminal's program; n numbers the tab's terminals;
	// command is the last command an agent ran in it (nerdr terminal), to
	// run again.
	shell   *term.Pane
	n       int
	command string
	// small is set while the pane is minimised by hand; used is when it
	// last had the keyboard, for which to fold first when room runs out.
	small bool
	used  time.Time
	// weight is the pane's share of the room beside the others shown, as
	// its divider was dragged to; 0 is an even share.
	weight float64
}

// panes are t's panes, its own agent first.
func (t *tab) panes() []*paneTab {
	if len(t.paneList) == 0 || t.paneList[0].a != t {
		t.paneList = append([]*paneTab{{kind: paneAgent, a: t, used: time.Now()}}, t.paneList...)
	}
	if t.Num == 0 {
		t.Num = t.nextNum()
	}
	return t.paneList
}

// ownPane is the pane of t's own agent, which stays as long as the tab.
func (t *tab) ownPane() *paneTab { return t.panes()[0] }

// nextNum is the lowest number none of t's agents has. Numbers are kept
// for good: an agent that goes leaves a gap rather than renumber the
// others, which other agents may have been told.
func (t *tab) nextNum() int {
	used := map[int]bool{t.Num: true}
	for _, p := range t.paneList {
		if p.kind == paneAgent && p.a != nil {
			used[p.a.Num] = true
		}
	}
	n := 1
	for used[n] {
		n++
	}
	return n
}

// agentNumbered is the agent of t's with number n, or nil.
func (t *tab) agentNumbered(n int) *tab {
	for _, a := range append([]*tab{t}, t.extraAgents()...) {
		if a.Num == n {
			return a
		}
	}
	return nil
}

// focused is the pane of t's the keyboard goes to while it is with the
// tab: the one last gone to, or its own agent.
func (t *tab) focused() *paneTab {
	ps := t.panes()
	if t.on != nil && slices.Contains(ps, t.on) {
		return t.on
	}
	return ps[0]
}

// term is the program the pane shows: its agent's or its terminal's; nil
// while there is none.
func (p *paneTab) term() *term.Pane {
	if p.kind == paneShell {
		return p.shell
	}
	if p.a != nil {
		return p.a.pane
	}
	return nil
}

// agent is the agent the keys about an agent are for in pane p: its own,
// or in a review pane the one reviewed.
func (p *paneTab) agent() *tab {
	switch p.kind {
	case paneAgent:
		return p.a
	case paneReview:
		return p.of
	}
	return nil
}

// curAgent is the agent the keyboard is with, or the one it is nearest
// to: the active tab's focused pane's agent, its own otherwise.
func (m *Model) curAgent() *tab {
	t := m.cur()
	if t == nil {
		return nil
	}
	if a := t.focused().agent(); a != nil {
		return a
	}
	return t
}

// agentsIn are the agents working in t: its own, the others in its panes,
// and the reviewers of each.
func (t *tab) agentsIn() []*tab {
	var out []*tab
	for _, a := range append([]*tab{t}, t.extraAgents()...) {
		out = append(out, a)
		if a.review != nil {
			for _, s := range a.review.seats {
				out = append(out, s.r)
			}
		}
	}
	return out
}

// extraAgents are the agents t has beside its own, not counting reviewers.
func (t *tab) extraAgents() []*tab {
	var out []*tab
	for _, p := range t.panes()[1:] {
		if p.kind == paneAgent && p.a != nil {
			out = append(out, p.a)
		}
	}
	return out
}

// owner is the tab agent t works in: itself, or the tab its pane is in.
func (t *tab) owner() *tab {
	switch {
	case t.partOf != nil:
		return t.partOf
	case t.reviewOf != nil:
		return t.reviewOf.owner()
	}
	return t
}

// paneOf is the pane agent t is in, or nil.
func (t *tab) paneOf() *paneTab {
	for _, p := range t.owner().panes() {
		if p.a == t {
			return p
		}
	}
	return nil
}

// allAgents are every agent nerdr runs: the tabs' own, the others beside
// them and the reviewers.
func (m *Model) allAgents() []*tab {
	var out []*tab
	for _, t := range m.tabs {
		out = append(out, t.agentsIn()...)
	}
	return out
}

// agentByHook is the agent that reports as id, of any tab, or nil.
func (m *Model) agentByHook(id string) *tab {
	for _, a := range m.allAgents() {
		if a.hookID != "" && a.hookID == id {
			return a
		}
	}
	return nil
}

// rowShown reports whether t has the row over its panes: while one of
// them is minimised, there to be brought back.
func (m *Model) rowShown(t *tab) bool {
	_, w, _ := m.tabArea(t)
	return len(m.paneCols(t, w)) < len(t.panes())
}

// rowH is the rows the pane row takes over t's panes.
func (m *Model) rowH(t *tab) int {
	if m.rowShown(t) {
		return 1
	}
	return 0
}

// paneCol is one pane on screen: x and w its columns in the tab's area,
// its frame included.
type paneCol struct {
	p    *paneTab
	x, w int
}

// paneMinW is the narrowest a pane is shown, its frame included.
func (m *Model) paneMinW() int { return max(m.cfg.PaneMinWidth, 20) }

// paneCols are the panes of t shown in w columns: the one with the
// keyboard, and as many of the others not minimised as there is room for,
// those used last first, in the order of the row.
func (m *Model) paneCols(t *tab, w int) []paneCol {
	ps := t.panes()
	f := t.focused()
	open := []*paneTab{f}
	for _, p := range ps {
		if p != f && !p.small {
			open = append(open, p)
		}
	}
	slices.SortStableFunc(open[1:], func(a, b *paneTab) int { return b.used.Compare(a.used) })
	n := min(len(open), max((w+1)/(m.paneMinW()+1), 1))
	shown := open[:n]
	var out []paneCol
	for _, p := range ps {
		if slices.Contains(shown, p) {
			out = append(out, paneCol{p: p})
		}
	}
	// The room left by the columns between them, shared by weight; each
	// at least paneMinShare wide.
	room := w - (len(out) - 1)
	sum := 0.0
	for _, c := range out {
		sum += c.p.share()
	}
	x := 0
	for i := range out {
		left := len(out) - 1 - i // panes after this one
		cw := int(float64(room)*out[i].p.share()/sum + 0.5)
		cw = min(max(cw, paneMinShare), w-x-left*(paneMinShare+1))
		if i == len(out)-1 {
			cw = w - x
		}
		out[i].x, out[i].w = x, cw
		x += cw + 1
	}
	return out
}

// paneMinShare is the narrowest a divider can make a pane, its frame
// included.
const paneMinShare = 12

// share is p's weight, 1 when it has none of its own.
func (p *paneTab) share() float64 {
	if p.weight <= 0 {
		return 1
	}
	return p.weight
}

// dividerAt is which divider between t's panes is at (x, y) of its area —
// the column between two panes, or the frame either side of it — as the
// number of the pane before it; -1 for none.
func (m *Model) dividerAt(t *tab, x, y int) int {
	_, aw, ah := m.tabArea(t)
	if y < m.rowH(t) || y >= ah {
		return -1
	}
	cols := m.paneCols(t, aw)
	for i := 0; i+1 < len(cols); i++ {
		if gap := cols[i].x + cols[i].w; x >= gap-1 && x <= gap+1 {
			return i
		}
	}
	return -1
}

// dragDivider moves the divider after t's i'th pane shown to column x of
// its area: the two panes either side of it share their room anew, and
// the others keep theirs.
func (m *Model) dragDivider(t *tab, i, x int) tea.Cmd {
	_, aw, _ := m.tabArea(t)
	cols := m.paneCols(t, aw)
	if i < 0 || i+1 >= len(cols) {
		return nil
	}
	a, b := cols[i], cols[i+1]
	both := a.w + b.w
	aw2 := min(max(x-a.x, paneMinShare), both-paneMinShare)
	if aw2 == a.w {
		return nil
	}
	for _, c := range cols {
		c.p.weight = float64(c.w)
	}
	a.p.weight, b.p.weight = float64(aw2), float64(both-aw2)
	m.resizePanes()
	return nil
}

// paneShown reports whether p, one of t's panes, is on screen when t is.
func (m *Model) paneShown(t *tab, p *paneTab) bool {
	_, w, _ := m.tabArea(t)
	for _, c := range m.paneCols(t, w) {
		if c.p == p {
			return true
		}
	}
	return false
}

// tabArea is where tab t is drawn, its panes and their row: the column it
// starts at, and its width and height. A tab not on screen is given the
// active tab's, where it appears when gone to.
func (m *Model) tabArea(t *tab) (x, w, h int) {
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
func (m *Model) paneTop(p *paneTab, w, h int) int {
	switch {
	case p.kind == paneAgent:
		return len(m.taskRows(p.a, w, h))
	case p.kind == paneReview && p.a != nil:
		return reviewerHeadRows
	}
	return 0
}

// paneRect is where the program in t's pane p is drawn on the screen: its
// top-left corner and its size. One not on screen is given the size it
// would have in place of the pane with the keyboard.
func (m *Model) paneRect(t *tab, p *paneTab) (x, y, w, h int) {
	ax, aw, ah := m.tabArea(t)
	ch := max(ah-m.rowH(t), 3)
	cols := m.paneCols(t, aw)
	c := cols[0]
	if i := slices.IndexFunc(cols, func(o paneCol) bool { return o.p == p }); i >= 0 {
		c = cols[i]
	} else if i := slices.IndexFunc(cols, func(o paneCol) bool { return o.p == t.focused() }); i >= 0 {
		c = cols[i]
	}
	iw, ih := max(c.w-2, 1), max(ch-2, 1)
	top := m.paneTop(p, iw, ih)
	return ax + c.x + 1, m.barRows() + m.rowH(t) + 1 + top, iw, max(ih-top, 1)
}

// paneSize is the size of the program in agent a's pane.
func (m *Model) paneSize(a *tab) (int, int) {
	o := a.owner()
	p := a.paneOf()
	if p == nil {
		p = o.focused()
	}
	_, _, w, h := m.paneRect(o, p)
	return w, h
}

// shellSize is the size of the terminal in t's pane p: a column narrower
// than the pane, so the end of a long prompt and the cursor at it are not
// against the frame, nor cut off where the terminal draws a character
// (an icon in the prompt) wider than nerdr counts it.
func (m *Model) shellSize(t *tab, p *paneTab) (int, int) {
	_, _, w, h := m.paneRect(t, p)
	return max(w-shellMargin, 1), h
}

// shellMargin is the columns a terminal pane leaves empty at its right.
const shellMargin = 1

// focusPane gives p, one of t's panes, the keyboard: it is shown, and
// counts as used now.
func (m *Model) focusPane(t *tab, p *paneTab) tea.Cmd {
	if t.on != p {
		m.scrolling = false
		t.scroll = 0
	}
	t.on = p
	p.small = false
	p.used = time.Now()
	if a := p.a; a != nil && p.kind != paneShell {
		a.seen()
		a.queuePaused = false
	}
	m.setFocus(focusAgent)
	m.focusTerms(t)
	return m.resize()
}

// focusTerms tells t's programs which of them has the keyboard, for those
// that draw it (a cursor, a focus ring).
func (m *Model) focusTerms(t *tab) {
	f := t.focused()
	for _, p := range t.panes() {
		if tp := p.term(); tp != nil {
			tp.Focus(p == f && t == m.cur() && !m.blurred)
		}
	}
}

// stepPane moves the keyboard to the pane d along from the one with it,
// round past the ends; minimised ones are gone to too, and shown.
func (m *Model) stepPane(d int) tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	ps := t.panes()
	i := slices.Index(ps, t.focused())
	return m.focusPane(t, ps[(i+d+len(ps))%len(ps)])
}

// movePane moves the pane with the keyboard d places along, to the left
// for d < 0; not past the ends. The tab's own agent stays first.
func (m *Model) movePane(d int) tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	ps := t.panes()
	i := slices.Index(ps, t.focused())
	if i == 0 {
		m.toast(m.agentLabel(t, t) + " is the tab's first agent: it stays first")
		return nil
	}
	j := i + d
	if j < 1 || j >= len(ps) {
		return nil
	}
	ps[i], ps[j] = ps[j], ps[i]
	m.saveSession()
	return m.resize()
}

// addPane puts p last in t's panes, and gives it the keyboard unless
// quiet: a review that starts by itself does not take it, and is the
// first to be folded away.
func (m *Model) addPane(t *tab, p *paneTab, quiet bool) tea.Cmd {
	t.paneList = append(t.panes(), p)
	if quiet {
		m.resizePanes()
		return nil
	}
	return m.focusPane(t, p)
}

// dropPane takes p out of t's panes; the keyboard goes to the one used
// last when it had it, mostly the one it was opened from.
func (m *Model) dropPane(t *tab, p *paneTab) {
	ps := t.panes()
	i := slices.Index(ps, p)
	if i < 0 || p == t.ownPane() {
		return
	}
	if t.on == p {
		var next *paneTab
		for _, q := range ps {
			if q != p && (next == nil || q.used.After(next.used)) {
				next = q
			}
		}
		t.on = next
		next.used = time.Now()
		next.small = false
		m.scrolling = false
		t.scroll = 0
	}
	t.paneList = slices.Delete(ps, i, i+1)
	m.focusTerms(t)
	m.resizePanes()
}

// minimisePane folds p into the row, the keyboard going to the next pane
// shown; the tab's last pane shown stays.
func (m *Model) minimisePane(t *tab, p *paneTab) tea.Cmd {
	if p.small {
		return m.focusPane(t, p)
	}
	var other *paneTab
	_, w, _ := m.tabArea(t)
	for _, c := range m.paneCols(t, w) {
		if c.p != p {
			other = c.p
		}
	}
	if other == nil {
		for _, q := range t.panes() {
			if q != p && !q.small {
				other = q
			}
		}
	}
	if other == nil {
		m.toast("the last pane shown stays · " + m.pk("t t") + " opens a terminal beside it")
		return nil
	}
	p.small = true
	if t.focused() == p {
		return m.focusPane(t, other)
	}
	return m.resize()
}

// closePane stops what runs in p and takes it out of the row: its
// terminal, its agent or its reviewer. The tab's first agent goes only
// with the tab: it is always there to carry on the tab's conversation.
func (m *Model) closePane(t *tab, p *paneTab) tea.Cmd {
	switch {
	case p == t.ownPane():
		m.toast(m.agentLabel(t, t) + " is the tab's first agent: it stays as long as the tab · " + m.pk("x") + " closes the tab")
		return nil
	case p.kind == paneShell:
		if p.shell != nil {
			go p.shell.Close()
		}
	case p.kind == paneReview && p.a != nil:
		if p.of.seatOf(p.a) != nil {
			return tea.Batch(m.dropReviewer(p.of, p.a, string(p.a.Agent)+" stopped", false), m.resize())
		}
	case p.kind == paneAgent && p.a != nil:
		a := p.a
		if a.review != nil {
			m.endReview(a, "stopped", false)
		}
		for _, q := range append([]*paneTab{}, t.panes()...) {
			if q.kind == paneReview && q.of == a {
				m.dropPane(t, q)
			}
		}
		if a.pane != nil {
			go a.pane.Close()
		}
		m.dropPane(t, p)
		m.saveSession()
		m.toast(t.Name + ": stopped " + m.paneName(t, p) + "; its conversation is kept by " + string(a.Agent))
		return m.resize()
	}
	m.dropPane(t, p)
	return m.resize()
}

// askClosePane closes the pane with the keyboard, asking first when an
// agent in it is at work.
func (m *Model) askClosePane() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	p := t.focused()
	if p.kind == paneShell || p.a == nil || p.a.status != stWorking || p == t.ownPane() {
		return m.closePane(t, p)
	}
	m.dialog = &confirm{
		title: "Stop " + m.paneName(t, p) + "?",
		lines: []string{string(p.a.Agent) + " is working. Closing its pane stops it mid-turn."},
		yes: func(m *Model) tea.Cmd {
			if m.tabIndex(t) < 0 || !slices.Contains(t.panes(), p) {
				return nil
			}
			return m.closePane(t, p)
		},
	}
	return nil
}

// newShell opens a terminal in the active tab's worktree, in a pane of
// its own, last.
func (m *Model) newShell() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	_, cmd := m.newShellIn(t, false)
	return cmd
}

// newShellIn opens a terminal in t's worktree, in a pane of its own,
// last; quiet leaves the keyboard where it is. It returns the pane, nil
// when the terminal did not start.
func (m *Model) newShellIn(t *tab, quiet bool) (*paneTab, tea.Cmd) {
	if m.reposHold(t) {
		return nil, nil
	}
	n := 1
	for _, p := range t.panes() {
		if p.kind == paneShell {
			n = max(n, p.n+1)
		}
	}
	p := &paneTab{kind: paneShell, n: n}
	cmd := m.addPane(t, p, quiet)
	w, h := m.shellSize(t, p)
	env := append(childEnv(t), "NERDR_SHELL=1")
	sh, err := term.Start(m.shellCommand(), t.Dir, env, w, h, m.notify)
	if err != nil {
		m.dropPane(t, p)
		m.toast("terminal: " + err.Error())
		return nil, m.resize()
	}
	p.shell = sh
	m.focusTerms(t)
	return p, cmd
}

// shellCommand is the shell to run: the config's, else $SHELL, else sh.
func (m *Model) shellCommand() []string {
	for _, s := range []string{m.cfg.Shell, os.Getenv("SHELL")} {
		if f := strings.Fields(s); len(f) > 0 {
			return f
		}
	}
	return []string{"/bin/sh"}
}

// checkShells closes the pane of a terminal that has exited, as a
// terminal window closes when its shell does.
func (m *Model) checkShells() {
	for _, t := range m.tabs {
		// A copy: dropPane takes the pane out of the list ranged over.
		for _, p := range slices.Clone(t.panes()) {
			if p.kind != paneShell || p.shell == nil {
				continue
			}
			if exited, _, _ := p.shell.Exited(); exited {
				go p.shell.Close()
				m.dropPane(t, p)
			}
		}
	}
}

// openNewAgent offers the agents on PATH to start beside the active tab's,
// in the same worktree.
func (m *Model) openNewAgent() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	var items []menuItem
	for _, k := range agents.Available(m.cfg.Agents) {
		items = append(items, menuItem{label: string(k), key: strconv.Itoa(len(items) + 1),
			run: func(m *Model) tea.Cmd { return m.newAgent(t, k) }})
	}
	if len(items) == 0 {
		m.toast("no agent on PATH")
		return nil
	}
	items = append([]menuItem{{label: "A new conversation in " + filepath.Base(t.Dir) + ", beside " + string(t.Agent)}}, items...)
	m.dialog = &menu{title: "New agent in " + t.Name, items: items, x: m.width / 3, y: m.height / 3, hi: 1}
	return nil
}

// newAgent starts kind k in a new conversation in t's worktree, in a pane
// of its own, last.
func (m *Model) newAgent(t *tab, k agents.Kind) tea.Cmd {
	_, cmd := m.newAgentWith(t, k, "", "", false, false)
	return cmd
}

// newAgentWith is newAgent, the agent started with model in place of the
// config's, if any, and given task as its first message, if any; quiet
// leaves the keyboard where it is, and sandboxed marks task as from an
// agent in a sandbox. It returns the agent, nil when t is gone.
func (m *Model) newAgentWith(t *tab, k agents.Kind, model, task string, quiet, sandboxed bool) (*tab, tea.Cmd) {
	if m.tabIndex(t) < 0 || m.reposHold(t) {
		return nil, nil
	}
	a := &tab{Tab: state.Tab{ID: newTabID(), Name: t.Name, Dir: t.Dir, Agent: k, Model: model, Worktrees: t.Worktrees, Num: t.nextNum()},
		status: stStarting, partOf: t}
	p := &paneTab{kind: paneAgent, a: a}
	cmd := m.addPane(t, p, quiet)
	if task != "" {
		m.addMessageFrom(a, task, true, sandboxed) // before the start, which may take it
	}
	start := m.start(a, false)
	if a.pane == nil && a.err != "" {
		m.toast(t.Name + ": " + a.err)
	}
	m.saveSession()
	m.focusTerms(t)
	return a, tea.Batch(cmd, start)
}

// restoreAgents puts back the agents t had beside its own when nerdr was
// last running, to be resumed.
func (m *Model) restoreAgents(t *tab) {
	for _, st := range t.Agents {
		a := &tab{Tab: st, status: stStarting, partOf: t}
		if !t.holds(a.Dir) {
			// Kept from before an agent could run in a folder of the tab
			// other than its own: one carrying on a conversation had in
			// one of its repositories, from the search.
			a.Dir = t.Dir
		}
		a.Name, a.Worktrees = t.Name, t.Worktrees
		a.TakeQueue()
		t.paneList = append(t.panes(), &paneTab{kind: paneAgent, a: a, used: time.Now()})
		if a.Num == 0 || t.agentNumbered(a.Num) != a {
			a.Num = 0
			a.Num = t.nextNum()
		}
	}
	t.Agents = nil
}

// holds reports whether dir is t's folder, or in it or one of its
// worktrees.
func (t *tab) holds(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	paths := []string{t.Dir}
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	for _, p := range paths {
		p = filepath.Clean(p)
		if dir == p || strings.HasPrefix(dir, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// paneName is what p is called in its frame and in the row.
func (m *Model) paneName(t *tab, p *paneTab) string {
	switch p.kind {
	case paneShell:
		name := "terminal"
		if p.n > 1 {
			name += " " + strconv.Itoa(p.n)
		}
		if p.shell != nil {
			if title := shellTitle(p.shell.Title(), t.Dir); title != "" {
				name += ": " + ansi.Truncate(title, 24, "…")
			}
		}
		return name
	case paneReview:
		if p.a == nil {
			return "review of " + m.agentLabel(t, p.of) + "…"
		}
		return string(p.a.Agent) + " reviews " + m.agentLabel(t, p.of)
	}
	return m.agentLabel(t, p.a)
}

// shellTitle is what is worth showing of the title a terminal's program
// set, in a worktree at dir: of a shell's "user@host: ~/dir/sub", the
// directory, "sub" as from the worktree, or none in the worktree itself,
// which the tab is named for; another program's title (vim's, say) as it
// is.
func shellTitle(title, dir string) string {
	title = strings.TrimSpace(title)
	at, path, ok := strings.Cut(title, ":")
	if !ok || !strings.Contains(at, "@") || strings.ContainsAny(at, " \t") {
		return title
	}
	path = strings.TrimSpace(path)
	if rest, ok := strings.CutPrefix(path, "~"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + rest
		}
	}
	if rel, err := filepath.Rel(dir, filepath.Clean(path)); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return ""
		}
		return rel
	}
	return filepath.Base(path)
}

// agentLabel names agent a among t's: its number and kind, "2:codex";
// the kind alone while it is the tab's only agent.
func (m *Model) agentLabel(t *tab, a *tab) string {
	if a == nil {
		return "agent"
	}
	if len(t.extraAgents()) == 0 && a.reviewOf == nil {
		return string(a.Agent)
	}
	return strconv.Itoa(a.Num) + ":" + string(a.Agent)
}

// agentName is how a notification names agent a: its tab's name, and
// which of its agents it is when not the tab's own.
func (m *Model) agentName(a *tab) string {
	o := a.owner()
	if o == a {
		return a.Name
	}
	if p := a.paneOf(); p != nil {
		return o.Name + " · " + m.paneName(o, p)
	}
	return o.Name + " · " + string(a.Agent)
}

// paneStatus is the light p shows: its agent's; a terminal has none.
func (p *paneTab) status() (status, bool) {
	if p.a == nil || p.kind == paneShell {
		return 0, false
	}
	return p.a.status, true
}

// paneRow is the row over t's panes, w wide: the minimised ones, each
// with its light, to be brought back; and the buttons for new panes on
// the right. The panes shown are named in their frames.
func (m *Model) paneRow(t *tab, w int) string {
	bar := lipgloss.NewStyle().Background(tabBarBg)
	_, aw, _ := m.tabArea(t)
	cols := m.paneCols(t, aw)
	var small []*paneTab
	for _, p := range t.panes() {
		if !slices.ContainsFunc(cols, func(c paneCol) bool { return c.p == p }) {
			small = append(small, p)
		}
	}
	var items []barItem
	for _, p := range small {
		bg := tabBarBg
		st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("245"))
		tip := m.paneName(t, p) + " · minimised"
		part := st.Render(" ")
		s, ok := p.status()
		switch {
		case ok:
			part += m.lightTip(s, bg, "") + st.Render(" ")
		case p.kind == paneShell:
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
	if t.paneBar == nil {
		t.paneBar = &barRow{}
	}
	left := m.scrollBar(items, 0, t.paneBar, w-lipgloss.Width(right), "pane", func(m *Model, i int) tea.Cmd {
		if i < len(small) {
			return m.goToPane(t, small[i])
		}
		return nil
	})
	return joinBar(left, right, w, bar)
}

// newPaneButtons are the buttons that open a terminal, an agent or a code
// review in a new pane, last, on background bg.
func (m *Model) newPaneButtons(t *tab, bg lipgloss.Color) string {
	st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("81")).Bold(true)
	sp := lipgloss.NewStyle().Background(bg).Render(" ")
	btn := func(label, keys, tip string, run func(m *Model) tea.Cmd) string {
		return m.tip(m.onClick(st.Render("+"+label), func(m *Model) tea.Cmd {
			if m.tabIndex(t) >= 0 && t != m.cur() {
				m.selectTab(m.tabIndex(t))
			}
			return run(m)
		}), tip+" · "+m.pk(keys))
	}
	return btn("terminal", "t t", "A terminal in the worktree, in a new pane", (*Model).newShell) + sp +
		btn("agent", "t a", "Another agent in the same worktree, in a new conversation", (*Model).openNewAgent) + sp +
		btn("review", "t r", "A second agent reviews the work of the agent whose pane has the keyboard", (*Model).newReviewPane) + sp
}

// goToPane goes to tab t and gives its pane p the keyboard.
func (m *Model) goToPane(t *tab, p *paneTab) tea.Cmd {
	var cmd tea.Cmd
	if i := m.tabIndex(t); i >= 0 && t != m.cur() {
		cmd = m.selectTab(i)
	}
	return tea.Batch(cmd, m.focusPane(t, p))
}

// goToAgent goes to agent a, a tab's own or another in one of its panes:
// its tab, with its pane given the keyboard.
func (m *Model) goToAgent(a *tab) tea.Cmd {
	o := a.owner()
	p := a.paneOf()
	if p == nil {
		return m.selectTab(m.tabIndex(o))
	}
	return m.goToPane(o, p)
}

// paneMouse handles the mouse on pane p in t's row: a click goes to it, a
// middle click closes it, a right click shows what can be done to it.
func (m *Model) paneMouse(t *tab, p *paneTab, ev tea.MouseMsg) tea.Cmd {
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
func (m *Model) openPaneMenu(t *tab, p *paneTab, x, y int) {
	small := "Minimise"
	if p.small {
		small = "Show"
	}
	items := []menuItem{
		{label: m.paneName(t, p)},
		{label: small, key: "t m", run: func(m *Model) tea.Cmd { return m.minimisePane(t, p) }},
		{label: "Close", key: "t x", run: func(m *Model) tea.Cmd { return m.askClosePane() }},
		{label: "New panes"},
		{label: "Terminal", key: "t t", run: func(m *Model) tea.Cmd { return m.newShell() }},
		{label: "Agent…", key: "t a", run: func(m *Model) tea.Cmd { return m.openNewAgent() }},
		{label: "Code review", key: "t r", run: func(m *Model) tea.Cmd { return m.newReviewPane() }, off: m.commandOff("t r")},
	}
	if p == t.ownPane() {
		items[2].off = "the tab's first agent stays as long as the tab"
	}
	m.dialog = &menu{title: "Pane", items: items, x: x, y: y, hi: 1}
}

// paneEnd is the buttons at the right of pane p's frame: minimise and
// close, but on a tab's only pane; and, while there is no row over the
// panes to have them, those for new panes on the last pane shown.
func (m *Model) paneEnd(t *tab, p *paneTab) string {
	if !m.mouseOn() {
		return ""
	}
	if len(t.panes()) == 1 {
		return m.newPaneButtons(t, "")
	}
	out := ""
	if _, w, _ := m.tabArea(t); !m.rowShown(t) {
		if cols := m.paneCols(t, w); cols[len(cols)-1].p == p {
			out = m.newPaneButtons(t, "") + " "
		}
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	out += m.tip(m.onClick(st.Render("−"), func(m *Model) tea.Cmd { return m.minimisePane(t, p) }), "Minimise · "+m.pk("t m"))
	if p != t.ownPane() {
		out += " " + m.tip(m.onClick(st.Render("×"), func(m *Model) tea.Cmd {
			cmd := m.goToPane(t, p)
			return tea.Batch(cmd, m.askClosePane())
		}), "Close · "+m.pk("t x"))
	}
	return out
}

// tabLines draws tab t into a w×h area: the row of its minimised panes
// when it has any, and the panes shown, each in its frame.
func (m *Model) tabLines(t *tab, w, h int) []string {
	if t == nil {
		return framed(m.paneLines(nil, nil, w-2, h-2), w, h, "", m.focus == focusAgent)
	}
	var out []string
	if m.rowShown(t) {
		out = append(out, m.paneRow(t, w))
	}
	ch := h - len(out)
	cols := m.paneCols(t, w)
	var body []string
	for i, c := range cols {
		lines := m.paneColumn(t, c.p, c.w, ch)
		if i == 0 {
			body = lines
			continue
		}
		body = besides(body, " ", lines)
	}
	return append(out, body...)
}

// paneColumn draws t's pane p in its frame, w×h.
func (m *Model) paneColumn(t *tab, p *paneTab, w, h int) []string {
	title := ""
	if one := len(t.panes()) == 1; !one || m.splitShown() {
		title = m.paneName(t, p)
		if s, ok := p.status(); ok {
			title = m.tip(light(s), statusTip(s)) + " " + title
		}
		if m.splitShown() && one {
			title += dim(" · " + t.Name)
		}
	}
	active := m.focus == focusAgent && t == m.cur() && p == t.focused()
	iw, ih := w-2, h-2
	var body []string
	switch p.kind {
	case paneAgent:
		body = m.taskLines(p.a, iw, ih)
		body = append(body, m.paneLines(t, p, iw, max(ih-len(body), 1))...)
	case paneReview:
		body = m.reviewPaneLines(t, p, iw, ih)
	default:
		body = m.paneLines(t, p, iw, ih)
	}
	return framedEnd(body, w, h, title, m.paneEnd(t, p), active)
}

// paneLines draws the program in t's pane p into a w×h area: its agent or
// its terminal. Only the pane with the keyboard shows a cursor and can be
// scrolled back.
func (m *Model) paneLines(t *tab, p *paneTab, w, h int) []string {
	if t == nil {
		msg := []string{"", "  No tabs open.", "", "  Press " + keyHint(m.cfg.Prefix) + " then " + keyHint(m.shownKeys("c")) + " (or enter) to start one,",
			"  or click " + m.onClick(buttonStyle.Render(" + new tab "), (*Model).openWizard) + "."}
		return fitLines(msg, w, h)
	}
	a := p.a
	if p.kind != paneShell && (a == nil || a.pane == nil) {
		msg := []string{""}
		switch {
		case a == nil:
		case a.err != "":
			msg = []string{"", "  " + warn(a.err), "", "  " + keyHint("r") + " / enter: try again   " +
				keyHint(m.cfg.Prefix) + " " + keyHint(m.shownKeys("x")) + ": close the tab"}
		default:
			msg = []string{"", "  Starting " + string(a.Agent) + " in " + a.Dir + " …"}
		}
		return fitLines(msg, w, h)
	}
	tp := p.term()
	if tp == nil {
		return fitLines(nil, w, h)
	}
	keys := t == m.cur() && p == t.focused()
	scrolling := keys && m.scrolling
	scroll := 0
	if scrolling {
		scroll = t.scroll
	}
	lines := fitLines(tp.Render(scroll, keys && m.focus == focusAgent && !m.scrolling && m.dialog == nil), w, h)
	if p.kind != paneShell && a.status == stExited {
		note := " " + string(a.Agent) + " has exited"
		if a.err != "" {
			note += " (" + a.err + ")"
		}
		note += " — enter / r / click: start it again, continuing the conversation"
		if p == t.ownPane() {
			note += "   " + m.pk("x") + ": close tab "
		} else {
			note += "   " + m.pk("t x") + ": close the pane "
		}
		lines[h-1] = m.onClick(lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("231")).
			Render(padTo(ansi.Truncate(note, w, "…"), w)), func(m *Model) tea.Cmd { return m.restart(a) })
	}
	if scrolling {
		n := tp.ScrollbackLen()
		note := " scrollback " + strconv.Itoa(t.scroll) + "/" + strconv.Itoa(n) + " · ↑↓ pgup pgdn g G wheel · q or click here: back "
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
func (m *Model) paneAt(t *tab, x, y int) (p *paneTab, px, py int, inside bool) {
	ax, aw, _ := m.tabArea(t)
	if y < m.rowH(t) {
		return nil, 0, 0, false
	}
	for _, c := range m.paneCols(t, aw) {
		if x >= c.x && x < c.x+c.w {
			p = c.p
		}
	}
	if p == nil {
		return nil, 0, 0, false
	}
	rx, ry, rw, rh := m.paneRect(t, p)
	px, py = x+ax-rx, y+m.barRows()-ry
	return p, px, py, px >= 0 && py >= 0 && px < rw && py < rh
}

// savedAgents are t's agents beside its own, as the session keeps them.
func (t *tab) savedAgents() []state.Tab {
	var out []state.Tab
	for _, a := range t.extraAgents() {
		st := a.Tab
		st.Agents = nil
		out = append(out, st)
	}
	return out
}

// cycleFocus moves the keyboard on: to the next pane of the active tab
// shown, then to the split's other tab or the review panel, and round.
func (m *Model) cycleFocus() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	_, w, _ := m.tabArea(t)
	cols := m.paneCols(t, w)
	if m.focus == focusReview {
		return m.focusPane(t, cols[0].p)
	}
	i := slices.IndexFunc(cols, func(c paneCol) bool { return c.p == t.focused() })
	switch {
	case i+1 < len(cols):
		return m.focusPane(t, cols[i+1].p)
	case m.splitTab() != nil && m.splitShown():
		return m.selectTab(m.tabIndex(m.splitTab()))
	case !m.reviewOpen && len(cols) > 1:
		return m.focusPane(t, cols[0].p)
	}
	m.setFocus(focusReview)
	return m.resize()
}

// onMessage takes a message one agent sends another of its tab with
// `nerdr send`: it goes to the other as a task, typed in when it is idle,
// saying whom it is from and how to answer. The sender is told whether it
// went; with no number, which agents there are.
func (m *Model) onMessage(r hooks.Report) tea.Cmd {
	reply := func(a hooks.Answer) {
		if r.Reply != nil {
			select {
			case r.Reply <- a:
			default:
			}
		}
	}
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	o := from.owner()
	if from.reviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer sends its review with nerdr post-review, to the agent it reviews"})
		return nil
	}
	to := o.agentNumbered(r.To)
	switch {
	case r.To == 0:
		reply(hooks.Answer{Text: "The agents of this tab: " + m.agentList(o, from) + ". nerdr send N MESSAGE sends one a message."})
		return nil
	case to == nil:
		reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %d in this tab; its agents are %s", r.To, m.agentList(o, from))})
		return nil
	case to == from:
		reply(hooks.Answer{Err: fmt.Sprintf("agent %d is you; the others are %s", r.To, m.agentList(o, from))})
		return nil
	case m.confined(r, from) && !m.inSandbox(to):
		reply(hooks.Answer{Err: fmt.Sprintf("you run in a sandbox, and agent %d (%s) does not: "+
			"nerdr does not pass it what you write. Ask the user instead", to.Num, to.Agent)})
		return nil
	}
	text := fmt.Sprintf("Message from agent %d (%s) in this nerdr tab:\n\n%s\n\n"+
		"It has none of your conversation. To answer it, or to send it your work, use the nerdr-send skill: nerdr send %d \"…\"",
		from.Num, from.Agent, r.Text, from.Num)
	m.addMessageFrom(to, text, true, m.confined(r, from))
	to.Messages[len(to.Messages)-1].From = from.Num // for nerdr send --wait
	m.saveSession()
	m.resizePanes()
	m.toast(o.Name + ": " + m.agentLabel(o, from) + " → " + m.agentLabel(o, to) + ": a message, sent when it is idle")
	when := "now"
	if to.status != stIdle && to.status != stDone {
		when = "when it is idle"
	}
	reply(hooks.Answer{Text: fmt.Sprintf("Sent to agent %d (%s): it gets it %s, and answers with nerdr send %d if it has anything to say.",
		to.Num, to.Agent, when, from.Num)})
	return nil
}

// maxAgents is the most agents a tab has beside its own that another
// agent can start: each is a program running, and spends from its
// account.
const maxAgents = 8

// onNewAgent starts another agent in the tab of the one asking (`nerdr
// agent`), in a pane beside it, with the task it wrote. It does not take
// the keyboard: the user may be typing to another agent.
func (m *Model) onNewAgent(r hooks.Report) tea.Cmd {
	reply := func(a hooks.Answer) {
		if r.Reply != nil {
			select {
			case r.Reply <- a:
			default:
			}
		}
	}
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.reviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not start agents; it sends its review with nerdr post-review"})
		return nil
	}
	o := from.owner()
	if n := len(o.extraAgents()); n >= maxAgents {
		reply(hooks.Answer{Err: fmt.Sprintf("the tab has %d agents beside its first, the most nerdr starts for an agent: "+
			"give one of them the task with nerdr send, or ask the user to close one; its agents are %s", n, m.agentList(o, from))})
		return nil
	}
	k := from.Agent
	if r.Kind != "" {
		k = agents.Kind(strings.ToLower(r.Kind))
	}
	avail := agents.Available(m.cfg.Agents)
	if !slices.Contains(avail, k) {
		names := make([]string, len(avail))
		for i, a := range avail {
			names[i] = string(a)
		}
		reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %q here; those there are: %s", k, strings.Join(names, ", "))})
		return nil
	}
	if why := m.startRefusal(k); why != "" && m.confined(r, from) {
		reply(hooks.Answer{Err: why})
		return nil
	}
	if r.Model != "" && !agents.TakesModel(k) {
		reply(hooks.Answer{Err: fmt.Sprintf("nerdr does not know how to give %s a model: leave --model out, and it runs the one the config gives it", k)})
		return nil
	}
	if o.reposBusy {
		reply(hooks.Answer{Err: "the tab's repositories are being changed; try again in a moment"})
		return nil
	}
	num := o.nextNum()
	task := fmt.Sprintf("Task from agent %d (%s) in this nerdr tab, which started you in a pane beside it, as agent %d, "+
		"in the same worktree:\n\n%s\n\n"+
		"You have none of its conversation. When you are done, or need something from it, tell it with the nerdr-send skill: "+
		"nerdr send %d \"…\". It waits for your answer.",
		from.Num, from.Agent, num, r.Text, from.Num)
	a, cmd := m.newAgentWith(o, k, r.Model, task, true, m.confined(r, from))
	if a == nil {
		reply(hooks.Answer{Err: "the tab is gone"})
		return nil
	}
	m.toast(o.Name + ": " + m.agentLabel(o, from) + " started " + m.agentLabel(o, a))
	if a.pane == nil && a.err != "" {
		reply(hooks.Answer{Err: fmt.Sprintf("agent %d (%s) could not start: %s", a.Num, a.Agent, a.err)})
		return cmd
	}
	model := ""
	if a.Model != "" {
		model = ", model " + a.Model
	}
	reply(hooks.Answer{Text: fmt.Sprintf("Started agent %d (%s%s) beside you, with your task as its first message. "+
		"It answers with nerdr send %d when it is done; reach it with nerdr send %d. The tab's agents: %s.",
		a.Num, a.Agent, model, from.Num, a.Num, m.agentList(o, from))})
	return cmd
}

// agentList is t's agents as words, "1:claude (you), 2:codex", you
// being from.
func (m *Model) agentList(t, from *tab) string {
	var out []string
	for _, a := range append([]*tab{t}, t.extraAgents()...) {
		s := strconv.Itoa(a.Num) + ":" + string(a.Agent)
		if a == from {
			s += " (you)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// claimMsg is the conversation found to be agent t's, while pane was its
// process; sure, that the one nerdr gave it is.
type claimMsg struct {
	t    *tab
	id   string
	pid  *term.Pane
	sure bool
}

// noteSaid keeps what nerdr typed in to agent t, the first few things
// since it started, to know its conversation by; see claimSession.
func (t *tab) noteSaid(s string) {
	if strings.TrimSpace(s) != "" && len(t.said) < 8 {
		t.said = append(t.said, s)
	}
}

// claimSession looks, once agent t has finished a turn, for the
// conversation it is in, when nerdr could not give it an id and it has
// not reported one (codex, agy), or gave one it may not have taken
// (opencode): the one in the worktree since it started that is no other
// agent's and has in it what nerdr typed in to it. Known by its id, it is
// the one carried on, never another agent's beside it. While that is not
// certain, it is looked for again after the next turn.
func (m *Model) claimSession(t *tab) tea.Cmd {
	if t.reviewOf != nil || t.pane == nil || t.SessionID != "" && !t.sessionUnsure {
		return nil
	}
	specs, kind, dir, pane := m.cfg.Agents, t.Agent, t.Dir, t.pane
	skip, since, said, given := m.othersSessions(t), t.startedAt.Add(-time.Second), slices.Clone(t.said), t.SessionID
	return func() tea.Msg {
		if given != "" && agents.HasSession(specs, kind, given, dir) {
			return claimMsg{t: t, id: given, pid: pane, sure: true}
		}
		return claimMsg{t: t, id: agents.Claim(specs, kind, dir, skip, since, said), pid: pane}
	}
}

// onClaim keeps the conversation found for an agent, unless it has been
// restarted meanwhile, or another agent has it.
func (m *Model) onClaim(msg claimMsg) {
	t := msg.t
	if !m.live(t) || t.pane != msg.pid || t.SessionID != "" && !t.sessionUnsure {
		return
	}
	switch {
	case msg.sure && msg.id == t.SessionID:
		t.sessionUnsure = false
	case msg.id != "" && !msg.sure && !m.othersSessions(t)[msg.id]:
		t.sessionUnsure = false
		m.setSession(t, msg.id)
	}
}
