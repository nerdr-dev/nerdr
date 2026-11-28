package gitlab

import (
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// rawIssue is an issue as the API has it.
type rawIssue struct {
	IID         int       `json:"iid"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	WebURL      string    `json:"web_url"`
	Author      rawUser   `json:"author"`
	Labels      []string  `json:"labels"`
	UpdatedAt   time.Time `json:"updated_at"`
	References  struct {
		Full string `json:"full"`
	} `json:"references"`
}

// toIssue is r in forge's words, Repo named as forge.RepoKey does when
// host is given.
func (r rawIssue) toIssue(host string) forge.Issue {
	is := forge.Issue{Kind: forge.GitLab, Number: r.IID, Title: r.Title, Body: r.Description, URL: r.WebURL,
		Author: r.Author.Username, Labels: r.Labels, UpdatedAt: r.UpdatedAt}
	if host != "" {
		if path, _, ok := cutRef(r.References.Full, "#"); ok {
			is.Repo = forge.RepoKey(host, path)
		}
	}
	return is
}

// cutRef splits a full reference, "group/name#12" or "group/name!12",
// into the project's path and the number.
func cutRef(full, sep string) (path string, n int, ok bool) {
	i := strings.LastIndex(full, sep)
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(full[i+1:])
	if err != nil {
		return "", 0, false
	}
	return full[:i], n, true
}

func sortIssues(issues []forge.Issue) {
	sort.SliceStable(issues, func(i, j int) bool { return issues[j].UpdatedAt.Before(issues[i].UpdatedAt) })
}

// FetchIssue reads issue n of origin.
func (f Forge) FetchIssue(dir string, n int) (forge.Issue, error) {
	if !Available() {
		return forge.Issue{}, errors.New(NotInstalled)
	}
	var r rawIssue
	if err := getJSON(dir, f.host(), project(f.Origin.Path)+"/issues/"+strconv.Itoa(n), &r); err != nil {
		return forge.Issue{}, err
	}
	return r.toIssue(""), nil
}

// ListIssues is origin's open issues, whoever they are assigned to, most
// recently updated first; limit caps how many are asked for.
func (f Forge) ListIssues(dir string, limit int) ([]forge.Issue, error) {
	if !Available() {
		return nil, errors.New(NotInstalled)
	}
	var raws []rawIssue
	err := getJSON(dir, f.host(), query(project(f.Origin.Path)+"/issues", url.Values{
		"state": {"opened"}, "order_by": {"updated_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(min(limit, 100))},
	}), &raws)
	if err != nil {
		return nil, err
	}
	issues := make([]forge.Issue, 0, len(raws))
	for _, r := range raws {
		issues = append(issues, r.toIssue(""))
	}
	sortIssues(issues)
	return issues, nil
}
