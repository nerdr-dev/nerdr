package app

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The bottom bar is a row along the bottom of the screen (bottom_bar, the
// prefix and l g), in three parts, each entry a button for its screen:
//
//   - on the left, what wants a look in all the tabs: failing CI, news of
//     your pull requests and issues, tabs waiting for you, tabs merged;
//   - in the middle, the active tab's git: its branch, how far it is ahead
//     of and behind its base, the files not committed, the commits not
//     pushed, the lines changed, its pull request and CI;
//   - on the right, the tab's tasks (F3) and the saved prompts.
//
// While it shows, the news badges, the overlap warning and the active
// tab's details are its alone, not the top bar's as well.

// footRows is the rows the bottom bar takes: none when it is off, or the
// window is too low to spare one.
func (m *Model) footRows() int {
	if m.bottomBar && m.editor == nil && m.height >= 12 {
		return 1
	}
	return 0
}

// bodyH is the rows between the bars.
func (m *Model) bodyH() int { return m.height - m.barRows() - m.footRows() }

// topWarning is the overlap warning for the top bar: the bottom bar's
// while it shows.
func (m *Model) topWarning() string {
	if m.footRows() > 0 {
		return ""
	}
	return m.overlapWarning()
}

var footSep = lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("238")).Render(" │ ")

// bottomBarRow draws the bottom bar. When it is too narrow for all three
// parts, the left one goes first, then the middle is cut.
func (m *Model) bottomBarRow() string {
	bar := lipgloss.NewStyle().Background(tabBarBg)
	all, tab, right := m.footAll(), m.footTab(), m.footRight()
	left := tab
	if all != "" {
		if tab == "" {
			left = all
		} else if lipgloss.Width(all)+lipgloss.Width(footSep)+lipgloss.Width(tab)+lipgloss.Width(right) <= m.width {
			left = all + footSep + tab
		}
	}
	return joinBar(left, right, m.width, bar)
}

// footStyle is the bottom bar's text in colour c.
func footStyle(c string) lipgloss.Style {
	return lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color(c))
}

// footAll is the bottom bar's left part: what wants a look in all the
// tabs, "" when nothing does.
func (m *Model) footAll() string {
	var failing, merged []*core.Tab
	for _, t := range m.fleet.Tabs() {
		switch {
		case t.Info.Merged():
			merged = append(merged, t)
		case t.Info.Ci() == "fail":
			failing = append(failing, t)
		}
	}
	s := " "
	if n := len(failing); n > 0 {
		s += m.tip(m.onClick(footStyle("196").Bold(true).Render("✗ "+strconv.Itoa(n)+" CI")+footStyle("").Render(" "), func(m *Model) tea.Cmd {
			return m.footTabMenu("Failing CI checks", failing, false)
		}), core.Plural(n, "tab")+" with failing CI checks · click: go to one")
	}
	s += m.prBadge() + m.issueBadge() + m.waitingBadge()
	if n := len(merged); n > 0 {
		s += m.tip(m.onClick(footStyle("141").Render(" ✓ "+strconv.Itoa(n)+" merged "), func(m *Model) tea.Cmd {
			return m.footTabMenu("Merged: can be closed", merged, true)
		}), core.Plural(n, "tab")+" with all its work merged · click: close one")
	}
	if s == " " {
		return ""
	}
	return s
}

// footTabMenu lists tabs, above the bottom bar: picked, one is gone to, or
// with closing asked to be closed.
func (m *Model) footTabMenu(title string, tabs []*core.Tab, closing bool) tea.Cmd {
	var items []menuItem
	for _, t := range tabs {
		label := t.Name + " · " + m.where(t)
		run := func(m *Model) tea.Cmd { return m.selectTab(m.fleet.TabIndex(t)) }
		if closing {
			label = "Close " + label + "…"
			run = func(m *Model) tea.Cmd {
				return tea.Batch(m.selectTab(m.fleet.TabIndex(t)), m.openCloseFor(t))
			}
		}
		items = append(items, menuItem{label: label, run: run})
	}
	// The title row and the border's two above the bottom bar.
	y := max(m.height-m.footRows()-len(items)-3, m.barRows())
	m.dialog = &menu{title: title, items: items, x: 1, y: y}
	return nil
}

// footTab is the bottom bar's middle: the active tab's git, "" with no
// tab, or one in no repository.
func (m *Model) footTab() string {
	t := m.Cur()
	if t == nil || m.global.shown {
		return ""
	}
	in := t.Info
	branch := ""
	if len(in.Wts) > 0 {
		branch = in.Wts[0].Branch
	} else if len(t.Worktrees) > 0 {
		branch = t.Worktrees[0].Branch
	}
	if branch == "" && !in.Known() {
		return ""
	}
	f1 := func(m *Model) tea.Cmd { return m.screenPressed(tea.KeyMsg{Type: tea.KeyF1}) }
	sp := footStyle("").Render(" ")
	if branch == "" {
		branch = "(detached)"
	}
	label := " " + branch
	tip := "Branch " + branch
	if n := len(t.Worktrees) - 1; n > 0 {
		label += " +" + strconv.Itoa(n)
		// Before git has been asked, Wts is empty: the tab's own names.
		var names []string
		for _, w := range t.Worktrees[1:] {
			names = append(names, w.Branch)
		}
		if len(in.Wts) == len(t.Worktrees) {
			names = names[:0]
			for _, w := range in.Wts[1:] {
				names = append(names, w.Branch)
			}
		}
		tip += " and " + core.Plural(n, "more worktree")
		if len(names) > 0 {
			tip += ": " + strings.Join(names, ", ")
		}
	}
	parts := []string{m.tip(m.onClick(footStyle("252").Bold(true).Render(label), f1), tip+" · click: the code changes")}
	if !in.Known() {
		parts = append(parts, footStyle("241").Render("looking…"))
		return " " + strings.Join(parts, sp) + " "
	}
	add, del, ahead, behind := in.Sums()
	if ahead > 0 || behind > 0 {
		var s string
		if ahead > 0 {
			s += footStyle("110").Render("↑" + strconv.Itoa(ahead))
		}
		if behind > 0 {
			if s != "" {
				s += sp
			}
			s += footStyle("179").Render("↓" + strconv.Itoa(behind))
		}
		parts = append(parts, m.tip(m.onClick(s, f1),
			core.Plural(ahead, "commit")+" ahead of the base branch, "+strconv.Itoa(behind)+" behind · click: the code changes"))
	}
	var uncommitted, unpushed int
	for _, w := range in.Wts {
		uncommitted += w.Uncommitted
		if w.Err == "" {
			unpushed += w.Unpushed
		}
	}
	if uncommitted > 0 {
		parts = append(parts, m.tip(m.onClick(footStyle("214").Render("●"+strconv.Itoa(uncommitted)), func(m *Model) tea.Cmd {
			return m.openCommit(t)
		}), core.Plural(uncommitted, "file")+" not committed · click: commit"))
	}
	if unpushed > 0 {
		parts = append(parts, m.tip(m.onClick(footStyle("81").Render("⇡"+strconv.Itoa(unpushed)), (*Model).showBranchPR),
			core.Plural(unpushed, "commit")+" not pushed · click: the branch in F2, where p pushes"))
	}
	if add+del > 0 {
		parts = append(parts, m.tip(m.onClick(footStyle("71").Render("+"+strconv.Itoa(add))+footStyle("167").Render("−"+strconv.Itoa(del)), f1),
			"Lines added and removed on the branch · click: the code changes"))
	}
	parts = append(parts, m.footPR(t))
	if w := m.overlapWarning(); w != "" {
		parts = append(parts, w)
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sp) + " "
}

// footPR is the active tab's pull request in the bottom bar: its number,
// state and CI, a click going to it in F2 and a right click to GitHub;
// with none yet, a button for opening one when there is something to
// open it for.
func (m *Model) footPR(t *core.Tab) string {
	in := t.Info
	if in.Merged() {
		return m.tip(m.onClick(footStyle("141").Render("✓ merged"), (*Model).showBranchPR),
			"The branch is merged · click: its pull request (F2)")
	}
	prs := in.Prs()
	if len(prs) == 0 {
		if !in.CanOpenPR() {
			return ""
		}
		return m.tip(m.onClick(footStyle("244").Render("+ PR"), (*Model).showBranchPR),
			"No pull request yet · click: the branch in F2, where P opens one")
	}
	pr := prs[0]
	s := footStyle("110").Render(pr.Ref() + " " + strings.ToLower(pr.State))
	tip := pr.Kind.PRTitle() + " " + pr.Ref() + ", " + strings.ToLower(pr.State)
	if pr.After > 0 {
		s += footStyle("179").Render(" +" + strconv.Itoa(pr.After) + " after")
		tip += " · " + core.Plural(pr.After, "commit") + " after it, in no " + pr.Kind.PRWord()
	}
	switch in.Ci() {
	case "pass":
		s += footStyle("71").Render(" ✓")
		tip += " · checks passed"
	case "fail":
		s += footStyle("196").Render(" ✗")
		tip += " · checks failed"
	case "pending":
		s += footStyle("220").Render(" …")
		tip += " · checks running"
	}
	if n := len(prs) - 1; n > 0 {
		s += footStyle("244").Render(" +" + strconv.Itoa(n))
		tip += " · and " + strconv.Itoa(n) + " more"
	}
	return m.tip(m.zone(s, func(m *Model, ev tea.MouseMsg) tea.Cmd {
		if ev.Action != tea.MouseActionPress {
			return nil
		}
		switch ev.Button {
		case tea.MouseButtonLeft:
			return m.showBranchPR()
		case tea.MouseButtonRight:
			m.openTabPRs()
		}
		return nil
	}), tip+" · click: in F2 · right-click: in the browser")
}

// footRight is the bottom bar's right part: the tab's tasks and the
// saved prompts.
func (m *Model) footRight() string {
	t := m.Cur()
	var s string
	if t != nil && !m.global.shown {
		n := len(t.TaskRefs())
		label, tip := " ✉ tasks ", "The tab's tasks for its agents (F3) · click: show them"
		st := footStyle("244")
		if n > 0 {
			label = " ✉ " + core.Plural(n, "task") + " "
			tip = core.Plural(n, "task") + " waiting to be sent (F3) · click: show them"
			st = footStyle("81")
			if t.QueuePaused {
				label = " ✉ " + core.Plural(n, "task") + " ⏸ "
				tip += " · paused: the agent did not take the last one up"
			}
		}
		s += m.tip(m.onClick(st.Render(label), (*Model).openMessages), tip)
	}
	s += m.tip(m.onClick(footStyle("244").Render(" ★ prompts "), (*Model).openSavedPrompts),
		"Saved prompts, to type into the agent · click: show them")
	return s
}

// openSavedPrompts shows F3's saved prompts, with the keyboard.
func (m *Model) openSavedPrompts() tea.Cmd {
	cmd := m.openMessages()
	m.showSaved(true)
	return cmd
}
