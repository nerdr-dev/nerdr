package app

import (
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
)

// F2's third view is the issues, where ctrl+q i o goes: drawn by nerdr
// below the menu as all your pull requests are, with its keys.

// panelIssues reports whether the review panel shows the issues.
func (m *Model) panelIssues() bool { return m.reviewOpen && m.review.Issues() }

// syncIssuePanel keeps the issues in the panel up to date while they are
// shown, as syncAllPRs does all your PRs: coming to them fills them with
// what is known, asking GitHub when that is old; issues newly assigned
// are seen there.
func (m *Model) syncIssuePanel() tea.Cmd {
	m.review.SetIssueNews(len(m.issueWatch.news) > 0)
	shown := m.panelIssues()
	entered := shown && !m.issuePanelShown
	m.issuePanelShown = shown
	if !shown {
		return nil
	}
	if m.issuePanel == nil {
		m.issuePanel = &issueList{}
	}
	d := m.issuePanel
	if entered {
		d.news = nil
	}
	if len(m.issueWatch.news) > 0 {
		if d.news == nil {
			d.news = map[string]bool{}
		}
		for k := range m.takeIssueNews() {
			d.news[k] = true
		}
		m.review.SetIssueNews(false)
	}
	if !entered {
		return nil
	}
	d.setScope(m)
	d.fill(m)
	if time.Since(m.issueWatch.at) < m.githubEvery(issueAllEvery) {
		return m.refreshAllIssues(false)
	}
	return tea.Batch(m.refreshIssues(), m.refreshAllIssues(false))
}

// showIssues shows the issues in the review panel, with the keyboard:
// where the mark in the bar and ctrl+q i o take you.
func (m *Model) showIssues() tea.Cmd {
	m.dialog = nil
	m.reviewOpen = true
	cmd := m.review.ShowPRView(review.ViewIssues)
	m.setFocus(focusReview)
	return tea.Batch(cmd, m.syncPRPanel(), m.resize())
}

// issuesKey handles a key while the issues have the keyboard.
func (m *Model) issuesKey(msg tea.KeyMsg) tea.Cmd {
	d := m.issuePanel
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
		if d != nil && d.cursor < len(d.entries) {
			return m.goToIssue(d.entries[d.cursor])
		}
		return nil
	}
	if d == nil {
		return nil
	}
	return d.key(m, msg)
}

// issuesTitle is the title of the issues' frame in the panel.
func (m *Model) issuesTitle() string {
	if m.issuePanel == nil {
		return "issues …"
	}
	return strings.ToLower(m.issuePanel.title()[:1]) + m.issuePanel.title()[1:]
}

// issuesBody draws the issues, w×h, for the panel's frame.
func (m *Model) issuesBody(w, h int) []string {
	d := m.issuePanel
	if d == nil {
		d = &issueList{loading: true}
	}
	allKey, hereKey := "all open", "this tab"
	if d.all {
		allKey = "assigned to me"
	}
	if d.here {
		hereKey = "all repos"
	}
	pairs := []string{"↑↓", "pick", "enter", "start or go to tab", "a", allKey}
	if m.cur() != nil {
		pairs = append(pairs, "t", hereKey)
	}
	pairs = append(pairs, "o", "browser", "r", "reload", "tab", "next", "esc", "agent")
	foot := append([]string{""}, m.panelKeys(m.issuesKey, w, pairs...)...)
	lines := append([]string{m.prViewsLine(review.ViewIssues), ""}, d.table(m, w, max(h-len(foot)-6-len(d.errs), 3))...)
	return append(fitLines(lines, w, h-len(foot)), foot...)
}
