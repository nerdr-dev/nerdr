package github

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// issueFields is what the issue lists ask for: enough for a row, not the
// body, which can be long and is only needed for the one a tab starts from.
const issueFields = "number,title,url,author,labels,updatedAt"

type rawIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	Author    rawAuthor `json:"author"`
	UpdatedAt time.Time `json:"updatedAt"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

func (r rawIssue) toIssue() Issue {
	is := Issue{Number: r.Number, Title: r.Title, Body: r.Body, URL: r.URL,
		Author: r.Author.Login, UpdatedAt: r.UpdatedAt,
		Repo: strings.ToLower(r.Repository.NameWithOwner)}
	for _, l := range r.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

func parseIssues(out []byte) ([]Issue, error) {
	var raws []rawIssue
	if json.Unmarshal(out, &raws) != nil {
		return nil, errors.New("could not parse the output from gh")
	}
	issues := make([]Issue, 0, len(raws))
	for _, r := range raws {
		issues = append(issues, r.toIssue())
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[j].UpdatedAt.Before(issues[i].UpdatedAt) })
	return issues, nil
}

// MyIssues is the open issues assigned to you, on every github.com
// repository, found with one search: watching them costs one request
// however many repositories there are. The search can be a minute behind.
func MyIssues() ([]Issue, error) {
	out, err := gh("", "search", "issues", "--assignee", "@me", "--state", "open",
		"--limit", strconv.Itoa(searchLimit), "--json", issueFields+",repository")
	if err != nil {
		return nil, err
	}
	issues, err := parseIssues(out)
	if err == nil && len(issues) >= searchLimit {
		return nil, errors.New("too many issues to search")
	}
	return issues, err
}

// ListIssues is the repository's open issues, whoever they are assigned
// to, most recently updated first; limit caps how many GitHub is asked for.
func ListIssues(repoPath string, limit int) ([]Issue, error) {
	out, err := gh(repoPath, "issue", "list", "--state", "open",
		"--limit", strconv.Itoa(limit), "--json", issueFields)
	if err != nil {
		return nil, err
	}
	return parseIssues(out)
}
