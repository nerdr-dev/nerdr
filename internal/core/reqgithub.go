package core

import "github.com/nerdr-dev/nerdr/internal/workspace"

// The requests about what core asks GitHub and git in the background: the
// tabs' branches, your pull requests and issues.

func init() {
	registerRequests(LookAgain{}, RefreshPRs{}, RefreshIssues{}, ClearPRNews{}, ClearIssueNews{}, AskAllIssues{})
}

// LookAgain has tab T's branches looked at afresh, and with PR its pull
// requests too.
type LookAgain struct {
	T  TabRef
	PR bool
}

func (q LookAgain) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		t.LookAgain(q.PR)
	}
	return m.RefreshInfo()
}

// RefreshPRs asks GitHub for your pull requests again.
type RefreshPRs struct{}

func (RefreshPRs) Do(m *Core) Cmd { return m.RefreshPRs() }

// RefreshIssues asks GitHub for the issues assigned to you again.
type RefreshIssues struct{}

func (RefreshIssues) Do(m *Core) Cmd { return m.RefreshIssues() }

// ClearPRNews is the news of your pull requests seen.
type ClearPRNews struct{}

func (ClearPRNews) Do(m *Core) Cmd {
	if len(m.PRWatch.News) > 0 {
		m.PRWatch.News = nil
		m.SaveSession()
	}
	m.Tell(PRMarkMsg{})
	return nil
}

// ClearIssueNews is the issues newly assigned to you seen.
type ClearIssueNews struct{}

func (ClearIssueNews) Do(m *Core) Cmd {
	if len(m.IssueWatch.News) > 0 {
		m.IssueWatch.News = nil
		m.SaveSession()
	}
	return nil
}

// AskAllIssues asks Repos for their open issues, for the issue list
// when it shows all of them.
type AskAllIssues struct{ Repos []workspace.Repo }

func (q AskAllIssues) Do(m *Core) Cmd {
	if m.IssueWatch.AllBusy || len(q.Repos) == 0 {
		return nil
	}
	m.IssueWatch.AllBusy = true
	m.Tell(IssuesMsg{})
	repos := q.Repos
	return func() Event { return FetchAllIssues(repos) }
}

// OnIssueAll takes a look at repositories' open issues.
func (m *Core) OnIssueAll(msg IssueAllMsg) {
	w := &m.IssueWatch
	w.AllBusy = false
	if w.All == nil {
		w.All = map[string]RepoIssues{}
	}
	for path, got := range msg.Repos {
		w.All[path] = got
	}
	m.Tell(IssuesMsg{})
}
