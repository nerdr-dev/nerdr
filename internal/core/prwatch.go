package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

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
