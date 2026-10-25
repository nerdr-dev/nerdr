package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

const (
	// prWatchEvery is how often GitHub is asked.
	prWatchEvery = 2 * time.Minute
	// prFetchers is how many repositories are asked at once: each is a gh
	// process and a request to GitHub.
	prFetchers = 4
	// notGitHubFor is how long a repository with no GitHub remote is left
	// alone before it is asked again.
	notGitHubFor = 30 * time.Minute
)

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
		open[prKey(filepath.Clean(e.repo.Path), e.pr.Number)] = true
	}
	gone := map[string]bool{}
	for _, e := range before {
		if k := prKey(filepath.Clean(e.repo.Path), e.pr.Number); !open[k] {
			gone[k] = true
		}
	}
	if len(gone) == 0 {
		return
	}
	for _, t := range m.tabs {
		for _, wi := range t.info.wts {
			if wi.hasPR && gone[prKey(filepath.Clean(wi.repo), wi.pr.Number)] {
				t.lookAgain(true)
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

func prKey(repo string, number int) string { return repo + "#" + strconv.Itoa(number) }

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
	if !isOn(m.cfg.PRNotify) && !m.prsShown() {
		return nil
	}
	if now.Sub(m.prWatch.at) < m.githubEvery(prWatchEvery) {
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
	root, wtDir, dirs := m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs
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
		debugf("pr search: %v; asking every repository", err)
		withPRs = nil
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, prFetchers)
	)
	for _, r := range repos {
		if withPRs != nil {
			if names, elsewhere, err := repoNames(r.Path); err == nil && !askRepo(names, elsewhere, withPRs) {
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

// askRepo reports whether a repository with remotes on github.com names,
// and elsewhere when it has one anywhere else, has to be asked for its PRs,
// given the repositories the search found PRs of yours in.
func askRepo(names []string, elsewhere bool, withPRs map[string]bool) bool {
	if elsewhere {
		return true // the search covers github.com only
	}
	for _, n := range names {
		if withPRs[n] {
			return true
		}
	}
	return false // on github.com with none of yours open, or no remote at all
}

// remoteCache keeps what a repository's remotes are, for remoteCacheFor:
// they hardly ever change, and fetchPRs would read them for every
// repository at every look.
var remoteCache = struct {
	sync.Mutex
	m map[string]remoteInfo
}{m: map[string]remoteInfo{}}

const remoteCacheFor = 10 * time.Minute

type remoteInfo struct {
	names     []string
	elsewhere bool
	at        time.Time
}

// repoNames is github.RepoNames, kept for a while.
func repoNames(path string) ([]string, bool, error) {
	remoteCache.Lock()
	in, ok := remoteCache.m[path]
	remoteCache.Unlock()
	if ok && time.Since(in.at) < remoteCacheFor {
		return in.names, in.elsewhere, nil
	}
	names, elsewhere, err := github.RepoNames(path)
	if err != nil {
		return nil, false, err
	}
	remoteCache.Lock()
	remoteCache.m[path] = remoteInfo{names: names, elsewhere: elsewhere, at: time.Now()}
	remoteCache.Unlock()
	return names, elsewhere, nil
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
			w.skip[r] = time.Now().Add(notGitHubFor)
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
		k := prKey(e.repo.Path, e.pr.Number)
		now := seenOf(e.repo.Path, e.pr)
		if w.repos[e.repo.Path] {
			tabName := ""
			if t := m.tabOnBranch(e.repo.Path, e.branch()); t != nil {
				tabName = t.Name
			}
			for _, c := range prChanges(w.known[k], now) {
				news = append(news, state.PRNews{Key: k, Repo: e.repo.Name, Number: e.pr.Number, What: c.what, Tab: tabName, Checks: c.checks})
			}
		}
		known[k] = now
	}
	for r := range msg.answered {
		w.repos[r] = true
	}
	w.known = known
	m.dropStalePRNews()
	if len(news) > 0 && isOn(m.cfg.PRNotify) {
		m.tellPRNews(news)
	}
	m.applyPRs()
	m.saveSession()
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

// seenOf is how pr stands. A check counts by the commit it ran on, so the
// same check failing again after a push is news; one cancelled is not
// taken for failed, being most often cancelled on purpose. Reviews by its
// author (a reply to a review comment is one) and drafts not yet sent are
// not counted.
func seenOf(repo string, pr github.PR) state.PRSeen {
	s := state.PRSeen{Repo: repo, Failing: map[string]bool{}, Reviews: map[string]state.ReviewSeen{}}
	for _, c := range pr.Checks {
		if github.Failed(c.Status) && !strings.EqualFold(c.Status, "CANCELLED") {
			s.Failing[pr.Head+"/"+c.Name] = true
		}
	}
	for _, rv := range pr.Reviews {
		switch {
		case rv.Author == pr.Author, rv.SubmittedAt.IsZero():
		case rv.State == "PENDING", rv.State == "DISMISSED":
		default:
			s.Reviews[rv.Author] = state.ReviewSeen{State: rv.State, At: rv.SubmittedAt}
		}
	}
	return s
}

// prChange is one thing that happened to a pull request, and the failing
// checks it is about, if it is about checks.
type prChange struct {
	what   string
	checks []string
}

// prChanges says what happened between two looks at a pull request: the
// checks that have come to fail, and the reviews that have come in.
func prChanges(before, now state.PRSeen) []prChange {
	var out []prChange
	var keys []string
	for key := range now.Failing {
		if !before.Failing[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	switch len(keys) {
	case 0:
	case 1:
		_, name, _ := strings.Cut(keys[0], "/")
		out = append(out, prChange{"check failed: " + name, keys})
	default:
		out = append(out, prChange{fmt.Sprintf("%d checks failed", len(keys)), keys})
	}
	var who []string
	for author, rv := range now.Reviews {
		if old, ok := before.Reviews[author]; !ok || rv.At.After(old.At) {
			who = append(who, author)
		}
	}
	sort.Strings(who)
	for _, author := range who {
		switch now.Reviews[author].State {
		case "APPROVED":
			out = append(out, prChange{what: "approved by " + author})
		case "CHANGES_REQUESTED":
			out = append(out, prChange{what: "changes requested by " + author})
		default:
			out = append(out, prChange{what: "reviewed by " + author})
		}
	}
	return out
}

// tellPRNews tells of news, and keeps it for the marks.
func (m *Model) tellPRNews(news []state.PRNews) {
	m.prWatch.news = append(m.prWatch.news, news...)
	m.review.SetPRNews(true)
	n := news[0]
	text := prNewsLabel(n) + ": " + n.What
	if len(news) > 1 {
		text += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	m.toastText = text + " · " + m.screenKey(2)
	m.toastUntil = time.Now().Add(inAppFor)
	if !m.blurred {
		return
	}
	title, body := "nerdr · "+prNewsLabel(n), n.What
	if len(news) > 1 {
		body += fmt.Sprintf(" (and %d more)", len(news)-1)
	}
	debugf("pr notify: %s: %s", title, body)
	if m.herdr != nil {
		m.herdr.Notify(title, body, "request")
		return
	}
	go desktopNotify(title, body)
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

// prNewsLabel names the PR news is of: by the tab working on its branch,
// which is how you know the work, else by its repository.
func prNewsLabel(n state.PRNews) string {
	if n.Tab != "" {
		return fmt.Sprintf("%s #%d", n.Tab, n.Number)
	}
	return fmt.Sprintf("%s #%d", n.Repo, n.Number)
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
		m.saveSession()
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
