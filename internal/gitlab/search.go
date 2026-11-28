package gitlab

import (
	"errors"
	"net/url"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// The searches across a host, which the watches start from: one call
// (and its pages) however many projects there are. Every page is read,
// so a merge request not among them is not there.

// searchLimit is the most a search reads before giving up, as GitHub's
// stops at a thousand: a search that comes back short would make what it
// left out look gone.
const searchLimit = 1000

// ReposWithMyPRs is the projects on host ("host/path", as forge.RepoKey
// names them) you have open merge requests in.
func ReposWithMyPRs(host string) (map[string]bool, error) {
	mrs, err := search[rawMR](host, "merge_requests", url.Values{"scope": {"created_by_me"}, "state": {"opened"}})
	if err != nil {
		return nil, err
	}
	repos := map[string]bool{}
	for _, mr := range mrs {
		if path, _, ok := cutRef(mr.References.Full, "!"); ok {
			repos[forge.RepoKey(host, path)] = true
		}
	}
	return repos, nil
}

// ReviewRequested is the open merge requests on host you are a reviewer
// of, as forge.RequestKey names them. scope=all: without it GitLab looks
// only among your own.
func ReviewRequested(host string) (map[string]bool, error) {
	u, err := me(host)
	if err != nil {
		return nil, err
	}
	mrs, err := search[rawMR](host, "merge_requests", url.Values{
		"scope": {"all"}, "state": {"opened"}, "reviewer_username": {u.Username},
	})
	if err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, mr := range mrs {
		if path, n, ok := cutRef(mr.References.Full, "!"); ok {
			asked[forge.RequestKey(forge.RepoKey(host, path), n)] = true
		}
	}
	return asked, nil
}

// MyIssues is the open issues assigned to you on host, most recently
// updated first, each with its Repo.
func MyIssues(host string) ([]forge.Issue, error) {
	raws, err := search[rawIssue](host, "issues", url.Values{"scope": {"assigned_to_me"}, "state": {"opened"}})
	if err != nil {
		return nil, err
	}
	issues := make([]forge.Issue, 0, len(raws))
	for _, r := range raws {
		issues = append(issues, r.toIssue(host))
	}
	sortIssues(issues)
	return issues, nil
}

// search is every page of what path on host lists for v.
func search[T any](host, path string, v url.Values) ([]T, error) {
	if !Available() {
		return nil, errors.New(NotInstalled)
	}
	v.Set("per_page", "100")
	all, err := getAll[T]("", host, query(path, v), glabLongTimeout)
	if err != nil {
		return nil, err
	}
	if len(all) >= searchLimit {
		return nil, errors.New("too many to search")
	}
	return all, nil
}
