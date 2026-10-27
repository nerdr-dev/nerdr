package app

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Working with many agents means switching between them all day, and each
// switch costs the time it takes to remember where that tab had got to.
// This is what nerdr does to make that cheaper:
//
//   - a panel above the agent with the tab's task (the first thing its
//     agent was asked) and your note; see panel.go. What happened while
//     you were away is kept for H. Going to a tab, or leaving one, opens
//     no dialog;
//   - an inbox of the tabs waiting for you, the longest waiting first;
//   - keeping the number of tabs in hand: a limit, a tab whose work is
//     merged saying so, and tabs gone cold marked as such.

// summaryCommits is how many new commits the summary lists by name.
const summaryCommits = 5

// visit is how a tab looked when you last left it.
type visit struct {
	at    time.Time
	heads []string // per worktree; filled in in the background
	known bool     // whether the tab's info was known then
	add   int
	del   int
	files map[string]bool
	ci    string
	pr    string
}

// awayLog counts what a tab's agent did while the tab was not on screen.
type awayLog struct {
	done, blocked, sent int
}

// headsMsg carries the commits a tab's worktrees were at when it was left.
type headsMsg struct {
	t     *tab
	at    time.Time
	heads []string
}

// track notices the active tab changing: the one left is recorded as it
// is, the one come to is summed up. It runs before the new tab is marked
// seen, so a finished agent is still seen as finished.
func (m *Model) track() tea.Cmd {
	t := m.cur()
	if t == nil || t == m.entered {
		return nil
	}
	now := time.Now()
	var cmds []tea.Cmd
	if old := m.entered; old != nil && m.tabIndex(old) >= 0 {
		cmds = append(cmds, m.leave(old, now))
		old.focusVisit = &focusVisit{}
		cmds = append(cmds, captureFocus(old, old.focusVisit, false))
	}
	m.entered = t
	cmds = append(cmds, m.enter(t, now), m.lookUpGoal(t), m.wakeTab(t))
	if s := m.splitTab(); s != nil && s != t {
		cmds = append(cmds, m.wakeTab(s))
	}
	t.unseen = nil
	if t.focusVisit != nil {
		cmds = append(cmds, captureFocus(t, t.focusVisit, true))
	}
	return tea.Batch(cmds...)
}

// leave records how tab t looks as you leave it.
func (m *Model) leave(t *tab, now time.Time) tea.Cmd {
	v := &visit{at: now, known: t.info.known(), files: changedFiles(t.info), ci: t.info.ci(), pr: prLabel(t.info)}
	v.add, v.del, _, _ = t.info.sums()
	t.visit, t.away = v, awayLog{}
	t.LastActive = now
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	return func() tea.Msg {
		heads := make([]string, len(wts))
		for i, w := range wts {
			heads[i] = workspace.Head(w)
		}
		return headsMsg{t: t, at: now, heads: heads}
	}
}

// setHeads files the commits found for a visit, if it is still the last.
func (m *Model) setHeads(msg headsMsg) {
	if v := msg.t.visit; v != nil && v.at.Equal(msg.at) {
		v.heads = msg.heads
	}
}

// enter sums up what happened in t since it was last left; the commits
// are looked up in the background and the summary follows as summaryMsg.
func (m *Model) enter(t *tab, now time.Time) tea.Cmd {
	t.LastActive = now
	v := t.visit
	if v == nil {
		return nil
	}
	t.visit = nil
	s := summary{
		t: t, since: v.at, away: now.Sub(v.at), log: t.away,
		finished: t.status == stDone, blocked: t.status == stBlocked,
		known: v.known && t.info.known(), wasAdd: v.add, wasDel: v.del, wasCI: v.ci, wasPR: v.pr,
		ci: t.info.ci(), pr: prLabel(t.info),
	}
	s.add, s.del, _, _ = t.info.sums()
	for f := range changedFiles(t.info) {
		if !v.files[f] {
			s.newFiles = append(s.newFiles, f)
		}
	}
	sort.Strings(s.newFiles)
	heads := v.heads
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	return func() tea.Msg {
		for i, w := range wts {
			if i < len(heads) {
				subj, n := workspace.CommitsSince(w, heads[i], summaryCommits)
				s.commits = append(s.commits, subj...)
				s.nCommits += n
			}
		}
		return summaryMsg{s}
	}
}

// changedFiles are the files the tab's branches change, as repo/file.
func changedFiles(in tabInfo) map[string]bool {
	out := map[string]bool{}
	for _, w := range in.wts {
		for _, f := range w.stats.Changed {
			out[baseName(w.repo)+"/"+f] = true
		}
	}
	return out
}

// prLabel is the state of the tab's first pull request, e.g. "#12 open".
func prLabel(in tabInfo) string {
	prs := in.prs()
	if len(prs) == 0 {
		return ""
	}
	if in.merged() {
		return "#" + strconv.Itoa(prs[0].Number) + " merged"
	}
	return "#" + strconv.Itoa(prs[0].Number) + " " + prState(prs[0])
}

// summary is what happened in a tab while you were elsewhere.
type summary struct {
	t     *tab
	since time.Time
	away  time.Duration
	log   awayLog
	// finished: the agent was done and waiting to be looked at when you
	// came back; blocked: it asked something then. How it is now is read
	// from the tab, not kept here.
	finished, blocked bool

	commits  []string
	nCommits int

	known          bool // the before and after of the changes are both known
	add, del       int
	wasAdd, wasDel int
	newFiles       []string
	ci, wasCI      string
	pr, wasPR      string
}

type summaryMsg struct{ s summary }

// onSummary keeps the summary for H.
func (m *Model) onSummary(s summary) {
	if m.tabIndex(s.t) < 0 {
		return
	}
	s.t.summary = &s
}

// openSummary shows the active tab's last summary again.
func (m *Model) openSummary() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	if t.summary == nil {
		m.toast("nothing to sum up yet: " + t.Name + " has not been left and come back to")
		return nil
	}
	m.dialog = &summaryDialog{s: *t.summary}
	return nil
}

// summaryDialog shows a summary. Any key closes it.
type summaryDialog struct{ s summary }

func (d *summaryDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	m.dialog = nil
	return nil
}

// view shows what happened while you were away, as it stood when you
// came back, and the agent as it is now: the dialog can be opened again
// with H long after, when "still working" would no longer be so.
func (d *summaryDialog) view(m *Model, w, h int) string {
	s := d.s
	agent := string(s.t.Agent)
	green := lipgloss.NewStyle().Foreground(lightColours[stDone])
	back := s.since.Add(s.away)
	lines := []string{dim("Away " + duration(s.away) + ", from " + s.since.Format("15:04") + " to " + back.Format("15:04") + ".")}
	add := func(l ...string) { lines = append(lines, l...) }
	add("")
	if r := strings.TrimSpace(s.t.Recap); r != "" && s.t.RecapAt.After(s.since) {
		add(wrapLines(r, max(min(w-10, 90), 20), 6)...)
		add("")
	}
	switch {
	case s.finished:
		what := agent + " had finished"
		if s.log.done > 1 {
			what += " " + strconv.Itoa(s.log.done) + " turns"
		}
		add(light(stDone) + " " + green.Render(what+" when you came back"))
	case s.log.done > 0:
		add(light(stDone) + " " + green.Render(agent+" finished "+plural(s.log.done, "turn")))
	}
	if s.blocked || s.log.blocked > 0 {
		add(light(stIdle) + " asked for input " + plural(max(s.log.blocked, 1), "time") + " meanwhile")
	}
	switch s.t.status {
	case stWorking:
		add(light(stWorking) + " working now")
	case stBlocked:
		add(light(stBlocked) + " " + agent + " needs your input now")
	}
	if s.log.sent > 0 {
		add("✉ " + plural(s.log.sent, "queued message") + " sent to it")
	}
	if s.nCommits > 0 {
		add("", plural(s.nCommits, "new commit")+":")
		for _, c := range s.commits {
			add("  · " + ansi.Truncate(c, max(w-16, 20), "…"))
		}
		if more := s.nCommits - len(s.commits); more > 0 {
			add(dim("  … and " + strconv.Itoa(more) + " more"))
		}
	}
	if s.known {
		if s.add != s.wasAdd || s.del != s.wasDel {
			add("", "Changes against main: "+diffCount(s.add, s.del)+dim("  (were "+diffCount(s.wasAdd, s.wasDel)+")"))
		}
		if n := len(s.newFiles); n > 0 {
			shown := s.newFiles[:min(n, 4)]
			l := "Newly changed: " + strings.Join(shown, ", ")
			if n > len(shown) {
				l += dim(" and " + strconv.Itoa(n-len(shown)) + " more")
			}
			add(ansi.Truncate(l, max(w-10, 20), "…"))
		}
		if s.pr != s.wasPR {
			add("Pull request: " + orNone(s.wasPR) + " → " + orNone(s.pr))
		}
		if s.ci != s.wasCI {
			add("CI: " + ciLabel(s.wasCI) + " → " + ciLabel(s.ci))
		}
	}
	if len(lines) == 2 {
		add(dim("Nothing happened while you were away."))
	}
	add("", m.keys("enter", "close", "", m.pk("h")+" shows it again"))
	return box("Since you were here · "+s.t.Name, lines, w)
}

func diffCount(add, del int) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Render("+"+strconv.Itoa(add)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render("−"+strconv.Itoa(del))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func ciLabel(c string) string {
	switch c {
	case "pass":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Render("✓ passing")
	case "fail":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("✗ failing")
	case "pending":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("… running")
	}
	return "none"
}

// duration says how long d is, roughly: "40s", "12m", "3h 5m", "2d 4h".
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h " + strconv.Itoa(int(d.Minutes())%60) + "m"
	}
	return strconv.Itoa(int(d.Hours())/24) + "d " + strconv.Itoa(int(d.Hours())%24) + "h"
}

// goalText is what the tab is about, for the inbox: your note, or else
// its task.
func (t *tab) goalText() (label, text string) {
	if t == nil {
		return "", ""
	}
	if n := strings.TrimSpace(t.Note); n != "" {
		return "note", n
	}
	return "task", strings.TrimSpace(t.Goal)
}

// noteDialog writes your note for a tab: where you were, what is next.
type noteDialog struct {
	t     *tab
	input textarea.Model
}

// openNote opens the note for t.
func (m *Model) openNote(t *tab) tea.Cmd {
	if t == nil || m.tabIndex(t) < 0 {
		return nil
	}
	in := newTextArea("where you were, what is next…", min(max(m.width-16, 30), 80))
	in.SetHeight(3)
	in.SetValue(t.Note)
	m.dialog = &noteDialog{t: t, input: in}
	return nil
}

func (d *noteDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "enter":
		m.dialog = nil
		if m.tabIndex(d.t) >= 0 {
			d.t.Note = strings.TrimSpace(d.input.Value())
			m.saveSession()
			m.resizePanes() // the panel above the agent may have changed height
		}
		return nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (d *noteDialog) view(m *Model, w, h int) string {
	lines := []string{"A note to yourself for when you come back: where you were, what is next.",
		dim("It is shown above the agent, under the tab's task."),
		"", d.input.View(), "",
		m.keys("enter", "save", "", newLineHint, "esc", "cancel")}
	return box("Note · "+d.t.Name, lines, w)
}

// The inbox: the tabs waiting for you.

// waiting are the agents that need you, a tab's own or another in one of
// its panes: those asking something first, then those that have finished,
// each the longest waiting first.
func (m *Model) waiting() []*tab {
	var out []*tab
	for _, o := range m.tabs {
		for _, t := range append([]*tab{o}, o.extraAgents()...) {
			if t.status == stBlocked || t.status == stDone {
				out = append(out, t)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].status == stBlocked, out[j].status == stBlocked; a != b {
			return a
		}
		return out[i].changed.Before(out[j].changed)
	})
	return out
}

// cold reports whether t has gone cold: neither its agent nor you have
// done anything in it for cold_days.
func (m *Model) cold(t *tab, now time.Time) bool {
	days := intOr0(m.cfg.ColdDays)
	if days == 0 || t.LastActive.IsZero() {
		return false
	}
	switch t.status {
	case stWorking, stBlocked, stDone:
		return false
	}
	return now.Sub(t.LastActive) >= time.Duration(days)*24*time.Hour
}

func (m *Model) coldTabs() []*tab {
	now := time.Now()
	var out []*tab
	for _, t := range m.tabs {
		if m.cold(t, now) {
			out = append(out, t)
		}
	}
	return out
}

// nextWaiting goes to the agent that has waited longest for you: its tab,
// and its pane.
func (m *Model) nextWaiting() tea.Cmd {
	for _, t := range m.waiting() {
		if t != m.curAgent() {
			return m.goToAgent(t)
		}
	}
	m.toast("nothing is waiting for you")
	return nil
}

// inbox lists the tabs waiting for you, with what each is waiting with,
// and the tabs gone cold.
type inbox struct{ cursor int }

func (m *Model) openInbox() tea.Cmd {
	m.dialog = &inbox{}
	return nil
}

func (b *inbox) entries(m *Model) []*tab {
	return append(m.waiting(), m.coldTabs()...)
}

func (b *inbox) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	es := b.entries(m)
	b.cursor = min(b.cursor, max(len(es)-1, 0))
	switch msg.String() {
	case "up", "k":
		b.cursor = max(b.cursor-1, 0)
	case "down", "j":
		b.cursor = min(b.cursor+1, max(len(es)-1, 0))
	case "enter", " ":
		if b.cursor < len(es) {
			m.dialog = nil
			return m.goToAgent(es[b.cursor])
		}
	case "x":
		if b.cursor < len(es) {
			m.dialog = nil
			return m.openCloseFor(es[b.cursor].owner())
		}
	case "esc", "q", "ctrl+c", "w":
		m.dialog = nil
	}
	return nil
}

func (b *inbox) view(m *Model, w, h int) string {
	now := time.Now()
	waiting, cold := m.waiting(), m.coldTabs()
	es := append(append([]*tab{}, waiting...), cold...)
	b.cursor = min(b.cursor, max(len(es)-1, 0))
	inner := min(max(w-10, 40), 100)
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	var lines []string
	row := func(i int, t *tab, what string) {
		line := light(t.agentLit()) + " " + m.agentName(t) + dim("  "+t.owner().group()) + "  " + what
		if m.cold(t, now) {
			line = lipgloss.NewStyle().Foreground(groupAccent).Render("❄") + " " + t.Name + dim("  "+t.group()) + "  " + what
		}
		lines = append(lines, m.onClick(cursorLine(i == b.cursor, line, sel), func(m *Model) tea.Cmd {
			m.dialog = nil
			return m.goToAgent(t)
		}))
		if _, text := t.goalText(); text != "" {
			lines = append(lines, dim("    "+ansi.Truncate(strings.Join(strings.Fields(text), " "), inner-4, "…")))
		}
	}
	if len(waiting) == 0 {
		lines = append(lines, dim("Nothing is waiting for you."))
	}
	for i, t := range waiting {
		what := "turn ended"
		if t.status == stBlocked {
			need := "needs input"
			if t.Spinoff != nil {
				need = "wants " + plural(len(t.Spinoff.Next), "new tab")
			}
			what = lipgloss.NewStyle().Foreground(lightColours[stBlocked]).Render(need)
		}
		row(i, t, what+dim(" · waiting "+duration(now.Sub(t.changed))))
	}
	if n := m.countStatus(stWorking); n > 0 {
		lines = append(lines, "", dim(light(stWorking)+" "+plural(n, "agent")+" working"))
	}
	if len(cold) > 0 {
		lines = append(lines, "", dim("Gone cold — nothing done in them for "+strconv.Itoa(intOr0(m.cfg.ColdDays))+"+ days:"))
		for j, t := range cold {
			row(len(waiting)+j, t, dim("last active "+duration(now.Sub(t.LastActive))+" ago"))
		}
	}
	if b.cursor < len(waiting) {
		t := waiting[b.cursor]
		name := m.agentName(t)
		lines = append(lines, "", dim("── "+name+" "+strings.Repeat("─", max(inner-lipgloss.Width(name)-4, 0))))
		for _, l := range screenTail(t, 6) {
			lines = append(lines, ansi.Truncate(l, inner, "…"))
		}
	}
	lines = append(lines, "", m.keys("enter", "go to it", "x", "close tab", "esc", "close", "", m.pk("n")+" goes to the next straight away"))
	return box("Agents waiting", lines, w)
}

// countStatus is how many tabs have status s.
func (m *Model) countStatus(s status) int {
	n := 0
	for _, t := range m.tabs {
		if t.lit() == s {
			n++
		}
	}
	return n
}

// screenTail is what the tab's agent last said: its integration's message
// when it gave one, else the last lines on its screen with text.
func screenTail(t *tab, n int) []string {
	if msg := strings.TrimSpace(t.hook.msg); msg != "" {
		return strings.Split(msg, "\n")
	}
	if t.pane == nil {
		return nil
	}
	var out []string
	rows := strings.Split(t.pane.Snapshot().Screen, "\n")
	for i := len(rows) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(rows[i]) != "" {
			out = append([]string{rows[i]}, out...)
		}
	}
	return out
}

// waitingBadge is the count of tabs waiting for you, for the top bar: red
// when one of them is asking something. A click opens the inbox.
func (m *Model) waitingBadge() string {
	n, blocked := 0, false
	for _, t := range m.waiting() {
		if t != m.curAgent() {
			n++
			blocked = blocked || t.status == stBlocked
		}
	}
	if n == 0 {
		return ""
	}
	s := stDone
	if blocked {
		s = stBlocked
	}
	return m.tip(m.onClick(lightOn(s, tabBarBg)+lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("250")).
		Render(" "+strconv.Itoa(n)+" waiting "), (*Model).openInbox), "Tabs waiting for you · click: go through them")
}

// Keeping the number of tabs in hand.

// tabLimitReached reports whether max_tabs are open.
func (m *Model) tabLimitReached() bool {
	n := intOr0(m.cfg.MaxTabs)
	return n > 0 && len(m.tabs) >= n
}

// openTabLimit says a new tab has to wait, and offers the tabs that can
// most likely go: those whose work is merged, and those gone cold.
func (m *Model) openTabLimit() {
	now := time.Now()
	items := []menuItem{{label: plural(len(m.tabs), "tab") + " open: the limit (max_tabs). Close one first."}}
	for _, t := range m.tabs {
		why := ""
		switch {
		case t.info.merged():
			why = "merged"
		case m.cold(t, now):
			why = "cold, last active " + duration(now.Sub(t.LastActive)) + " ago"
		default:
			continue
		}
		items = append(items, menuItem{label: "Close " + t.Name + " · " + why + "…", run: func(m *Model) tea.Cmd {
			return tea.Batch(m.selectTab(m.tabIndex(t)), m.openCloseFor(t))
		}})
	}
	if len(items) == 1 {
		items = append(items, menuItem{label: "Close this tab…", key: "x", run: func(m *Model) tea.Cmd { return m.openClose() }})
	}
	m.dialog = &menu{title: "New tab", items: items, x: m.width / 4, y: m.height / 3, hi: 1}
}

// barsFull reports whether the bars, as last drawn, have no room for
// another tab: the tab row, or the group row, has entries hidden, or the
// tab row has no room for one more.
func (m *Model) barsFull() bool {
	row := m.tabRows[m.curGroup()]
	return m.groupRow.hidden > 0 || (row != nil && (row.hidden > 0 || !row.room))
}

// offerRoom asks, when the bars are full and some tabs' work is merged,
// whether to close those tabs and delete their worktrees before a new tab
// is made; then, either way, does then. It reports whether it asked.
func (m *Model) offerRoom(then func(m *Model) tea.Cmd) bool {
	if m.roomAsked || !m.barsFull() {
		return false
	}
	var merged []*tab
	for _, t := range m.tabs {
		if t.info.merged() {
			merged = append(merged, t)
		}
	}
	if len(merged) == 0 {
		return false
	}
	names := make([]string, len(merged))
	for i, t := range merged {
		names[i] = t.Name
	}
	goOn := func(m *Model) tea.Cmd {
		m.roomAsked = true
		defer func() { m.roomAsked = false }()
		return then(m)
	}
	items := []menuItem{
		{label: "The bar is full. Merged: " + ansi.Truncate(strings.Join(names, ", "), max(m.width/2, 30), "…")},
		{label: "Close " + plural(len(merged), "merged tab") + " and delete their worktrees", key: "y", run: func(m *Model) tea.Cmd {
			var cmds []tea.Cmd
			for _, t := range merged {
				// Not forced: a worktree with uncommitted changes is kept,
				// and git says why.
				cmds = append(cmds, m.closeTab(t, true, false, nil))
			}
			if m.dialog == nil {
				// Closing the last tab opens the new-tab dialog by itself.
				cmds = append(cmds, goOn(m))
			}
			return tea.Batch(cmds...)
		}},
		{label: "Keep them, and make a new tab anyway", key: "n", run: goOn},
	}
	m.dialog = &menu{title: "New tab", items: items, x: m.width / 4, y: m.height / 3, hi: 1}
	return true
}

// isOn and intOr0 read a setting that config.Load always fills in, and a
// Model built without it (in tests) leaves nil: off, and 0.
func isOn(b *bool) bool { return b != nil && *b }

func intOr0(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
