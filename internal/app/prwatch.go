package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
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

// prWatch is what is known of your pull requests.
type prWatch struct {
	entries []prEntry
	errs    map[string]string // by repository path: why it did not answer

	known map[string]state.PRSeen // by prKey
	repos map[string]bool         // the repositories that have answered once
	news  []state.PRNews          // not seen in the list yet, oldest first
	// skip is the repositories with no GitHub remote, and until when they
	// are left alone.
	skip map[string]time.Time

	busy bool
	at   time.Time // when GitHub was last asked
}

// lookAtGonePRs has the tabs whose pull request was open and is no longer,
// merged or closed, asked about at their next look, rather than at their
// own, slower, turn: a tab not on screen asks only every idlePREvery, and
// would say it is merged that much later. It costs no call: the watch
// asked already.
func (m *Model) lookAtGonePRs(before, after []prEntry) {
	open := map[string]bool{}
	for _, e := range after {
		open[core.PrKey(filepath.Clean(e.repo.Path), e.pr.Number)] = true
	}
	gone := map[string]bool{}
	for _, e := range before {
		if k := core.PrKey(filepath.Clean(e.repo.Path), e.pr.Number); !open[k] {
			gone[k] = true
		}
	}
	if len(gone) == 0 {
		return
	}
	for _, t := range m.Tabs {
		for _, wi := range t.Info.Wts {
			if wi.HasPR && gone[core.PrKey(filepath.Clean(wi.Repo), wi.Pr.Number)] {
				t.LookAgain(true)
			}
		}
	}
}

// prWatchMsg is a look at the pull requests.
type prWatchMsg struct {
	prs      []prEntry
	answered map[string]bool   // repository paths
	failed   map[string]string // repository path: "<name>: <why>"
	// notGitHub is the repositories that failed for want of a GitHub
	// remote; skipped those left alone this time.
	notGitHub, skipped map[string]bool
}

// loadPRWatch takes up what the session kept of the watching.
func (m *Model) loadPRWatch(s *state.PRWatch) {
	if s == nil {
		return
	}
	w := &m.prWatch
	w.known, w.news, w.repos = s.Known, s.News, map[string]bool{}
	for _, r := range s.Repos {
		w.repos[r] = true
	}
	m.review.SetPRNews(len(w.news) > 0)
}

// savedPRWatch is the watching as the session keeps it.
func (m *Model) savedPRWatch() *state.PRWatch {
	w := &m.prWatch
	if len(w.known) == 0 && len(w.news) == 0 {
		return nil
	}
	s := &state.PRWatch{Known: w.known, News: w.news}
	for r := range w.repos {
		s.Repos = append(s.Repos, r)
	}
	sort.Strings(s.Repos)
	return s
}

// prsShown reports whether a list of the pull requests is on screen.
func (m *Model) prsShown() bool {
	return m.panelAllPRs()
}

// tickPRs asks GitHub again when it is time to: while the PRs are watched
// or their list is shown.
func (m *Model) tickPRs(now time.Time) tea.Cmd {
	if !core.IsOn(m.Cfg.PRNotify) && !m.prsShown() {
		return nil
	}
	if now.Sub(m.prWatch.at) < m.GithubEvery(core.PrWatchEvery) {
		return nil
	}
	return m.refreshPRs()
}

// refreshPRs asks GitHub for your pull requests, unless it is being asked.
func (m *Model) refreshPRs() tea.Cmd {
	w := &m.prWatch
	if w.busy {
		return nil
	}
	if !github.Available() {
		w.errs = map[string]string{"": "the gh CLI is not installed"}
		m.applyPRs()
		return nil
	}
	now := time.Now()
	skip := map[string]bool{}
	for r, until := range w.skip {
		if now.Before(until) {
			skip[r] = true
		} else {
			delete(w.skip, r)
		}
	}
	w.busy, w.at = true, now
	m.applyPRs()
	root, wtDir, dirs := m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs
	return func() tea.Msg { return fetchPRs(root, wtDir, dirs, skip) }
}

// fetchPRs asks the repositories for their PRs, a few at once: one slow
// or unreachable repository costs its wait once, and many repositories do
// not start a gh each at the same moment.
//
// Not every repository is asked. One search finds the github.com
// repositories you have open PRs in, and a repository whose remotes are
// all on github.com and none of them among those has none: that is known
// without asking it. So the cost goes with the repositories you have PRs
// in, not with how many there are. Without the search, every repository
// is asked, but those in skip.
func fetchPRs(root, wtDir string, dirs []string, skip map[string]bool) prWatchMsg {
	msg := prWatchMsg{answered: map[string]bool{}, failed: map[string]string{},
		notGitHub: map[string]bool{}, skipped: map[string]bool{}}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		msg.failed[""] = err.Error()
		return msg
	}
	withPRs, err := github.ReposWithMyPRs()
	if err != nil {
		core.Debugf("pr search: %v; asking every repository", err)
		withPRs = nil
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, core.PrFetchers)
	)
	for _, r := range repos {
		if withPRs != nil {
			if names, elsewhere, err := core.RepoNames(r.Path); err == nil && !core.AskRepo(names, elsewhere, withPRs) {
				mu.Lock()
				msg.answered[r.Path] = true // none of yours open in it
				mu.Unlock()
				continue
			}
		}
		if skip[r.Path] {
			msg.skipped[r.Path] = true
			continue
		}
		wg.Add(1)
		go func(r workspace.Repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			prs, err := github.ListMyPRs(r.Path, prListLimit)
			if err != nil {
				mu.Lock()
				msg.failed[r.Path] = r.Name + ": " + err.Error()
				msg.notGitHub[r.Path] = github.NotOnGitHub(err)
				mu.Unlock()
				return
			}
			checkouts := map[string]string{}
			if wts, err := git.ListWorktrees(r.Path); err == nil {
				for _, w := range wts {
					if w.Branch != "" {
						checkouts[w.Branch] = w.Path
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			msg.answered[r.Path] = true
			for _, pr := range prs {
				msg.prs = append(msg.prs, prEntry{repo: r, pr: pr, wt: checkouts[localBranch(pr)]})
			}
		}(r)
	}
	wg.Wait()
	return msg
}

// onPRWatch takes a look at the pull requests: the lists show it, and
// what changed is told. A repository that did not answer keeps what was
// known of it, so its answering again is not taken for news.
func (m *Model) onPRWatch(msg prWatchMsg) {
	w := &m.prWatch
	w.busy = false
	if w.repos == nil {
		w.repos = map[string]bool{}
	}
	if w.skip == nil {
		w.skip = map[string]time.Time{}
	}
	if w.errs == nil {
		w.errs = map[string]string{}
	}
	kept := func(repo string) bool { return msg.failed[repo] != "" || msg.skipped[repo] }

	entries := append([]prEntry(nil), msg.prs...)
	for _, e := range w.entries {
		if kept(e.repo.Path) {
			entries = append(entries, e)
		}
	}
	sortPREntries(entries)
	m.lookAtGonePRs(w.entries, entries)
	w.entries = entries

	for r := range w.errs {
		if !kept(r) {
			delete(w.errs, r)
		}
	}
	for r, why := range msg.failed {
		w.errs[r] = why
		if msg.notGitHub[r] {
			w.skip[r] = time.Now().Add(core.NotGitHubFor)
		}
	}

	known := map[string]state.PRSeen{}
	for k, s := range w.known {
		if kept(s.Repo) {
			known[k] = s
		}
	}
	var news []state.PRNews
	for _, e := range msg.prs {
		k := core.PrKey(e.repo.Path, e.pr.Number)
		now := core.SeenOf(e.repo.Path, e.pr)
		if w.repos[e.repo.Path] {
			tabName := ""
			if t := m.tabOnBranch(e.repo.Path, e.branch()); t != nil {
				tabName = t.Name
			}
			for _, c := range core.PrChanges(w.known[k], now) {
				news = append(news, state.PRNews{Key: k, Repo: e.repo.Name, Number: e.pr.Number, What: c.What, Tab: tabName, Checks: c.Checks})
			}
		}
		known[k] = now
	}
	for r := range msg.answered {
		w.repos[r] = true
	}
	w.known = known
	m.dropStalePRNews()
	if len(news) > 0 && core.IsOn(m.Cfg.PRNotify) {
		m.tellPRNews(news)
	}
	m.applyPRs()
	m.SaveSession()
}

// applyPRs shows what is known in the lists on screen.
func (m *Model) applyPRs() {
	var errs []string
	for _, why := range m.prWatch.errs {
		errs = append(errs, why)
	}
	sort.Strings(errs)
	if d := m.prPanel; d != nil && d.all {
		entries, errs := m.allPREntries()
		d.apply(entries, errs)
		d.loading = m.prAll.busy
	} else if d != nil {
		d.apply(m.prWatch.entries, errs)
		d.loading = m.prWatch.busy
	}
}

// tellPRNews tells of news, and keeps it for the marks.
func (m *Model) tellPRNews(news []state.PRNews) {
	m.prWatch.news = append(m.prWatch.news, news...)
	m.review.SetPRNews(true)
	n := news[0]
	text := core.PrNewsLabel(n) + ": " + n.What
	if len(news) > 1 {
		text += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	m.toastText = text + " · " + m.screenKey(2)
	m.toastUntil = time.Now().Add(core.InAppFor)
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

// dropStalePRNews forgets news not seen yet that no longer holds: of a
// pull request no longer open, merged or closed, and of checks that no
// longer fail, passing again or pushed past. A repository that did not
// answer keeps its news, as it keeps what is known of it.
func (m *Model) dropStalePRNews() {
	w := &m.prWatch
	m.keepPRNews(func(n state.PRNews) bool {
		s, open := w.known[n.Key]
		if !open {
			return false
		}
		if len(n.Checks) == 0 {
			return true
		}
		for _, c := range n.Checks {
			if s.Failing[c] {
				return true
			}
		}
		return false
	})
}

// forgetTabPRNews forgets the news not seen yet that names the tab: the
// tab is closed, and its work with it.
func (m *Model) forgetTabPRNews(name string) {
	m.keepPRNews(func(n state.PRNews) bool { return n.Tab != name })
}

// keepPRNews keeps the news not seen yet that keep says to, and the marks
// with it.
func (m *Model) keepPRNews(keep func(state.PRNews) bool) {
	w := &m.prWatch
	var news []state.PRNews
	for _, n := range w.news {
		if keep(n) {
			news = append(news, n)
		}
	}
	if len(news) == len(w.news) {
		return
	}
	w.news = news
	m.review.SetPRNews(len(news) > 0)
}

// takePRNews is the news not seen yet, by pull request, and forgets it:
// the list it is shown in is where it is seen.
func (m *Model) takePRNews() map[string][]string {
	out := map[string][]string{}
	for _, n := range m.prWatch.news {
		out[n.Key] = append(out[n.Key], n.What)
	}
	if len(m.prWatch.news) > 0 {
		m.prWatch.news = nil
		m.SaveSession()
	}
	m.review.SetPRNews(false)
	return out
}

// prBadge is the mark in the bar while there is news of pull requests not
// seen yet; a click shows them in the review panel.
func (m *Model) prBadge() string {
	prs := map[string]bool{}
	for _, n := range m.prWatch.news {
		prs[n.Key] = true
	}
	if len(prs) == 0 {
		return ""
	}
	label := fmt.Sprintf(" ◆ %d PR%s ", len(prs), pluralS(len(prs)))
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Bold(true).Render(label),
		(*Model).showAllPRs), "News of your pull requests you have not seen · click: show them")
}
