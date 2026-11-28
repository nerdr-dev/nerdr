// Package forges picks the service a repository is on — GitHub or
// GitLab — and asks it, so the rest of nerdr asks one API whatever the
// repository's service is, and a tab, a list of pull requests or the
// watch on them can hold repositories of both at once.
//
// Which service is the one of the repository's origin remote's host (see
// forge.KindOf); a repository with no remote nerdr can read is taken for
// GitHub's, as nerdr always took them, and gh says what it makes of it.
package forges

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/gitlab"
)

// Forge is a repository's service, as nerdr asks it about a repository
// in a folder: dir is the repository, or a worktree of it.
type Forge interface {
	Kind() forge.Kind
	// Available is whether its CLI is installed.
	Available() bool
	// NoRepo is whether err, from asking about a repository, says the
	// service has no such repository: asking again will not help.
	NoRepo(err error) bool
	CurrentPR(dir string) (forge.PR, bool, string)
	FetchPR(dir string, n int) (forge.PR, error)
	ListMyPRs(dir string, limit int) ([]forge.PR, error)
	ListPRs(dir string, limit int) ([]forge.PR, error)
	BranchPR(dir, branch string) (n int, state, head string, ok bool)
	ForkPR(dir, branch string) (n int, owner string, ok bool)
	BranchStatus(dir string) (forge.Status, bool)
	FailedRunLogs(dir string, checks []forge.Check, limit int) (string, error)
	CreatePR(dir, origin string) (string, error)
	FetchIssue(dir string, n int) (forge.Issue, error)
	ListIssues(dir string, limit int) ([]forge.Issue, error)
	// PRRef is the ref the service keeps pull request n's head at.
	PRRef(n int) string
}

var (
	_ Forge = github.Forge{}
	_ Forge = gitlab.Forge{}
)

// RemotesFor is how long a folder's remotes are taken as they were: they
// hardly ever change, and every look at a branch's pull request would
// read them.
const RemotesFor = 10 * time.Minute

type remotesAt struct {
	remotes []forge.Remote
	odd     bool
	at      time.Time
}

var cache = struct {
	sync.Mutex
	m map[string]remotesAt
}{m: map[string]remotesAt{}}

// readRemotes is forge.Remotes, which tests replace.
var readRemotes = forge.Remotes

// Remotes is forge.Remotes of dir, kept for RemotesFor.
func Remotes(dir string) ([]forge.Remote, bool, error) {
	cache.Lock()
	in, ok := cache.m[dir]
	cache.Unlock()
	if ok && time.Since(in.at) < RemotesFor {
		return in.remotes, in.odd, nil
	}
	remotes, odd, err := readRemotes(dir)
	if err != nil {
		return nil, false, err
	}
	cache.Lock()
	cache.m[dir] = remotesAt{remotes: remotes, odd: odd, at: time.Now()}
	cache.Unlock()
	return remotes, odd, nil
}

// Forget drops what is kept of dir's remotes: they were just changed.
func Forget(dir string) {
	cache.Lock()
	delete(cache.m, dir)
	cache.Unlock()
}

// For is the service of the repository at dir: that of its origin, or,
// with no origin, of its first remote; GitHub when it has none nerdr can
// read.
func For(dir string) Forge {
	remotes, _, err := Remotes(dir)
	if err != nil || len(remotes) == 0 {
		return github.Forge{}
	}
	return ForRemote(remotes[0], remotes)
}

// ForRemote is the service of the repository at origin; others are the
// repository's remotes, among which a GitLab project's upstream is.
func ForRemote(origin forge.Remote, others []forge.Remote) Forge {
	if origin.Kind() != forge.GitLab {
		return github.Forge{}
	}
	f := gitlab.Forge{Origin: origin}
	for _, r := range others {
		if r.Name == "upstream" && r.Host == origin.Host {
			f.Upstream = r
		}
	}
	return f
}

// ForURL is the service of the repository at a remote's url.
func ForURL(url string) Forge {
	r, ok := forge.ParseRemote(url)
	if !ok {
		return github.Forge{}
	}
	r.Name = "origin"
	return ForRemote(r, nil)
}

// KindOf is the service of the repository at dir.
func KindOf(dir string) forge.Kind { return For(dir).Kind() }

// Available reports whether a CLI nerdr asks a service with, gh or glab,
// is installed.
func Available() bool { return github.Available() || gitlab.Available() }

// Missing is what is said when neither is.
const Missing = "neither the gh nor the glab CLI is installed"

// The calls of a repository, made of its service.

func CurrentPR(dir string) (forge.PR, bool, string) { return For(dir).CurrentPR(dir) }
func FetchPR(dir string, n int) (forge.PR, error)   { return For(dir).FetchPR(dir, n) }
func ListMyPRs(dir string, limit int) ([]forge.PR, error) {
	return For(dir).ListMyPRs(dir, limit)
}
func ListPRs(dir string, limit int) ([]forge.PR, error) { return For(dir).ListPRs(dir, limit) }
func BranchPR(dir, branch string) (int, string, string, bool) {
	return For(dir).BranchPR(dir, branch)
}
func ForkPR(dir, branch string) (int, string, bool) { return For(dir).ForkPR(dir, branch) }

// BranchStatus is the short state of the pull request of the branch
// checked out in dir; nothing when its service's CLI is not installed.
func BranchStatus(dir string) (forge.Status, bool) {
	f := For(dir)
	if !f.Available() {
		return forge.Status{}, false
	}
	return f.BranchStatus(dir)
}
func FailedRunLogs(dir string, checks []forge.Check, limit int) (string, error) {
	return For(dir).FailedRunLogs(dir, checks, limit)
}

// CreatePR opens a pull request in origin, the repository at the URL
// nerdr recorded, on its service.
func CreatePR(dir, origin string) (string, error) { return ForURL(origin).CreatePR(dir, origin) }
func FetchIssue(dir string, n int) (forge.Issue, error) {
	return For(dir).FetchIssue(dir, n)
}

// FetchIssueOf reads issue n of repo (forge.RepoKey), one of dir's
// remotes: the issues assigned to you are of each of a repository's
// remotes, a fork's upstream too, and #7 of one is not #7 of another.
// With repo "", or none of dir's remotes, it is FetchIssue.
func FetchIssueOf(dir, repo string, n int) (forge.Issue, error) {
	if repo != "" {
		remotes, _, err := Remotes(dir)
		if err == nil {
			for _, r := range remotes {
				if r.Key() == repo && r.Kind() == forge.GitLab {
					return ForRemote(r, nil).FetchIssue(dir, n)
				}
			}
		}
	}
	return FetchIssue(dir, n)
}
func ListIssues(dir string, limit int) ([]forge.Issue, error) {
	return For(dir).ListIssues(dir, limit)
}

// PRRef is the ref pull request n's head is at in dir's origin.
func PRRef(dir string, n int) string { return For(dir).PRRef(n) }

// NoRepo is whether err, from asking dir's service about it, says there
// is no such repository there.
func NoRepo(dir string, err error) bool { return For(dir).NoRepo(err) }

// Available is whether the CLI of dir's service is installed.
func AvailableFor(dir string) bool { return For(dir).Available() }

// RepoNames is the repositories the remotes of the repository at dir
// are, as forge.RepoKey names them, on GitHub and on GitLab; and whether
// it has a remote nerdr cannot name so: one on GitHub Enterprise, which
// the searches here do not cover, or a local path.
func RepoNames(dir string) (names []string, elsewhere bool, err error) {
	remotes, odd, err := Remotes(dir)
	if err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	for _, r := range remotes {
		if r.Kind() == forge.GitHub && r.Host != forge.GitHubHost {
			elsewhere = true
			continue
		}
		if k := r.Key(); !seen[k] {
			seen[k] = true
			names = append(names, k)
		}
	}
	return names, elsewhere || odd, nil
}

// Hosts is the hosts that dirs' remotes are on that the searches cover:
// github.com, and each GitLab host. Searches run once a host.
func Hosts(dirs []string) []string {
	set := map[string]bool{}
	for _, d := range dirs {
		remotes, _, err := Remotes(d)
		if err != nil {
			continue
		}
		for _, r := range remotes {
			if r.Host == forge.GitHubHost || r.Kind() == forge.GitLab {
				set[r.Host] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Search is what a search across hosts found: Found by host, and why a
// host could not be searched. A host neither found nor failed was not
// searched: none of the repositories is on it.
type Search[T any] struct {
	Found  map[string]T
	Failed map[string]error
}

// Searched is whether host answered.
func (s Search[T]) Searched(host string) bool {
	_, ok := s.Found[host]
	return ok
}

// Err is the hosts' failures in one, each said with its host when there
// was more than one to ask; nil when every host answered.
func (s Search[T]) Err() error {
	if len(s.Failed) == 0 {
		return nil
	}
	var hosts []string
	for h := range s.Failed {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	var msgs []string
	for _, h := range hosts {
		msg := s.Failed[h].Error()
		if len(s.Failed)+len(s.Found) > 1 {
			msg = h + ": " + msg
		}
		msgs = append(msgs, msg)
	}
	return errors.New(strings.Join(msgs, "; "))
}

// searchHosts runs github's search on github.com and gitlab's on each
// GitLab host, at once.
func searchHosts[T any](hosts []string, gh func() (T, error), gl func(host string) (T, error)) Search[T] {
	s := Search[T]{Found: map[string]T{}, Failed: map[string]error{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	asks := map[string]func() (T, error){}
	for _, h := range hosts {
		switch {
		case h == forge.GitHubHost:
			if !github.Available() {
				s.Failed[h] = errors.New("the gh CLI is not installed")
				continue
			}
			asks[h] = gh
		case forge.KindOf(h) == forge.GitLab:
			if !gitlab.Available() {
				s.Failed[h] = errors.New(gitlab.NotInstalled)
				continue
			}
			asks[h] = func() (T, error) { return gl(h) }
		}
	}
	// Every host is sorted out before the first is asked: the asks write
	// to s as they answer.
	for h, ask := range asks {
		wg.Add(1)
		go func(h string, ask func() (T, error)) {
			defer wg.Done()
			v, err := ask()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				s.Failed[h] = err
				return
			}
			s.Found[h] = v
		}(h, ask)
	}
	wg.Wait()
	return s
}

// ReposWithMyPRs is, by host, the repositories (forge.RepoKey) you have
// open pull requests in.
func ReposWithMyPRs(hosts []string) Search[map[string]bool] {
	return searchHosts(hosts, github.ReposWithMyPRs, gitlab.ReposWithMyPRs)
}

// ReviewRequested is, by host, the open pull requests your review is
// asked for (forge.RequestKey).
func ReviewRequested(hosts []string) Search[map[string]bool] {
	return searchHosts(hosts, github.ReviewRequested, gitlab.ReviewRequested)
}

// MyIssues is, by host, the open issues assigned to you.
func MyIssues(hosts []string) Search[[]forge.Issue] {
	return searchHosts(hosts, github.MyIssues, gitlab.MyIssues)
}

// CloneRepo clones the repository at path on host (on its service, with
// its CLI and the login it has) into a folder named name in parent.
func CloneRepo(parent, host, path, name string) (string, error) {
	if forge.KindOf(host) == forge.GitLab {
		return gitlab.CloneRepo(parent, host, path, name)
	}
	return github.CloneRepo(parent, host, path, name)
}

// CreateRepo makes a private repository on host's service, named as dir
// is, makes it dir's origin and pushes to it; it returns its page.
func CreateRepo(dir, host string) (string, error) {
	defer Forget(dir)
	if forge.KindOf(host) == forge.GitLab {
		return gitlab.CreateRepo(dir, host)
	}
	return github.CreateRepo(dir, host)
}

// CanMake is whether the CLI that makes and clones repositories on host
// is installed.
func CanMake(host string) bool {
	if forge.KindOf(host) == forge.GitLab {
		return gitlab.Available()
	}
	return github.Available()
}
