package core

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
)

// PRListLimit caps how many PRs are asked for per repository.
const PRListLimit = 50

const (
	// PrWatchEvery is how often GitHub is asked.
	PrWatchEvery = 2 * time.Minute
	// PrFetchers is how many repositories are asked at once: each is a gh
	// process and a request to GitHub.
	PrFetchers = 4
	// NotGitHubFor is how long a repository with no GitHub remote is left
	// alone before it is asked again.
	NotGitHubFor = 30 * time.Minute
)

func PrKey(repo string, number int) string { return repo + "#" + strconv.Itoa(number) }

// AskRepo reports whether a repository with remotes on github.com names,
// and elsewhere when it has one anywhere else, has to be asked for its PRs,
// given the repositories the search found PRs of yours in.
func AskRepo(names []string, elsewhere bool, withPRs map[string]bool) bool {
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

const RemoteCacheFor = 10 * time.Minute

type RemoteInfo struct {
	Names     []string
	Elsewhere bool
	At        time.Time
}

// SeenOf is how pr stands. A check counts by the commit it ran on, so the
// same check failing again after a push is news; one cancelled is not
// taken for failed, being most often cancelled on purpose. Reviews by its
// author (a reply to a review comment is one) and drafts not yet sent are
// not counted.
func SeenOf(repo string, pr github.PR) state.PRSeen {
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

// PrChange is one thing that happened to a pull request, and the failing
// checks it is about, if it is about checks.
type PrChange struct {
	What   string
	Checks []string
}

// PrChanges says what happened between two looks at a pull request: the
// checks that have come to fail, and the reviews that have come in.
func PrChanges(before, now state.PRSeen) []PrChange {
	var out []PrChange
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
		out = append(out, PrChange{"check failed: " + name, keys})
	default:
		out = append(out, PrChange{fmt.Sprintf("%d checks failed", len(keys)), keys})
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
			out = append(out, PrChange{What: "approved by " + author})
		case "CHANGES_REQUESTED":
			out = append(out, PrChange{What: "changes requested by " + author})
		default:
			out = append(out, PrChange{What: "reviewed by " + author})
		}
	}
	return out
}

// PrNewsLabel names the PR news is of: by the tab working on its branch,
// which is how you know the work, else by its repository.
func PrNewsLabel(n state.PRNews) string {
	if n.Tab != "" {
		return fmt.Sprintf("%s #%d", n.Tab, n.Number)
	}
	return fmt.Sprintf("%s #%d", n.Repo, n.Number)
}

// remoteCache keeps what a repository's remotes are, for RemoteCacheFor:
// they hardly ever change, and the look at the pull requests would read them
// for every repository at every look.
var remoteCache = struct {
	sync.Mutex
	m map[string]RemoteInfo
}{m: map[string]RemoteInfo{}}

// RepoNames is github.RepoNames, kept for a while.
func RepoNames(path string) ([]string, bool, error) {
	remoteCache.Lock()
	in, ok := remoteCache.m[path]
	remoteCache.Unlock()
	if ok && time.Since(in.At) < RemoteCacheFor {
		return in.Names, in.Elsewhere, nil
	}
	names, elsewhere, err := github.RepoNames(path)
	if err != nil {
		return nil, false, err
	}
	remoteCache.Lock()
	remoteCache.m[path] = RemoteInfo{Names: names, Elsewhere: elsewhere, At: time.Now()}
	remoteCache.Unlock()
	return names, elsewhere, nil
}

// PREntry is one pull request, and where its branch is checked out, if
// anywhere.
type PREntry struct {
	Repo workspace.Repo
	PR   github.PR
	Wt   string // the worktree with the branch, "" when there is none
	// Asked is set when your review of it is asked for, on you or on a
	// team of yours.
	Asked bool
}

// Branch is the local branch the pull request is worked on in: its own,
// or, from a fork, the one FetchForkPR fetches it into.
func (e PREntry) Branch() string { return LocalBranch(e.PR) }

// LocalBranch is the local branch pr is worked on in; see PREntry.Branch.
func LocalBranch(pr github.PR) string {
	if pr.Fork != "" {
		return workspace.ForkBranch(pr.Number, pr.Branch)
	}
	return pr.Branch
}

// SortPREntries groups the list by repository, the most recently updated
// first within each.
func SortPREntries(entries []PREntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Repo.Name != b.Repo.Name {
			return a.Repo.Name < b.Repo.Name
		}
		return b.PR.UpdatedAt.Before(a.PR.UpdatedAt)
	})
}

// PRWatchState is what is known of your pull requests.
type PRWatchState struct {
	Entries []PREntry
	Errs    map[string]string // by repository path: why it did not answer

	Known map[string]state.PRSeen // by prKey
	Repos map[string]bool         // the repositories that have answered once
	News  []state.PRNews          // not seen in the list yet, oldest first
	// Skip is the repositories with no GitHub remote, and until when they
	// are left alone.
	Skip map[string]time.Time

	Busy bool
	At   time.Time // when GitHub was last asked
}

// PRWatchMsg is a look at the pull requests.
type PRWatchMsg struct {
	PRs      []PREntry
	Answered map[string]bool   // repository paths
	Failed   map[string]string // repository path: "<name>: <why>"
	// NotGitHub is the repositories that failed for want of a GitHub
	// remote; Skipped those left alone this time.
	NotGitHub, Skipped map[string]bool
}

// LookAtGonePRs has the tabs whose pull request was open and is no longer,
// merged or closed, asked about at their next look, rather than at their
// own, slower, turn: a tab not on screen asks only every idlePREvery, and
// would say it is merged that much later. It costs no call: the watch
// asked already.
func (m *Core) LookAtGonePRs(before, after []PREntry) {
	open := map[string]bool{}
	for _, e := range after {
		open[PrKey(filepath.Clean(e.Repo.Path), e.PR.Number)] = true
	}
	gone := map[string]bool{}
	for _, e := range before {
		if k := PrKey(filepath.Clean(e.Repo.Path), e.PR.Number); !open[k] {
			gone[k] = true
		}
	}
	if len(gone) == 0 {
		return
	}
	for _, t := range m.Tabs {
		for _, wi := range t.Info.Wts {
			if wi.HasPR && gone[PrKey(filepath.Clean(wi.Repo), wi.Pr.Number)] {
				t.LookAgain(true)
			}
		}
	}
}

// LoadPRWatch takes up what the session kept of the watching.
func (m *Core) LoadPRWatch(s *state.PRWatch) {
	if s == nil {
		return
	}
	w := &m.PRWatch
	w.Known, w.News, w.Repos = s.Known, s.News, map[string]bool{}
	for _, r := range s.Repos {
		w.Repos[r] = true
	}
	m.Tell(PRMarkMsg{})
}

// SavedPRWatch is the watching as the session keeps it.
func (m *Core) SavedPRWatch() *state.PRWatch {
	w := &m.PRWatch
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

// TickPRs asks GitHub again when it is time to: while the PRs are watched
// or their list is shown.
func (m *Core) TickPRs(now time.Time) Cmd {
	if !IsOn(m.Cfg.PRNotify) && !(m.Client != nil && m.Client.Sight().PRsShown) {
		return nil
	}
	if now.Sub(m.PRWatch.At) < m.GithubEvery(PrWatchEvery) {
		return nil
	}
	return m.RefreshPRs()
}

// RefreshPRs asks GitHub for your pull requests, unless it is being asked.
func (m *Core) RefreshPRs() Cmd {
	w := &m.PRWatch
	if w.Busy {
		return nil
	}
	if !github.Available() {
		w.Errs = map[string]string{"": "the gh CLI is not installed"}
		m.Tell(PRsMsg{})
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
	m.Tell(PRsMsg{})
	root, wtDir, dirs := m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs
	return func() Event { return fetchPRs(root, wtDir, dirs, skip) }
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
func fetchPRs(root, wtDir string, dirs []string, skip map[string]bool) PRWatchMsg {
	msg := PRWatchMsg{Answered: map[string]bool{}, Failed: map[string]string{},
		NotGitHub: map[string]bool{}, Skipped: map[string]bool{}}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		msg.Failed[""] = err.Error()
		return msg
	}
	withPRs, err := github.ReposWithMyPRs()
	if err != nil {
		Debugf("pr search: %v; asking every repository", err)
		withPRs = nil
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, PrFetchers)
	)
	for _, r := range repos {
		if withPRs != nil {
			if names, elsewhere, err := RepoNames(r.Path); err == nil && !AskRepo(names, elsewhere, withPRs) {
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
			prs, err := github.ListMyPRs(r.Path, PRListLimit)
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
				msg.PRs = append(msg.PRs, PREntry{Repo: r, PR: pr, Wt: checkouts[LocalBranch(pr)]})
			}
		}(r)
	}
	wg.Wait()
	return msg
}

// OnPRWatch takes a look at the pull requests: the lists show it, and
// what changed is told. A repository that did not answer keeps what was
// known of it, so its answering again is not taken for news.
func (m *Core) OnPRWatch(msg PRWatchMsg) {
	w := &m.PRWatch
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

	entries := append([]PREntry(nil), msg.PRs...)
	for _, e := range w.Entries {
		if kept(e.Repo.Path) {
			entries = append(entries, e)
		}
	}
	SortPREntries(entries)
	m.LookAtGonePRs(w.Entries, entries)
	w.Entries = entries

	for r := range w.Errs {
		if !kept(r) {
			delete(w.Errs, r)
		}
	}
	for r, why := range msg.Failed {
		w.Errs[r] = why
		if msg.NotGitHub[r] {
			w.Skip[r] = time.Now().Add(NotGitHubFor)
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
		k := PrKey(e.Repo.Path, e.PR.Number)
		now := SeenOf(e.Repo.Path, e.PR)
		if w.Repos[e.Repo.Path] {
			tabName := ""
			if t := m.TabOnBranch(e.Repo.Path, e.Branch()); t != nil {
				tabName = t.Name
			}
			for _, c := range PrChanges(w.Known[k], now) {
				news = append(news, state.PRNews{Key: k, Repo: e.Repo.Name, Number: e.PR.Number, What: c.What, Tab: tabName, Checks: c.Checks})
			}
		}
		known[k] = now
	}
	for r := range msg.Answered {
		w.Repos[r] = true
	}
	w.Known = known
	m.DropStalePRNews()
	if len(news) > 0 && IsOn(m.Cfg.PRNotify) {
		m.TellPRNews(news)
	}
	m.Tell(PRsMsg{})
	m.SaveSession()
}

// DropStalePRNews forgets news not seen yet that no longer holds: of a
// pull request no longer open, merged or closed, and of checks that no
// longer fail, passing again or pushed past. A repository that did not
// answer keeps its news, as it keeps what is known of it.
func (m *Core) DropStalePRNews() {
	w := &m.PRWatch
	m.KeepPRNews(func(n state.PRNews) bool {
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

// ForgetTabPRNews forgets the news not seen yet that names the tab: the
// tab is closed, and its work with it.
func (m *Core) ForgetTabPRNews(name string) {
	m.KeepPRNews(func(n state.PRNews) bool { return n.Tab != name })
}

// KeepPRNews keeps the news not seen yet that keep says to, and the marks
// with it.
func (m *Core) KeepPRNews(keep func(state.PRNews) bool) {
	w := &m.PRWatch
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
	m.Tell(PRMarkMsg{})
}

// TakePRNews is the news not seen yet, by pull request, and forgets it:
// the list it is shown in is where it is seen.
func (m *Core) TakePRNews() map[string][]string {
	out := map[string][]string{}
	for _, n := range m.PRWatch.News {
		out[n.Key] = append(out[n.Key], n.What)
	}
	if len(m.PRWatch.News) > 0 {
		m.PRWatch.News = nil
		m.SaveSession()
	}
	m.Tell(PRMarkMsg{})
	return out
}

// TabOnBranch is the tab with a worktree of repo on branch, or nil.
func (m *Core) TabOnBranch(repo, branch string) *Tab {
	for _, t := range m.Tabs {
		for _, w := range t.Worktrees {
			if filepath.Clean(w.Repo) == filepath.Clean(repo) && w.Branch == branch {
				return t
			}
		}
	}
	return nil
}

// TellPRNews keeps news for the marks, and tells the client of it.
func (m *Core) TellPRNews(news []state.PRNews) {
	m.PRWatch.News = append(m.PRWatch.News, news...)
	m.Tell(PRNewsMsg{News: news})
}
