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

// lookAtGonePRs has the tabs whose pull request was open and is no longer,
// merged or closed, asked about at their next look, rather than at their
// own, slower, turn: a tab not on screen asks only every idlePREvery, and
// would say it is merged that much later. It costs no call: the watch
// asked already.
func (m *Model) lookAtGonePRs(before, after []core.PREntry) {
	open := map[string]bool{}
	for _, e := range after {
		open[core.PrKey(filepath.Clean(e.Repo.Path), e.PR.Number)] = true
	}
	gone := map[string]bool{}
	for _, e := range before {
		if k := core.PrKey(filepath.Clean(e.Repo.Path), e.PR.Number); !open[k] {
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

// loadPRWatch takes up what the session kept of the watching.
func (m *Model) loadPRWatch(s *state.PRWatch) {
	if s == nil {
		return
	}
	w := &m.prWatch
	w.Known, w.News, w.Repos = s.Known, s.News, map[string]bool{}
	for _, r := range s.Repos {
		w.Repos[r] = true
	}
	m.review.SetPRNews(len(w.News) > 0)
}

// savedPRWatch is the watching as the session keeps it.
func (m *Model) savedPRWatch() *state.PRWatch {
	w := &m.prWatch
	if len(w.Known) == 0 && len(w.News) == 0 {
		return nil
	}
	s := &state.PRWatch{Known: w.Known, News: w.News}
	for r := range w.Repos {
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
	if now.Sub(m.prWatch.At) < m.GithubEvery(core.PrWatchEvery) {
		return nil
	}
	return m.refreshPRs()
}

// refreshPRs asks GitHub for your pull requests, unless it is being asked.
func (m *Model) refreshPRs() tea.Cmd {
	w := &m.prWatch
	if w.Busy {
		return nil
	}
	if !github.Available() {
		w.Errs = map[string]string{"": "the gh CLI is not installed"}
		m.applyPRs()
		return nil
	}
	now := time.Now()
	skip := map[string]bool{}
	for r, until := range w.Skip {
		if now.Before(until) {
			skip[r] = true
		} else {
			delete(w.Skip, r)
		}
	}
	w.Busy, w.At = true, now
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
func fetchPRs(root, wtDir string, dirs []string, skip map[string]bool) core.PRWatchMsg {
	msg := core.PRWatchMsg{Answered: map[string]bool{}, Failed: map[string]string{},
		NotGitHub: map[string]bool{}, Skipped: map[string]bool{}}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		msg.Failed[""] = err.Error()
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
				msg.Answered[r.Path] = true // none of yours open in it
				mu.Unlock()
				continue
			}
		}
		if skip[r.Path] {
			msg.Skipped[r.Path] = true
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
				msg.Failed[r.Path] = r.Name + ": " + err.Error()
				msg.NotGitHub[r.Path] = github.NotOnGitHub(err)
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
			msg.Answered[r.Path] = true
			for _, pr := range prs {
				msg.PRs = append(msg.PRs, core.PREntry{Repo: r, PR: pr, Wt: checkouts[core.LocalBranch(pr)]})
			}
		}(r)
	}
	wg.Wait()
	return msg
}

// onPRWatch takes a look at the pull requests: the lists show it, and
// what changed is told. A repository that did not answer keeps what was
// known of it, so its answering again is not taken for news.
func (m *Model) onPRWatch(msg core.PRWatchMsg) {
	w := &m.prWatch
	w.Busy = false
	if w.Repos == nil {
		w.Repos = map[string]bool{}
	}
	if w.Skip == nil {
		w.Skip = map[string]time.Time{}
	}
	if w.Errs == nil {
		w.Errs = map[string]string{}
	}
	kept := func(repo string) bool { return msg.Failed[repo] != "" || msg.Skipped[repo] }

	entries := append([]core.PREntry(nil), msg.PRs...)
	for _, e := range w.Entries {
		if kept(e.Repo.Path) {
			entries = append(entries, e)
		}
	}
	core.SortPREntries(entries)
	m.lookAtGonePRs(w.Entries, entries)
	w.Entries = entries

	for r := range w.Errs {
		if !kept(r) {
			delete(w.Errs, r)
		}
	}
	for r, why := range msg.Failed {
		w.Errs[r] = why
		if msg.NotGitHub[r] {
			w.Skip[r] = time.Now().Add(core.NotGitHubFor)
		}
	}

	known := map[string]state.PRSeen{}
	for k, s := range w.Known {
		if kept(s.Repo) {
			known[k] = s
		}
	}
	var news []state.PRNews
	for _, e := range msg.PRs {
		k := core.PrKey(e.Repo.Path, e.PR.Number)
		now := core.SeenOf(e.Repo.Path, e.PR)
		if w.Repos[e.Repo.Path] {
			tabName := ""
			if t := m.tabOnBranch(e.Repo.Path, e.Branch()); t != nil {
				tabName = t.Name
			}
			for _, c := range core.PrChanges(w.Known[k], now) {
				news = append(news, state.PRNews{Key: k, Repo: e.Repo.Name, Number: e.PR.Number, What: c.What, Tab: tabName, Checks: c.Checks})
			}
		}
		known[k] = now
	}
	for r := range msg.Answered {
		w.Repos[r] = true
	}
	w.Known = known
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
	for _, why := range m.prWatch.Errs {
		errs = append(errs, why)
	}
	sort.Strings(errs)
	if d := m.prPanel; d != nil && d.all {
		entries, errs := m.allPREntries()
		d.apply(entries, errs)
		d.loading = m.prAll.busy
	} else if d != nil {
		d.apply(m.prWatch.Entries, errs)
		d.loading = m.prWatch.Busy
	}
}

// tellPRNews tells of news, and keeps it for the marks.
func (m *Model) tellPRNews(news []state.PRNews) {
	m.prWatch.News = append(m.prWatch.News, news...)
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
		s, open := w.Known[n.Key]
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
	for _, n := range w.News {
		if keep(n) {
			news = append(news, n)
		}
	}
	if len(news) == len(w.News) {
		return
	}
	w.News = news
	m.review.SetPRNews(len(news) > 0)
}

// takePRNews is the news not seen yet, by pull request, and forgets it:
// the list it is shown in is where it is seen.
func (m *Model) takePRNews() map[string][]string {
	out := map[string][]string{}
	for _, n := range m.prWatch.News {
		out[n.Key] = append(out[n.Key], n.What)
	}
	if len(m.prWatch.News) > 0 {
		m.prWatch.News = nil
		m.SaveSession()
	}
	m.review.SetPRNews(false)
	return out
}

// prBadge is the mark in the bar while there is news of pull requests not
// seen yet; a click shows them in the review panel.
func (m *Model) prBadge() string {
	prs := map[string]bool{}
	for _, n := range m.prWatch.News {
		prs[n.Key] = true
	}
	if len(prs) == 0 {
		return ""
	}
	label := fmt.Sprintf(" ◆ %d PR%s ", len(prs), pluralS(len(prs)))
	return m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214")).Bold(true).Render(label),
		(*Model).showAllPRs), "News of your pull requests you have not seen · click: show them")
}
