package app

import (
	"fmt"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
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

// refreshAllIssues asks the repositories the issue list shows for their
// open issues, when it shows all of them: those not asked lately, or all
// of them when force is set.
func (m *Model) refreshAllIssues(force bool) tea.Cmd {
	d := m.issuePanel
	w := m.home().Core.IssueWatch
	if !m.panelIssues() || d == nil || !d.all || w.AllBusy {
		return nil
	}
	var ask []workspace.Repo
	for _, r := range d.scope {
		if got, ok := w.All[r.Path]; force || !ok || time.Since(got.At) >= m.home().Core.GithubEvery(core.IssueAllEvery) {
			ask = append(ask, r)
		}
	}
	if len(ask) == 0 {
		return nil
	}
	return m.do(core.AskAllIssues{Repos: ask})
}

// applyIssues shows what is known in the review panel's issue list.
func (m *Model) applyIssues() {
	if m.issuePanel != nil {
		m.issuePanel.fill(m)
	}
}

// tellIssueNews tells of issues newly assigned to you, which core keeps
// for the marks.
func (m *Model) tellIssueNews(news []state.IssueNews) {
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
	n := len(m.home().Core.IssueWatch.News)
	if n == 0 {
		return ""
	}
	label := fmt.Sprintf(" ◆ %d issue%s ", n, pluralS(n))
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Bold(true).Render(label),
		(*Model).showIssues), "Issues newly assigned to you · click: show them")
}
