package core

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

const (
	// IssueWatchEvery is how often GitHub is asked for the issues assigned
	// to you.
	IssueWatchEvery = 5 * time.Minute
	// IssueAllEvery is how long a repository's open issues, asked for by
	// the list, are taken as they were.
	IssueAllEvery = 2 * time.Minute
	// IssueListLimit caps how many issues are asked for per repository.
	IssueListLimit = 50
)

// IssueWatch is what is known of the issues.
type IssueWatch struct {
	Mine    []IssueEntry // assigned to you, in the repositories here
	MineErr string
	Known   map[string]bool // every issue assigned to you, by issueKey
	Seeded  bool            // known has been learned once: after that, new is news
	News    []state.IssueNews
	Busy    bool
	At      time.Time

	// All is each repository's open issues, by its path, asked for when
	// the list shows all issues.
	All     map[string]RepoIssues
	AllBusy bool
}

// RepoIssues is a repository's open issues, or why it did not answer.
type RepoIssues struct {
	Entries []IssueEntry
	Err     string
	At      time.Time
}

// IssueEntry is one issue, and the repository here it is in.
type IssueEntry struct {
	Repo  workspace.Repo
	Issue github.Issue
}

// IssueWatchMsg is a look at the issues assigned to you.
type IssueWatchMsg struct {
	Keys    []string     // every one, by issueKey, wherever it is
	Entries []IssueEntry // those in the repositories here
	Err     string
}

// IssueAllMsg is a look at some repositories' open issues.
type IssueAllMsg struct {
	Repos map[string]RepoIssues
}

func IssueKey(repo string, number int) string { return repo + "#" + strconv.Itoa(number) }

// LoadIssueWatch takes up what the session kept of the watching.
func (m *Core) LoadIssueWatch(s *state.IssueWatch) {
	if s == nil {
		return
	}
	w := &m.IssueWatch
	w.Known, w.Seeded, w.News = map[string]bool{}, true, s.News
	for _, k := range s.Known {
		w.Known[k] = true
	}
}

// SavedIssueWatch is the watching as the session keeps it.
func (m *Core) SavedIssueWatch() *state.IssueWatch {
	w := &m.IssueWatch
	if !w.Seeded {
		return nil
	}
	s := &state.IssueWatch{News: w.News}
	for k := range w.Known {
		s.Known = append(s.Known, k)
	}
	sort.Strings(s.Known)
	return s
}

// FetchAllIssues asks repos for their open issues, a few at once.
func FetchAllIssues(repos []workspace.Repo) IssueAllMsg {
	msg := IssueAllMsg{Repos: map[string]RepoIssues{}}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, PrFetchers)
	)
	for _, r := range repos {
		wg.Add(1)
		go func(r workspace.Repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got := RepoIssues{At: time.Now()}
			issues, err := github.ListIssues(r.Path, IssueListLimit)
			if err != nil {
				got.Err = r.Name + ": " + err.Error()
			}
			for _, is := range issues {
				got.Entries = append(got.Entries, IssueEntry{Repo: r, Issue: is})
			}
			mu.Lock()
			msg.Repos[r.Path] = got
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	return msg
}

// SortIssueEntries groups issues by repository, the most recently updated
// first within each.
func SortIssueEntries(entries []IssueEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Repo.Name != b.Repo.Name {
			return a.Repo.Name < b.Repo.Name
		}
		return b.Issue.UpdatedAt.Before(a.Issue.UpdatedAt)
	})
}

// takeIssueNews is the issues newly assigned and not seen yet, and
// forgets them: the list they are shown in is where they are seen.
func (m *Core) TakeIssueNews() map[string]bool {
	out := m.IssueNewsByKey()
	ClearIssueNews{}.Do(m)
	return out
}

// IssueNewsByKey is the issues newly assigned to you not seen yet, by key.
func (m *Core) IssueNewsByKey() map[string]bool {
	out := map[string]bool{}
	for _, n := range m.IssueWatch.News {
		out[n.Key] = true
	}
	return out
}

// TickIssues asks GitHub again when it is time to: while the issues are
// watched or their list is shown.
func (m *Core) TickIssues(now time.Time) Cmd {
	shown := m.Client != nil && m.Client.Sight().IssuesShown
	if !IsOn(m.Cfg.IssueNotify) && !shown {
		return nil
	}
	every := m.GithubEvery(IssueWatchEvery)
	if shown {
		every = m.GithubEvery(IssueAllEvery)
	}
	if now.Sub(m.IssueWatch.At) < every {
		return nil
	}
	return Batch(m.RefreshIssues(), m.Tell(IssuesDueMsg{}))
}

// RefreshIssues asks GitHub for the issues assigned to you, unless it is
// being asked.
func (m *Core) RefreshIssues() Cmd {
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
	return func() Event { return fetchMyIssues(root, wtDir, dirs) }
}

// fetchMyIssues searches for the issues assigned to you, and finds the
// repository here each is in.
func fetchMyIssues(root, wtDir string, dirs []string) IssueWatchMsg {
	issues, err := github.MyIssues()
	if err != nil {
		return IssueWatchMsg{Err: err.Error()}
	}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		return IssueWatchMsg{Err: err.Error()}
	}
	byName := map[string]workspace.Repo{}
	for _, r := range repos {
		names, _, err := RepoNames(r.Path)
		if err != nil {
			continue
		}
		for _, n := range names {
			if _, ok := byName[n]; !ok {
				byName[n] = r
			}
		}
	}
	var msg IssueWatchMsg
	for _, is := range issues {
		msg.Keys = append(msg.Keys, IssueKey(is.Repo, is.Number))
		if r, ok := byName[is.Repo]; ok {
			msg.Entries = append(msg.Entries, IssueEntry{Repo: r, Issue: is})
		}
	}
	return msg
}

// OnIssueWatch takes a look at the issues assigned to you: the client shows
// it, those newly assigned in the repositories here are told, and news of
// those no longer assigned, or closed, is forgotten. The
// first look only learns which there are. A look that failed keeps what
// was known, so the next one does not take them all for news.
func (m *Core) OnIssueWatch(msg IssueWatchMsg) {
	w := &m.IssueWatch
	w.Busy = false
	w.MineErr = msg.Err
	if msg.Err != "" {
		m.Tell(IssuesMsg{})
		return
	}
	var news []state.IssueNews
	if w.Seeded {
		for _, e := range msg.Entries {
			k := IssueKey(e.Issue.Repo, e.Issue.Number)
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
	SortIssueEntries(w.Mine)
	if len(news) > 0 && IsOn(m.Cfg.IssueNotify) {
		w.News = append(w.News, news...)
		m.Tell(IssueNewsMsg{News: news})
	}
	m.Tell(IssuesMsg{})
	m.SaveSession()
}
