package app

import (
	"fmt"
	"sort"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"

	"github.com/charmbracelet/lipgloss"
)

// Your open pull requests, across the repositories nerdr finds, are asked
// for in one place: the P list and the review panel's F2 show them, and
// they are watched, so a check that fails or a review that comes in is not
// left to be found by chance. That is told without taking you from what
// you are doing: in the bar for a moment, with a mark beside it and on F2
// that stays until you look at the list, and on the desktop while nerdr's
// window does not have focus. pr_notify = false turns the telling off, and
// GitHub is then only asked while the list is shown.
//
// What is news is what changed since the last look. How things stood is
// kept in the session, so what happened while nerdr was not running is
// told at the next start; a repository seen for the first time only
// teaches how things stand.

// prsShown reports whether a list of the pull requests is on screen.
func (m *Model) prsShown() bool {
	return m.panelAllPRs()
}

// applyPRs shows what is known in the lists on screen.
func (m *Model) applyPRs() {
	var errs []string
	for _, why := range m.tabOn().Core.PRWatch.Errs {
		errs = append(errs, why)
	}
	sort.Strings(errs)
	if d := m.prPanel; d != nil && d.all {
		entries, errs := m.allPREntries()
		d.apply(entries, errs)
		d.loading = m.tabOn().Core.PRAll.Busy
	} else if d != nil {
		d.apply(m.tabOn().Core.PRWatch.Entries, errs)
		d.loading = m.tabOn().Core.PRWatch.Busy
	}
}

// tellPRNews tells of news, which core keeps for the marks.
func (m *Model) tellPRNews(news []state.PRNews) {
	m.review.SetPRNews(true)
	n := news[0]
	text := core.PrNewsLabel(n) + ": " + n.What
	if len(news) > 1 {
		text += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	m.say(catGitHub, text+" · "+m.screenKey(2), core.InAppFor)
	if !m.blurred {
		return
	}
	title, body := "nerdr · "+core.PrNewsLabel(n), n.What
	if len(news) > 1 {
		body += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	core.Debugf("pr notify: %s: %s", title, body)
	if m.Herdr != nil {
		m.Herdr.Notify(title, body, "request")
		return
	}
	go core.DesktopNotify(title, body)
}

// prBadge is the mark in the bar while there is news of pull requests not
// seen yet; a click shows them in the review panel.
func (m *Model) prBadge() string {
	prs := map[string]bool{}
	for _, n := range m.tabOn().Core.PRWatch.News {
		prs[n.Key] = true
	}
	if len(prs) == 0 {
		return ""
	}
	label := fmt.Sprintf(" ◆ %d PR%s ", len(prs), pluralS(len(prs)))
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Bold(true).Render(label),
		(*Model).showAllPRs), "News of your pull requests you have not seen · click: show them")
}
