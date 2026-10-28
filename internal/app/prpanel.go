package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The review panel's F2 screen has two views: the worktree's pull request,
// which the panel draws, and all yours — the list P shows — which nerdr
// draws below the menu, as it does the messages. F2 again or tab switches
// between them.

// panelAllPRs reports whether the review panel shows all your PRs.
func (m *Model) panelAllPRs() bool { return m.reviewOpen && m.review.AllPRs() }

// The worktree's pull request has the tab panel's look at the tab's
// branches above it, with its keys: c commits, p pushes and P opens the
// pull request, and once there is one, f, a and A ask the agent about it;
// the PR itself is below them.

// panelBranchPR reports whether the review panel shows the worktree's PR.
func (m *Model) panelBranchPR() bool { return m.reviewOpen && m.review.BranchPR() }

// curBranchPanel is the tab panel's state for the active tab in F2.
func (m *Model) curBranchPanel() *tabPanel {
	t := m.cur()
	if t == nil {
		return nil
	}
	if m.branchPanel == nil || m.branchPanel.t != t {
		m.branchPanel = &tabPanel{t: t}
	}
	return m.branchPanel
}

// branchRows is what F2 shows above the worktree's PR, w wide: the keys
// go on as many rows as they need.
func (m *Model) branchRows(w int) []string {
	p := m.curBranchPanel()
	if p == nil {
		return nil
	}
	// The PR of the worktree the panel shows is drawn below, and a push
	// or a PR that went well is seen there and in the toast: only what
	// went wrong stays here.
	lines := p.branchLines(m, m.review.Worktree())
	if len(p.t.Worktrees) > 1 {
		lines = p.branchList(m, m.review.Worktree())
	}
	if p.failed {
		lines = append(lines, p.outcome()...)
	}
	lines = append(lines, "")
	if p.busy != "" {
		return append(lines, warn(p.busy))
	}
	key := func(k, what string, can bool) string {
		if !can {
			// Nothing for it to do: greyed, and no button.
			return dim(k + " " + what)
		}
		return m.onClick(keyHint(k)+" "+what, func(m *Model) tea.Cmd {
			cmd, done := m.branchKey(k)
			if done {
				return cmd
			}
			return tea.Batch(cmd, m.reviewKey(keyMsg(k)))
		})
	}
	canPush, canOpen := p.t.info.canPush(), p.t.info.canOpenPR()
	keys := []string{key("c", "commit", p.t.info.canCommit()), key("p", "push", canPush), key("P", "push + open PR", canOpen)}
	if m.review.HasPR() {
		// The panel's own keys for the PR below, as all your PRs have them.
		keys = append(keys, key("f", "fix ci", m.review.CanFix()), key("a", "reply to selected", m.review.CanReply()),
			key("A", "reply to all", m.review.CanReplyAll()))
	}
	if n := len(p.t.Worktrees); n > 1 {
		hint := keyHint("←") + " repositories"
		if m.review.OnRepoList() {
			hint = keyHint("↑↓") + " repository · " + keyHint("enter") + " comments"
		}
		keys = append(keys, m.onClick(hint, func(m *Model) tea.Cmd {
			return m.review.ShowPin((m.review.PinIdx() + 1) % n)
		}))
	}
	row := ""
	for _, k := range keys {
		switch {
		case row == "":
			row = k
		case lipgloss.Width(row+"   "+k) > w:
			lines, row = append(lines, row), k
		default:
			row += "   " + k
		}
	}
	return append(lines, row)
}

// branchKey does what key k does to the tab's branches in F2, reporting
// whether k is one of those keys.
func (m *Model) branchKey(k string) (tea.Cmd, bool) {
	p := m.curBranchPanel()
	if p == nil {
		return nil, false
	}
	switch k {
	case "c":
		if p.busy != "" {
			return nil, true
		}
		return m.openCommit(p.t), true
	case "p", "P":
		if p.busy != "" {
			return nil, true
		}
		// Asked first: the panel has the keyboard after ctrl+q i i, and a
		// word meant for the agent may start with either letter. A push and
		// a pull request are seen by others, and not taken back.
		t := p.t
		var branches []string
		for _, w := range t.Worktrees {
			if w.Branch != "" {
				branches = append(branches, w.Branch)
			}
		}
		title := "Push " + t.Name + "?"
		what := "Pushes " + strings.Join(branches, ", ") + " to GitHub."
		if k == "P" {
			title = "Push and open a pull request?"
			what = "Pushes " + strings.Join(branches, ", ") + " and opens a pull request for each branch without one."
		}
		m.dialog = &confirm{
			title:  title,
			lines:  []string{what},
			strict: true,
			yes: func(m *Model) tea.Cmd {
				q := m.curBranchPanel()
				if q == nil || q.t != t || q.busy != "" {
					return nil
				}
				cmd, _ := q.act(k)
				return cmd
			},
		}
		return nil, true
	case "r":
		// The panel reloads the PR too: the key goes on to it.
		p.t.lookAgain(true)
		return m.refreshInfo(), false
	}
	return nil, false
}

// syncPRPanel keeps the list in the panel up to date while it is shown:
// coming to it fills it with what is known, asking GitHub when that is
// old, and starts its news afresh; news that comes while it is shown is
// seen there.
//
// Coming to the worktree's PR has a fresh look taken at the tab's
// branches.
func (m *Model) syncPRPanel() tea.Cmd {
	var look tea.Cmd
	branch := m.panelBranchPR()
	if branch && !m.branchShown {
		if t := m.cur(); t != nil {
			t.lookAgain(false)
			look = m.refreshInfo()
		}
	}
	m.branchShown = branch
	return tea.Batch(look, m.syncAllPRs(), m.syncIssuePanel())
}

// syncAllPRs is syncPRPanel for the list of all your PRs.
func (m *Model) syncAllPRs() tea.Cmd {
	m.review.SetPRNews(len(m.prWatch.news) > 0)
	shown := m.panelAllPRs()
	entered := shown && !m.prPanelShown
	m.prPanelShown = shown
	if !shown {
		return nil
	}
	if m.prPanel == nil {
		m.prPanel = &prList{}
	}
	d := m.prPanel
	if entered {
		d.news = nil
		d.pointAt(m.cur())
	}
	if len(m.prWatch.news) > 0 {
		if d.news == nil {
			d.news = map[string][]string{}
		}
		for k, whats := range m.takePRNews() {
			d.news[k] = append(d.news[k], whats...)
		}
	}
	if !entered {
		return nil
	}
	if d.all {
		m.applyPRs()
		return m.refreshAllPRs(false)
	}
	return m.showPRs()
}

// showAllPRs shows all your PRs in the review panel, with the keyboard:
// where the mark in the bar and ctrl+q i p take you.
func (m *Model) showAllPRs() tea.Cmd {
	m.dialog = nil
	m.reviewOpen = true
	cmd := m.review.ShowPRView(review.ViewMyPRs)
	m.setFocus(focusReview)
	return tea.Batch(cmd, m.syncPRPanel(), m.resize())
}

// allPRsKey handles a key while all your PRs have the keyboard.
func (m *Model) allPRsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		m.setFocus(focusAgent)
		return m.resize()
	case "tab":
		return m.review.NextPRView(1)
	case "shift+tab":
		return m.review.NextPRView(-1)
	case "ctrl+c":
		return nil
	case "enter":
		if d := m.prPanel; d != nil && d.cursor < len(d.entries) {
			return m.openPRInPanel(d.entries[d.cursor])
		}
		return nil
	case "f":
		return m.askAboutPR(true)
	case "a", "A":
		return m.askAboutPR(false)
	}
	return m.prPanel.key(m, msg)
}

// allPRsBody draws all your PRs, w×h, for the panel's frame.
func (m *Model) allPRsBody(w, h int) []string {
	d := m.prPanel
	if d == nil {
		d = &prList{loading: true}
	}
	scope := "all in the repos"
	if d.all {
		scope = "only mine"
	}
	foot := append([]string{""}, m.panelKeys(m.allPRsKey, w, "↑↓", "pick", "enter", "go to it", "m", scope, "t", "title", "f", "fix ci", "a", "reply", "o", "browser", "r", "reload", "tab", "next", "esc", "agent")...)
	lines := append([]string{m.prViewsLine(review.ViewMyPRs), "", m.prScope(d.all), ""}, d.table(m, w, max(h-len(foot)-8-len(d.errs), 3))...)
	return append(fitLines(lines, w, h-len(foot)), foot...)
}

// prScope says what the list of PRs is: whose, and in which repositories —
// those nerdr finds where it was started and in repo_dirs, which with
// everyone's can be many more than one works in.
func (m *Model) prScope(all bool) string {
	whose, n := "Your", len(m.prWatch.repos)
	if all {
		whose, n = "Everyone's", len(m.prAll.repos)
	}
	home, _ := os.UserHomeDir()
	where := []string{tildePath(m.root, home)}
	for _, d := range m.cfg.RepoDirs {
		if !filepath.IsAbs(d) {
			d = filepath.Join(m.root, d)
		}
		where = append(where, tildePath(d, home))
	}
	in := "the repositories"
	switch {
	case n == 1:
		in = "1 repository"
	case n > 1:
		in = strconv.Itoa(n) + " repositories"
	}
	return lipgloss.NewStyle().Bold(true).Render(whose+" open pull requests") +
		dim(" · in "+in+" under "+strings.Join(where, ", "))
}

// prViewsLine is the line of F2's views above the lists nerdr draws there,
// shown the one on: each a button for its view.
func (m *Model) prViewsLine(shown review.PRView) string {
	var out string
	prNews := len(m.prWatch.news) > 0 && shown != review.ViewMyPRs
	issueNews := len(m.issueWatch.news) > 0 && shown != review.ViewIssues
	for i, l := range review.PRViews(shown, prNews, issueNews) {
		v := review.PRView(i)
		if v == shown {
			out += l
			continue
		}
		out += m.onClick(l, func(m *Model) tea.Cmd { return m.review.SetPRView(v) })
	}
	return out + "  " + keyHint("tab") + dim(" next")
}

// panelKeys is a view's keys, each one a button doing what do does with
// its key, on as many rows w wide as they need.
func (m *Model) panelKeys(do func(tea.KeyMsg) tea.Cmd, w int, pairs ...string) []string {
	var rows []string
	row := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		k, what := pairs[i], pairs[i+1]
		key := k
		if k == "↑↓" {
			key = "down"
		}
		b := m.onClick(keyHint(k)+" "+dim(what), func(*Model) tea.Cmd { return do(keyMsg(key)) })
		switch {
		case row == "":
			row = " " + b
		case lipgloss.Width(row+" · "+b) > w:
			rows, row = append(rows, row), " "+b
		default:
			row += dim(" · ") + b
		}
	}
	return append(rows, row)
}

// openPRInPanel is enter on a PR in the panel: to the tab on its branch,
// with the panel on that PR — its reviews, checks and conversation, where
// a and f answer them — and the keyboard. With no tab, as the P list does.
func (m *Model) openPRInPanel(e prEntry) tea.Cmd {
	if m.tabOnBranch(e.repo.Path, e.branch()) == nil {
		return m.goToPR(e)
	}
	cmd := m.goToPR(e)
	// After going to the tab, which brings back the screen it was left on.
	shown := m.review.ShowBranchPR()
	m.setFocus(focusReview)
	return tea.Batch(cmd, shown, m.syncPRPanel(), m.resize())
}

// prAskMsg is a message for the agent of the tab on a PR's branch, made in
// the background: the PR fetched afresh, a build log read.
type prAskMsg struct {
	t    *tab
	key  string
	what string // what the message asks, for the bar
	text string
	err  error
}

// askAboutPR is f and a among all your PRs: the message F2 writes for a
// PR — fix its build, or draft replies to its reviews — written for the
// PR under the cursor and put in the messages of the tab on its branch,
// for you to read and send from its F3. You stay where you are: that
// tab's agent may be busy, and you may be too.
func (m *Model) askAboutPR(fix bool) tea.Cmd {
	d := m.prPanel
	if d == nil || d.cursor >= len(d.entries) {
		return nil
	}
	e := d.entries[d.cursor]
	t := m.tabOnBranch(e.repo.Path, e.branch())
	if t == nil {
		m.toast("no tab works on " + e.branch() + ": enter opens one")
		return nil
	}
	key := prKey(e.repo.Path, e.pr.Number)
	if m.prAsking[key] {
		return nil
	}
	if m.prAsking == nil {
		m.prAsking = map[string]bool{}
	}
	m.prAsking[key] = true
	dir := e.repo.Path
	for _, w := range t.Worktrees {
		if filepath.Clean(w.Repo) == filepath.Clean(e.repo.Path) {
			dir = w.Path
		}
	}
	what := "draft replies to #" + strconv.Itoa(e.pr.Number) + "'s reviews"
	if fix {
		what = "fix #" + strconv.Itoa(e.pr.Number) + "'s build"
		m.toast("reading the build log of #" + strconv.Itoa(e.pr.Number) + "…")
	}
	return func() tea.Msg {
		msg := prAskMsg{t: t, key: key, what: what}
		pr, err := github.FetchPR(dir, e.pr.Number)
		switch {
		case err != nil:
			msg.err = err
		case fix:
			msg.text, msg.err = review.FixMessage(dir, pr)
		default:
			if msg.text = review.ReplyMessage(pr); msg.text == "" {
				msg.err = fmt.Errorf("no reviews or comments with words on #%d", pr.Number)
			}
		}
		return msg
	}
}

// onPRAsk puts a message askAboutPR made in its tab's messages.
func (m *Model) onPRAsk(msg prAskMsg) {
	delete(m.prAsking, msg.key)
	switch {
	case msg.err != nil:
		m.toast(msg.err.Error())
	case m.tabIndex(msg.t) < 0:
		m.toast(msg.t.Name + " was closed")
	default:
		m.addMessage(msg.t, msg.text, false)
		m.toast("in " + msg.t.Name + "'s " + m.screenKey(3) + ": a message to " + msg.what + " — read it there, then send it")
	}
}
