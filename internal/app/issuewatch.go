package app

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

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

const (
	// issueWatchEvery is how often GitHub is asked for the issues assigned
	// to you.
	issueWatchEvery = 5 * time.Minute
	// issueAllEvery is how long a repository's open issues, asked for by
	// the list, are taken as they were.
	issueAllEvery = 2 * time.Minute
	// issueListLimit caps how many issues are asked for per repository.
	issueListLimit = 50
)

// issueWatch is what is known of the issues.
type issueWatch struct {
	mine    []issueEntry // assigned to you, in the repositories here
	mineErr string
	known   map[string]bool // every issue assigned to you, by issueKey
	seeded  bool            // known has been learned once: after that, new is news
	news    []state.IssueNews
	busy    bool
	at      time.Time

	// all is each repository's open issues, by its path, asked for when
	// the list shows all issues.
	all     map[string]repoIssues
	allBusy bool
}

// repoIssues is a repository's open issues, or why it did not answer.
type repoIssues struct {
	entries []issueEntry
	err     string
	at      time.Time
}

// issueEntry is one issue, and the repository here it is in.
type issueEntry struct {
	repo  workspace.Repo
	issue github.Issue
}

// issueWatchMsg is a look at the issues assigned to you.
type issueWatchMsg struct {
	keys    []string     // every one, by issueKey, wherever it is
	entries []issueEntry // those in the repositories here
	err     string
}

// issueAllMsg is a look at some repositories' open issues.
type issueAllMsg struct {
	repos map[string]repoIssues
}

func issueKey(repo string, number int) string { return repo + "#" + strconv.Itoa(number) }

// loadIssueWatch takes up what the session kept of the watching.
func (m *Model) loadIssueWatch(s *state.IssueWatch) {
	if s == nil {
		return
	}
	w := &m.issueWatch
	w.known, w.seeded, w.news = map[string]bool{}, true, s.News
	for _, k := range s.Known {
		w.known[k] = true
	}
}

// savedIssueWatch is the watching as the session keeps it.
func (m *Model) savedIssueWatch() *state.IssueWatch {
	w := &m.issueWatch
	if !w.seeded {
		return nil
	}
	s := &state.IssueWatch{News: w.news}
	for k := range w.known {
		s.Known = append(s.Known, k)
	}
	sort.Strings(s.Known)
	return s
}

// issuesShown reports whether the issues are on screen, in the review
// panel.
func (m *Model) issuesShown() bool {
	return m.panelIssues()
}

// tickIssues asks GitHub again when it is time to: while the issues are
// watched or their list is shown.
func (m *Model) tickIssues(now time.Time) tea.Cmd {
	if !isOn(m.cfg.IssueNotify) && !m.issuesShown() {
		return nil
	}
	every := m.githubEvery(issueWatchEvery)
	if m.issuesShown() {
		every = m.githubEvery(issueAllEvery)
	}
	if now.Sub(m.issueWatch.at) < every {
		return nil
	}
	return tea.Batch(m.refreshIssues(), m.refreshAllIssues(false))
}

// refreshIssues asks GitHub for the issues assigned to you, unless it is
// being asked.
func (m *Model) refreshIssues() tea.Cmd {
	w := &m.issueWatch
	if w.busy {
		return nil
	}
	if !github.Available() {
		w.mineErr = "the gh CLI is not installed"
		return nil
	}
	w.busy, w.at = true, time.Now()
	root, wtDir, dirs := m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs
	return func() tea.Msg { return fetchMyIssues(root, wtDir, dirs) }
}

// fetchMyIssues searches for the issues assigned to you, and finds the
// repository here each is in.
func fetchMyIssues(root, wtDir string, dirs []string) issueWatchMsg {
	issues, err := github.MyIssues()
	if err != nil {
		return issueWatchMsg{err: err.Error()}
	}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		return issueWatchMsg{err: err.Error()}
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
	var msg issueWatchMsg
	for _, is := range issues {
		msg.keys = append(msg.keys, issueKey(is.Repo, is.Number))
		if r, ok := byName[is.Repo]; ok {
			msg.entries = append(msg.entries, issueEntry{repo: r, issue: is})
		}
	}
	return msg
}

// onIssueWatch takes a look at the issues assigned to you: the list shows
// it, those newly assigned in the repositories here are told, and news of
// those no longer assigned, or closed, is forgotten. The
// first look only learns which there are. A look that failed keeps what
// was known, so the next one does not take them all for news.
func (m *Model) onIssueWatch(msg issueWatchMsg) {
	w := &m.issueWatch
	w.busy = false
	w.mineErr = msg.err
	if msg.err != "" {
		m.applyIssues()
		return
	}
	var news []state.IssueNews
	if w.seeded {
		for _, e := range msg.entries {
			k := issueKey(e.issue.Repo, e.issue.Number)
			if !w.known[k] {
				news = append(news, state.IssueNews{Key: k, Repo: e.repo.Name, Number: e.issue.Number, Title: e.issue.Title})
			}
		}
	}
	w.known, w.seeded = map[string]bool{}, true
	for _, k := range msg.keys {
		w.known[k] = true
	}
	// News of an issue no longer assigned to you, or closed, no longer
	// holds.
	kept := w.news[:0:0]
	for _, n := range w.news {
		if w.known[n.Key] {
			kept = append(kept, n)
		}
	}
	w.news = kept
	w.mine = msg.entries
	sortIssueEntries(w.mine)
	if len(news) > 0 && isOn(m.cfg.IssueNotify) {
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
	w := &m.issueWatch
	if !m.panelIssues() || d == nil || !d.all || w.allBusy {
		return nil
	}
	var ask []workspace.Repo
	for _, r := range d.scope {
		if got, ok := w.all[r.Path]; force || !ok || time.Since(got.at) >= m.githubEvery(issueAllEvery) {
			ask = append(ask, r)
		}
	}
	if len(ask) == 0 {
		return nil
	}
	w.allBusy = true
	m.applyIssues()
	return func() tea.Msg { return fetchAllIssues(ask) }
}

// fetchAllIssues asks repos for their open issues, a few at once.
func fetchAllIssues(repos []workspace.Repo) issueAllMsg {
	msg := issueAllMsg{repos: map[string]repoIssues{}}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, prFetchers)
	)
	for _, r := range repos {
		wg.Add(1)
		go func(r workspace.Repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got := repoIssues{at: time.Now()}
			issues, err := github.ListIssues(r.Path, issueListLimit)
			if err != nil {
				got.err = r.Name + ": " + err.Error()
			}
			for _, is := range issues {
				got.entries = append(got.entries, issueEntry{repo: r, issue: is})
			}
			mu.Lock()
			msg.repos[r.Path] = got
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	return msg
}

// onIssueAll takes a look at repositories' open issues.
func (m *Model) onIssueAll(msg issueAllMsg) {
	w := &m.issueWatch
	w.allBusy = false
	if w.all == nil {
		w.all = map[string]repoIssues{}
	}
	for path, got := range msg.repos {
		w.all[path] = got
	}
	m.applyIssues()
}

// applyIssues shows what is known in the review panel's issue list.
func (m *Model) applyIssues() {
	if m.issuePanel != nil {
		m.issuePanel.fill(m)
	}
}

// sortIssueEntries groups issues by repository, the most recently updated
// first within each.
func sortIssueEntries(entries []issueEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.repo.Name != b.repo.Name {
			return a.repo.Name < b.repo.Name
		}
		return b.issue.UpdatedAt.Before(a.issue.UpdatedAt)
	})
}

// tellIssueNews tells of issues newly assigned to you, and keeps them for
// the marks.
func (m *Model) tellIssueNews(news []state.IssueNews) {
	m.issueWatch.news = append(m.issueWatch.news, news...)
	n := news[0]
	label := fmt.Sprintf("%s #%d", n.Repo, n.Number)
	what := "assigned to you: " + n.Title
	if len(news) > 1 {
		what += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	m.toastText = label + " " + what + " · " + m.pk("i o")
	m.toastUntil = time.Now().Add(inAppFor)
	if !m.blurred {
		return
	}
	title := "nerdr · " + label
	debugf("issue notify: %s: %s", title, what)
	if m.herdr != nil {
		m.herdr.Notify(title, what, "request")
		return
	}
	go desktopNotify(title, what)
}

// takeIssueNews is the issues newly assigned and not seen yet, and
// forgets them: the list they are shown in is where they are seen.
func (m *Model) takeIssueNews() map[string]bool {
	out := map[string]bool{}
	for _, n := range m.issueWatch.news {
		out[n.Key] = true
	}
	if len(m.issueWatch.news) > 0 {
		m.issueWatch.news = nil
		m.saveSession()
	}
	return out
}

// issueBadge is the mark in the bar while issues newly assigned to you
// are not seen yet; a click shows them.
func (m *Model) issueBadge() string {
	n := len(m.issueWatch.news)
	if n == 0 {
		return ""
	}
	label := fmt.Sprintf(" ◆ %d issue%s ", n, pluralS(n))
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Bold(true).Render(label),
		(*Model).showIssues), "Issues newly assigned to you · click: show them")
}
