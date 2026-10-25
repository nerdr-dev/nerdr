package app

import (
	"sort"
	"sync"
	"time"

	"nerdr/internal/git"
	"nerdr/internal/github"
	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// The list of your pull requests has a second scope, where m shows it:
// every open pull request in the repositories nerdr finds, whoever opened
// it, with those your review is asked for marked and first. They are
// asked for only while that list is shown, and not watched: news is of
// your own.

// prAllEvery is how long a repository's open pull requests, asked for by
// the list, are taken as they were.
const prAllEvery = 2 * time.Minute

// repoPRs is what a repository answered when asked for its open pull
// requests.
type repoPRs struct {
	at      time.Time
	entries []prEntry
	names   []string // the repository on github.com, "owner/name"
	err     string
}

// prAll is what is known of everyone's pull requests.
type prAll struct {
	repos map[string]repoPRs // by repository path
	// asked is the pull requests your review is asked for, by
	// github.RequestKey.
	asked    map[string]bool
	askedErr string
	busy     bool
}

// prAllMsg is a look at the repositories' open pull requests.
type prAllMsg struct {
	repos    map[string]repoPRs
	asked    map[string]bool
	askedErr string
	scanErr  string
}

// refreshAllPRs asks the repositories for their open pull requests while
// the list shows them all: those not asked lately, or all when force is
// set.
func (m *Model) refreshAllPRs(force bool) tea.Cmd {
	d, a := m.prPanel, &m.prAll
	if !m.panelAllPRs() || d == nil || !d.all || a.busy {
		return nil
	}
	if !github.Available() {
		a.askedErr = "the gh CLI is not installed"
		m.applyPRs()
		return nil
	}
	at := map[string]time.Time{}
	for path, got := range a.repos {
		at[path] = got.at
	}
	a.busy = true
	m.applyPRs()
	root, wtDir, dirs, every := m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs, m.githubEvery(prAllEvery)
	return func() tea.Msg { return fetchAllPRs(root, wtDir, dirs, at, every, force) }
}

// fetchAllPRs asks the repositories not asked for every (all of
// them when force is set) for their open pull requests, a few at once,
// and searches once for those your review is asked for.
func fetchAllPRs(root, wtDir string, dirs []string, at map[string]time.Time, every time.Duration, force bool) prAllMsg {
	msg := prAllMsg{repos: map[string]repoPRs{}}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		msg.scanErr = err.Error()
	}
	if msg.asked, err = github.ReviewRequested(); err != nil {
		msg.askedErr = "review requests: " + err.Error()
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, prFetchers)
	)
	for _, r := range repos {
		if t, ok := at[r.Path]; ok && !force && time.Since(t) < every {
			continue
		}
		wg.Add(1)
		go func(r workspace.Repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got := fetchRepoPRs(r)
			mu.Lock()
			msg.repos[r.Path] = got
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	return msg
}

// fetchRepoPRs asks repository r for its open pull requests, and finds
// where their branches are checked out.
func fetchRepoPRs(r workspace.Repo) repoPRs {
	got := repoPRs{at: time.Now()}
	got.names, _, _ = repoNames(r.Path)
	prs, err := github.ListPRs(r.Path, prListLimit)
	if err != nil {
		got.err = r.Name + ": " + err.Error()
	}
	checkouts := map[string]string{}
	if wts, err := git.ListWorktrees(r.Path); err == nil {
		for _, w := range wts {
			if w.Branch != "" {
				checkouts[w.Branch] = w.Path
			}
		}
	}
	for _, pr := range prs {
		got.entries = append(got.entries, prEntry{repo: r, pr: pr, wt: checkouts[localBranch(pr)]})
	}
	return got
}

// marked is got's pull requests, those your review is asked for, by
// github.RequestKey in asked, marked.
func (got repoPRs) marked(asked map[string]bool) []prEntry {
	out := make([]prEntry, 0, len(got.entries))
	for _, e := range got.entries {
		for _, n := range got.names {
			if asked[github.RequestKey(n, e.pr.Number)] {
				e.asked = true
			}
		}
		out = append(out, e)
	}
	return out
}

// sortAllPREntries groups pull requests by repository, those your review
// is asked for first within each, then the most recently updated.
func sortAllPREntries(entries []prEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		x, y := entries[i], entries[j]
		switch {
		case x.repo.Name != y.repo.Name:
			return x.repo.Name < y.repo.Name
		case x.asked != y.asked:
			return x.asked
		}
		return y.pr.UpdatedAt.Before(x.pr.UpdatedAt)
	})
}

// onPRAll takes a look at the repositories' open pull requests.
func (m *Model) onPRAll(msg prAllMsg) {
	a := &m.prAll
	a.busy = false
	if a.repos == nil {
		a.repos = map[string]repoPRs{}
	}
	for path, got := range msg.repos {
		a.repos[path] = got
	}
	// A search that failed keeps what it knew: the marks do not all go
	// for a moment's trouble.
	if msg.asked != nil {
		a.asked = msg.asked
	}
	a.askedErr = msg.askedErr
	if msg.scanErr != "" {
		a.askedErr = msg.scanErr
	}
	m.applyPRs()
}

// allPREntries is everyone's open pull requests as the list shows them,
// by repository, those your review is asked for first within each, and
// what went wrong asking.
func (m *Model) allPREntries() ([]prEntry, []string) {
	a := &m.prAll
	var entries []prEntry
	var errs []string
	for _, got := range a.repos {
		entries = append(entries, got.marked(a.asked)...)
		if got.err != "" {
			errs = append(errs, got.err)
		}
	}
	if a.askedErr != "" {
		errs = append(errs, a.askedErr)
	}
	sort.Strings(errs)
	sortAllPREntries(entries)
	return entries, errs
}

// askedCount is how many of entries your review is asked for.
func askedCount(entries []prEntry) int {
	n := 0
	for _, e := range entries {
		if e.asked {
			n++
		}
	}
	return n
}
