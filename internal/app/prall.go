package app

import (
	"sort"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"

	tea "github.com/charmbracelet/bubbletea"
)

// The list of your pull requests has a second scope, where m shows it:
// every open pull request in the repositories nerdr finds, whoever opened
// it, with those your review is asked for marked and first. They are
// asked for only while that list is shown, and not watched: news is of
// your own.

// refreshAllPRs has core ask the repositories for their open pull
// requests while the list shows them all: those not asked lately, or all
// when force is set. See core.PRAll.
func (m *Model) refreshAllPRs(force bool) tea.Cmd {
	d := m.prPanel
	if !m.panelAllPRs() || d == nil || !d.all || m.PRAll.Busy {
		return nil
	}
	return m.do(core.AskAllPRs{Force: force})
}

// markedPRs is got's pull requests, those your review is asked for, by
// github.RequestKey in asked, marked.
func markedPRs(got core.RepoPRs, asked map[string]bool) []core.PREntry {
	out := make([]core.PREntry, 0, len(got.Entries))
	for _, e := range got.Entries {
		for _, n := range got.Names {
			if asked[github.RequestKey(n, e.PR.Number)] {
				e.Asked = true
			}
		}
		out = append(out, e)
	}
	return out
}

// sortAllPREntries groups pull requests by repository, those your review
// is asked for first within each, then the most recently updated.
func sortAllPREntries(entries []core.PREntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		x, y := entries[i], entries[j]
		switch {
		case x.Repo.Name != y.Repo.Name:
			return x.Repo.Name < y.Repo.Name
		case x.Asked != y.Asked:
			return x.Asked
		}
		return y.PR.UpdatedAt.Before(x.PR.UpdatedAt)
	})
}

// allPREntries is everyone's open pull requests as the list shows them,
// by repository, those your review is asked for first within each, and
// what went wrong asking.
func (m *Model) allPREntries() ([]core.PREntry, []string) {
	a := m.PRAll
	var entries []core.PREntry
	var errs []string
	for _, got := range a.Repos {
		entries = append(entries, markedPRs(got, a.Asked)...)
		if got.Err != "" {
			errs = append(errs, got.Err)
		}
	}
	if a.AskedErr != "" {
		errs = append(errs, a.AskedErr)
	}
	sort.Strings(errs)
	sortAllPREntries(entries)
	return entries, errs
}

// askedCount is how many of entries your review is asked for.
func askedCount(entries []core.PREntry) int {
	n := 0
	for _, e := range entries {
		if e.Asked {
			n++
		}
	}
	return n
}
