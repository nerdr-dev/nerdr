package core

import (
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Everyone's pull requests, for the list's second scope and for the
// new-tab dialog: every open pull request in the repositories nerdr finds,
// whoever opened it, with those your review is asked for. They are asked
// for only while a client shows them, and not watched: news is of your
// own. And the git and gh a pull request picked in a list needs: where its
// branch is checked out, a fork's fetched, and what its reviews and build
// say.

// PrAllEvery is how long a repository's open pull requests, asked for by
// the list, are taken as they were.
const PrAllEvery = 2 * time.Minute

func init() {
	registerRequests(AskAllPRs{}, AskRepoPRs{}, LookUpCheckout{}, FetchForkPR{}, FetchPR{})
}

// RepoPRs is what a repository answered when asked for its open pull
// requests: when (At), the pull requests, the repository on github.com
// ("owner/name", Names), and why it did not answer.
type RepoPRs struct {
	At      time.Time
	Entries []PREntry
	Names   []string
	Err     string
}

// PRAll is what is known of everyone's pull requests: by repository path;
// those your review is asked for, by github.RequestKey (Asked), and why
// that could not be asked (AskedErr). Busy is set while they are asked
// for.
type PRAll struct {
	Repos    map[string]RepoPRs
	Asked    map[string]bool
	AskedErr string
	Busy     bool
}

// PRAllMsg is a look at the repositories' open pull requests.
type PRAllMsg struct {
	Repos    map[string]RepoPRs
	Asked    map[string]bool
	AskedErr string
	ScanErr  string
}

// AskAllPRs asks the repositories not asked lately for their open pull
// requests, all of them with Force. The client is told PRsMsg once they
// have answered, and when the asking starts.
type AskAllPRs struct{ Force bool }

func (q AskAllPRs) Do(m *Core) Cmd {
	a := &m.PRAll
	if a.Busy {
		return nil
	}
	if m.NoGh {
		a.AskedErr = "the gh CLI is not installed"
		return m.Tell(PRsMsg{})
	}
	at := map[string]time.Time{}
	for path, got := range a.Repos {
		at[path] = got.At
	}
	a.Busy = true
	root, wtDir, dirs, every := m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs, m.GithubEvery(PrAllEvery)
	return Batch(m.Tell(PRsMsg{}), func() Event { return FetchAllPRs(root, wtDir, dirs, at, every, q.Force) })
}

// FetchAllPRs asks the repositories not asked for every (all of them when
// force is set) for their open pull requests, a few at once, and searches
// once for those your review is asked for.
func FetchAllPRs(root, wtDir string, dirs []string, at map[string]time.Time, every time.Duration, force bool) PRAllMsg {
	msg := PRAllMsg{Repos: map[string]RepoPRs{}}
	repos, err := workspace.ScanAll(root, wtDir, dirs)
	if err != nil {
		msg.ScanErr = err.Error()
	}
	if msg.Asked, err = github.ReviewRequested(); err != nil {
		msg.AskedErr = "review requests: " + err.Error()
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, PrFetchers)
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
			got := FetchRepoPRs(r)
			mu.Lock()
			msg.Repos[r.Path] = got
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	return msg
}

// FetchRepoPRs asks repository r for its open pull requests, and finds
// where their branches are checked out.
func FetchRepoPRs(r workspace.Repo) RepoPRs {
	got := RepoPRs{At: time.Now()}
	got.Names, _, _ = RepoNames(r.Path)
	prs, err := github.ListPRs(r.Path, PRListLimit)
	if err != nil {
		got.Err = r.Name + ": " + err.Error()
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
		got.Entries = append(got.Entries, PREntry{Repo: r, PR: pr, Wt: checkouts[LocalBranch(pr)]})
	}
	return got
}

// OnPRAll takes a look at the repositories' open pull requests.
func (m *Core) OnPRAll(msg PRAllMsg) Cmd {
	a := &m.PRAll
	a.Busy = false
	m.keepRepoPRs(msg.Repos, msg.Asked)
	a.AskedErr = msg.AskedErr
	if msg.ScanErr != "" {
		a.AskedErr = msg.ScanErr
	}
	return m.Tell(PRsMsg{})
}

// keepRepoPRs keeps what repositories answered, and the pull requests
// your review is asked for; a search that failed (nil) keeps what it
// knew: the marks do not all go for a moment's trouble.
func (m *Core) keepRepoPRs(repos map[string]RepoPRs, asked map[string]bool) {
	a := &m.PRAll
	if a.Repos == nil {
		a.Repos = map[string]RepoPRs{}
	}
	for path, got := range repos {
		a.Repos[path] = got
	}
	if asked != nil {
		a.Asked = asked
	}
}

// AskRepoPRs asks repository Repo for its open pull requests, and GitHub
// for those your review is asked for, for the new-tab dialog of Job: kept
// in PRAll, and told as a RepoPRsMsg.
type AskRepoPRs struct {
	Job  string
	Repo workspace.Repo
}

// RepoPRsMsg tells the client of Job that Repo's pull requests are in
// PRAll; AskedErr is why the pull requests your review is asked for could
// not be.
type RepoPRsMsg struct {
	Job, Repo, AskedErr string
}

// repoPRsMsg is a repository's pull requests, asked for a client.
type repoPRsMsg struct {
	Job      string
	Repo     string
	Got      RepoPRs
	Asked    map[string]bool
	AskedErr string
}

func (q AskRepoPRs) Do(m *Core) Cmd {
	return func() Event {
		msg := repoPRsMsg{Job: q.Job, Repo: q.Repo.Path, Got: FetchRepoPRs(q.Repo)}
		var err error
		if msg.Asked, err = github.ReviewRequested(); err != nil {
			msg.AskedErr = "review requests: " + err.Error()
		}
		return msg
	}
}

func (m *Core) onRepoPRs(msg repoPRsMsg) Cmd {
	m.keepRepoPRs(map[string]RepoPRs{msg.Repo: msg.Got}, msg.Asked)
	return Batch(m.Tell(RepoPRsMsg{Job: msg.Job, Repo: msg.Repo, AskedErr: msg.AskedErr}), m.Tell(PRsMsg{}))
}

// LookUpCheckout finds the worktree E's branch is checked out in, as git
// says now: a list's, from when GitHub was last asked, can be minutes
// old, from before a worktree was made or removed. Told as a CheckoutMsg
// of Job.
type LookUpCheckout struct {
	Job string
	E   PREntry
}

// CheckoutMsg is the pull request LookUpCheckout of Job looked at, with
// the worktree its branch is checked out in (E.Wt), "" for none.
type CheckoutMsg struct {
	Job string
	E   PREntry
}

func (q LookUpCheckout) Do(m *Core) Cmd {
	return Later(func() Event {
		e := q.E
		e.Wt = CheckedOut(e)
		return CheckoutMsg{Job: q.Job, E: e}
	})
}

// CheckedOut is the worktree with e's branch checked out, as git says
// now; the list's when git cannot say.
func CheckedOut(e PREntry) string {
	wts, err := git.ListWorktrees(e.Repo.Path)
	if err != nil {
		return e.Wt
	}
	for _, w := range wts {
		if w.Branch == e.Branch() {
			return w.Path
		}
	}
	return ""
}

// FetchForkPR fetches E, a pull request from a fork, into its local
// branch, for a tab to be made on it: told as a ForkFetchedMsg of Job.
type FetchForkPR struct {
	Job string
	E   PREntry
}

// ForkFetchedMsg is E fetched for Job, or why it was not.
type ForkFetchedMsg struct {
	Job string
	E   PREntry
	Err string
}

func (q FetchForkPR) Do(m *Core) Cmd {
	return Later(func() Event {
		msg := ForkFetchedMsg{Job: q.Job, E: q.E}
		if err := workspace.FetchForkPR(q.E.Repo.Path, q.E.PR.Number, q.E.Branch()); err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

// FetchPR reads pull request Number in Dir with its reviews and comments,
// and with Logs the failing jobs' logs, at most Limit of each: told as a
// PRFetchedMsg of Job.
type FetchPR struct {
	Job    string
	Dir    string
	Number int
	Logs   bool
	Limit  int
}

// PRFetchedMsg is the pull request FetchPR of Job read, with its failing
// jobs' logs when asked; or why it could not.
type PRFetchedMsg struct {
	Job  string
	PR   github.PR
	Logs string
	Err  string
}

func (q FetchPR) Do(m *Core) Cmd {
	return Later(func() Event {
		msg := PRFetchedMsg{Job: q.Job}
		pr, err := github.FetchPR(q.Dir, q.Number)
		if err == nil && q.Logs {
			msg.Logs, err = github.FailedRunLogs(q.Dir, pr.Checks, q.Limit)
		}
		msg.PR = pr
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}
