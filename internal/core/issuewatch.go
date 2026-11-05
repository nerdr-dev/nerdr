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
	out := map[string]bool{}
	for _, n := range m.IssueWatch.News {
		out[n.Key] = true
	}
	if len(m.IssueWatch.News) > 0 {
		m.IssueWatch.News = nil
		m.SaveSession()
	}
	return out
}
