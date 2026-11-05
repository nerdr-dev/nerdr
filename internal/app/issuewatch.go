package app

import (
	"fmt"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The open issues assigned to you are asked for in one search, across
// every github.com repository: the I list shows those in the repositories
// nerdr finds, and they are watched, so one newly assigned to you is told
// the way news of a pull request is (see prwatch.go). Other issues, not
// assigned to you, are only asked for while the list shows them: they are
// a backlog to pick from, not something that needs you.
//
// Issues are watched less often than pull requests: one being assigned
// can wait a few minutes; a check failing on a PR under review less so.
// issue_notify = false turns the watching off.

// issuesShown reports whether the issues are on screen, in the review
// panel.
func (m *Model) issuesShown() bool {
	return m.panelIssues()
}

// tickIssues asks GitHub again when it is time to: while the issues are
// watched or their list is shown.
func (m *Model) tickIssues(now time.Time) tea.Cmd {
	if !core.IsOn(m.Cfg.IssueNotify) && !m.issuesShown() {
		return nil
	}
	every := m.GithubEvery(core.IssueWatchEvery)
	if m.issuesShown() {
		every = m.GithubEvery(core.IssueAllEvery)
	}
	if now.Sub(m.IssueWatch.At) < every {
		return nil
	}
	return tea.Batch(m.refreshIssues(), m.refreshAllIssues(false))
}

// refreshIssues asks GitHub for the issues assigned to you, unless it is
// being asked.
func (m *Model) refreshIssues() tea.Cmd {
	w := &m.IssueWatch
	if w.Busy {
		return nil
	}
	if !github.Available() {
		w.MineErr = "the gh CLI is not installed"
		return nil
	}
	w.Busy, w.At = true, time.Now()
	root, wtDir, dirs := m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs
	return func() tea.Msg { return fetchMyIssues(root, wtDir, dirs) }
}

// fetchMyIssues searches for the issues assigned to you, and finds the
// repository here each is in.
func fetchMyIssues(root, wtDir string, dirs []string) core.IssueWatchMsg {
	issues, err := github.MyIssues()
	if err != nil {
		return core.IssueWatchMsg{Err: err.Error()}
	}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		return core.IssueWatchMsg{Err: err.Error()}
	}
	byName := map[string]workspace.Repo{}
	for _, r := range repos {
		names, _, err := repoNames(r.Path)
		if err != nil {
			continue
		}
		for _, n := range names {
			if _, ok := byName[n]; !ok {
				byName[n] = r
			}
		}
	}
	var msg core.IssueWatchMsg
	for _, is := range issues {
		msg.Keys = append(msg.Keys, core.IssueKey(is.Repo, is.Number))
		if r, ok := byName[is.Repo]; ok {
			msg.Entries = append(msg.Entries, core.IssueEntry{Repo: r, Issue: is})
		}
	}
	return msg
}

// onIssueWatch takes a look at the issues assigned to you: the list shows
// it, those newly assigned in the repositories here are told, and news of
// those no longer assigned, or closed, is forgotten. The
// first look only learns which there are. A look that failed keeps what
// was known, so the next one does not take them all for news.
func (m *Model) onIssueWatch(msg core.IssueWatchMsg) {
	w := &m.IssueWatch
	w.Busy = false
	w.MineErr = msg.Err
	if msg.Err != "" {
		m.applyIssues()
		return
	}
	var news []state.IssueNews
	if w.Seeded {
		for _, e := range msg.Entries {
			k := core.IssueKey(e.Issue.Repo, e.Issue.Number)
			if !w.Known[k] {
				news = append(news, state.IssueNews{Key: k, Repo: e.Repo.Name, Number: e.Issue.Number, Title: e.Issue.Title})
			}
		}
	}
	w.Known, w.Seeded = map[string]bool{}, true
	for _, k := range msg.Keys {
		w.Known[k] = true
	}
	// News of an issue no longer assigned to you, or closed, no longer
	// holds.
	kept := w.News[:0:0]
	for _, n := range w.News {
		if w.Known[n.Key] {
			kept = append(kept, n)
		}
	}
	w.News = kept
	w.Mine = msg.Entries
	core.SortIssueEntries(w.Mine)
	if len(news) > 0 && core.IsOn(m.Cfg.IssueNotify) {
		m.tellIssueNews(news)
	}
	m.applyIssues()
	m.saveSession()
}

// refreshAllIssues asks the repositories the issue list shows for their
// open issues, when it shows all of them: those not asked lately, or all
// of them when force is set.
func (m *Model) refreshAllIssues(force bool) tea.Cmd {
	d := m.issuePanel
	w := &m.IssueWatch
	if !m.panelIssues() || d == nil || !d.all || w.AllBusy {
		return nil
	}
	var ask []workspace.Repo
	for _, r := range d.scope {
		if got, ok := w.All[r.Path]; force || !ok || time.Since(got.At) >= m.GithubEvery(core.IssueAllEvery) {
			ask = append(ask, r)
		}
	}
	if len(ask) == 0 {
		return nil
	}
	w.AllBusy = true
	m.applyIssues()
	return func() tea.Msg { return core.FetchAllIssues(ask) }
}

// onIssueAll takes a look at repositories' open issues.
func (m *Model) onIssueAll(msg core.IssueAllMsg) {
	w := &m.IssueWatch
	w.AllBusy = false
	if w.All == nil {
		w.All = map[string]core.RepoIssues{}
	}
	for path, got := range msg.Repos {
		w.All[path] = got
	}
	m.applyIssues()
}

// applyIssues shows what is known in the review panel's issue list.
func (m *Model) applyIssues() {
	if m.issuePanel != nil {
		m.issuePanel.fill(m)
	}
}

// tellIssueNews tells of issues newly assigned to you, and keeps them for
// the marks.
func (m *Model) tellIssueNews(news []state.IssueNews) {
	m.IssueWatch.News = append(m.IssueWatch.News, news...)
	n := news[0]
	label := fmt.Sprintf("%s #%d", n.Repo, n.Number)
	what := "assigned to you: " + n.Title
	if len(news) > 1 {
		what += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	m.toastText = label + " " + what + " · " + m.pk("i o")
	m.toastUntil = time.Now().Add(core.InAppFor)
	if !m.blurred {
		return
	}
	title := "nerdr · " + label
	core.Debugf("issue notify: %s: %s", title, what)
	if m.Herdr != nil {
		m.Herdr.Notify(title, what, "request")
		return
	}
	go core.DesktopNotify(title, what)
}

// issueBadge is the mark in the bar while issues newly assigned to you
// are not seen yet; a click shows them.
func (m *Model) issueBadge() string {
	n := len(m.IssueWatch.News)
	if n == 0 {
		return ""
	}
	label := fmt.Sprintf(" ◆ %d issue%s ", n, pluralS(n))
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Bold(true).Render(label),
		(*Model).showIssues), "Issues newly assigned to you · click: show them")
}
