package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"

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

// track notices the active tab changing: the one left is recorded as it
// is, the one come to is summed up. It runs before the new tab is marked
// seen, so a finished agent is still seen as finished.
func (m *Model) track() tea.Cmd {
	t := m.Cur()
	if t == nil || t == m.entered {
		return nil
	}
	old := m.entered
	m.entered = t
	return m.do(core.SwitchTab{Left: m.Core.Ref(old), To: m.Core.Ref(t), Beside: m.Core.Ref(m.splitTab())})
}

// openSummary shows the active tab's last summary again.
func (m *Model) openSummary() tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	if t.Summary == nil {
		m.toast("nothing to sum up yet: " + t.Name + " has not been left and come back to")
		return nil
	}
	m.dialog = &summaryDialog{s: *t.Summary}
	return nil
}

// summaryDialog shows a summary. Any key closes it.
type summaryDialog struct{ s core.Summary }

func (d *summaryDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	m.dialog = nil
	return nil
}

// view shows what happened while you were away, as it stood when you
// came back, and the agent as it is now: the dialog can be opened again
// with H long after, when "still working" would no longer be so.
func (d *summaryDialog) view(m *Model, w, h int) string {
	s := d.s
	agent := string(s.T.Agent)
	green := lipgloss.NewStyle().Foreground(lightColours[core.StDone])
	back := s.Since.Add(s.Away)
	lines := []string{dim("Away " + core.Duration(s.Away) + ", from " + s.Since.Format("15:04") + " to " + back.Format("15:04") + ".")}
	add := func(l ...string) { lines = append(lines, l...) }
	add("")
	if r := strings.TrimSpace(s.T.Recap); r != "" && s.T.RecapAt.After(s.Since) {
		add(wrapLines(r, max(min(w-10, 90), 20), 6)...)
		add("")
	}
	switch {
	case s.Finished:
		what := agent + " had finished"
		if s.Log.Done > 1 {
			what += " " + strconv.Itoa(s.Log.Done) + " turns"
		}
		add(light(core.StDone) + " " + green.Render(what+" when you came back"))
	case s.Log.Done > 0:
		add(light(core.StDone) + " " + green.Render(agent+" finished "+core.Plural(s.Log.Done, "turn")))
	}
	if s.Blocked || s.Log.Blocked > 0 {
		add(light(core.StIdle) + " asked for input " + core.Plural(max(s.Log.Blocked, 1), "time") + " meanwhile")
	}
	switch s.T.Light {
	case core.StWorking:
		add(light(core.StWorking) + " working now")
	case core.StBackground:
		add(light(core.StBackground) + " a process it started still runs in the background")
	case core.StBlocked:
		add(light(core.StBlocked) + " " + agent + " needs your input now")
	}
	if s.Log.Sent > 0 {
		add("✉ " + core.Plural(s.Log.Sent, "queued message") + " sent to it")
	}
	if s.NCommits > 0 {
		add("", core.Plural(s.NCommits, "new commit")+":")
		for _, c := range s.Commits {
			add("  · " + ansi.Truncate(c, max(w-16, 20), "…"))
		}
		if more := s.NCommits - len(s.Commits); more > 0 {
			add(dim("  … and " + strconv.Itoa(more) + " more"))
		}
	}
	if s.Known {
		if s.Add != s.WasAdd || s.Del != s.WasDel {
			add("", "Changes against main: "+diffCount(s.Add, s.Del)+dim("  (were "+diffCount(s.WasAdd, s.WasDel)+")"))
		}
		if n := len(s.NewFiles); n > 0 {
			shown := s.NewFiles[:min(n, 4)]
			l := "Newly changed: " + strings.Join(shown, ", ")
			if n > len(shown) {
				l += dim(" and " + strconv.Itoa(n-len(shown)) + " more")
			}
			add(ansi.Truncate(l, max(w-10, 20), "…"))
		}
		if s.Pr != s.WasPR {
			add("Pull request: " + core.OrNone(s.WasPR) + " → " + core.OrNone(s.Pr))
		}
		if s.Ci != s.WasCI {
			add("CI: " + ciLabel(s.WasCI) + " → " + ciLabel(s.Ci))
		}
	}
	if len(lines) == 2 {
		add(dim("Nothing happened while you were away."))
	}
	add("", m.keys("enter", "close", "", m.pk("h")+" shows it again"))
	return box("Since you were here · "+s.T.Name, lines, w)
}

func diffCount(add, del int) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Render("+"+strconv.Itoa(add)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render("−"+strconv.Itoa(del))
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

// noteDialog writes your note for a tab: where you were, what is next.
type noteDialog struct {
	t     *core.Tab
	input textarea.Model
}

// openNote opens the note for t.
func (m *Model) openNote(t *core.Tab) tea.Cmd {
	if t == nil || m.Core.TabIndex(t) < 0 {
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
		return m.do(core.NoteTab{T: m.Core.Ref(d.t), Note: d.input.Value()})
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (d *noteDialog) view(m *Model, w, h int) string {
	lines := []string{"A note to yourself for when you come back: where you were, what is next.",
		dim("It is shown above the agent, under the tab's task."),
		"", d.input.View(), "",
		m.keys("enter", "save", "", core.NewLineHint, "esc", "cancel")}
	return box("Note · "+d.t.Name, lines, w)
}

// The inbox: the tabs waiting for you.

// nextWaiting goes to the agent that has waited longest for you: its tab,
// and its pane.
func (m *Model) nextWaiting() tea.Cmd {
	for _, t := range m.Core.Waiting() {
		if t != m.CurAgent() {
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

func (b *inbox) entries(m *Model) []*core.Tab {
	return append(m.Core.Waiting(), m.Core.ColdTabs()...)
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
			return m.openCloseFor(es[b.cursor].Owner())
		}
	case "esc", "q", "ctrl+c", "w":
		m.dialog = nil
	}
	return nil
}

func (b *inbox) view(m *Model, w, h int) string {
	now := time.Now()
	waiting, cold := m.Core.Waiting(), m.Core.ColdTabs()
	es := append(append([]*core.Tab{}, waiting...), cold...)
	b.cursor = min(b.cursor, max(len(es)-1, 0))
	inner := min(max(w-10, 40), 100)
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	var lines []string
	row := func(i int, t *core.Tab, what string) {
		line := light(t.AgentLit()) + " " + m.Core.AgentName(t) + dim("  "+t.Owner().GroupName()) + "  " + what
		if m.Core.Cold(t, now) {
			line = lipgloss.NewStyle().Foreground(groupAccent).Render("❄") + " " + t.Name + dim("  "+t.GroupName()) + "  " + what
		}
		lines = append(lines, m.onClick(cursorLine(i == b.cursor, line, sel), func(m *Model) tea.Cmd {
			m.dialog = nil
			return m.goToAgent(t)
		}))
		if _, text := t.GoalText(); text != "" {
			lines = append(lines, dim("    "+ansi.Truncate(strings.Join(strings.Fields(text), " "), inner-4, "…")))
		}
	}
	if len(waiting) == 0 {
		lines = append(lines, dim("Nothing is waiting for you."))
	}
	for i, t := range waiting {
		what := "turn ended"
		if t.Light == core.StBlocked {
			need := "needs input"
			if t.Spinoff != nil {
				need = "wants " + core.Plural(len(t.Spinoff.Next), "new tab")
			}
			what = lipgloss.NewStyle().Foreground(lightColours[core.StBlocked]).Render(need)
		}
		row(i, t, what+dim(" · waiting "+core.Duration(now.Sub(t.Changed))))
	}
	if n := m.Core.CountStatus(core.StWorking); n > 0 {
		lines = append(lines, "", dim(light(core.StWorking)+" "+core.Plural(n, "agent")+" working"))
	}
	if len(cold) > 0 {
		lines = append(lines, "", dim("Gone cold — nothing done in them for "+strconv.Itoa(core.IntOr0(m.Core.Cfg.ColdDays))+"+ days:"))
		for j, t := range cold {
			row(len(waiting)+j, t, dim("last active "+core.Duration(now.Sub(t.LastActive))+" ago"))
		}
	}
	if b.cursor < len(waiting) {
		t := waiting[b.cursor]
		name := m.Core.AgentName(t)
		lines = append(lines, "", dim("── "+name+" "+strings.Repeat("─", max(inner-lipgloss.Width(name)-4, 0))))
		for _, l := range core.ScreenTail(t, 6) {
			lines = append(lines, ansi.Truncate(l, inner, "…"))
		}
	}
	lines = append(lines, "", m.keys("enter", "go to it", "x", "close tab", "esc", "close", "", m.pk("n")+" goes to the next straight away"))
	return box("Agents waiting", lines, w)
}

// waitingBadge is the count of tabs waiting for you, for the top bar: red
// when one of them is asking something. A click opens the inbox.
func (m *Model) waitingBadge() string {
	n, blocked := 0, false
	for _, t := range m.Core.Waiting() {
		if t != m.CurAgent() {
			n++
			blocked = blocked || t.Light == core.StBlocked
		}
	}
	if n == 0 {
		return ""
	}
	s := core.StDone
	if blocked {
		s = core.StBlocked
	}
	return m.tip(m.onClick(lightOn(s, tabBarBg)+lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("250")).
		Render(" "+strconv.Itoa(n)+" waiting "), (*Model).openInbox), "Tabs waiting for you · click: go through them")
}

// Keeping the number of tabs in hand.

// openTabLimit says a new tab has to wait, and offers the tabs that can
// most likely go: those whose work is merged, and those gone cold.
func (m *Model) openTabLimit() {
	now := time.Now()
	items := []menuItem{{label: core.Plural(len(m.Core.Tabs), "tab") + " open: the limit (max_tabs). Close one first."}}
	for _, t := range m.Core.Tabs {
		why := ""
		switch {
		case t.Info.Merged():
			why = "merged"
		case m.Core.Cold(t, now):
			why = "cold, last active " + core.Duration(now.Sub(t.LastActive)) + " ago"
		default:
			continue
		}
		items = append(items, menuItem{label: "Close " + t.Name + " · " + why + "…", run: func(m *Model) tea.Cmd {
			return tea.Batch(m.selectTab(m.Core.TabIndex(t)), m.openCloseFor(t))
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
	var merged []*core.Tab
	for _, t := range m.Core.Tabs {
		if t.Info.Merged() {
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
		{label: "Close " + core.Plural(len(merged), "merged tab") + " and delete their worktrees", key: "y", run: func(m *Model) tea.Cmd {
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
