// Package forge is what nerdr knows of a pull request, its reviews and
// checks, and an issue, whichever service the repository is on — GitHub
// or GitLab — and the git-only reasoning about a branch and its pull
// request that is the same on each. The github and gitlab packages fill
// these in from their CLIs, gh and glab; the forges package picks which
// one a repository is on.
//
// The words nerdr keeps a pull request's state in are GitHub's (OPEN,
// MERGED, SUCCESS, APPROVED, …): GitLab's are turned into them, so the
// rest of nerdr reads one vocabulary.
package forge

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Kind is the service a repository is on. The zero value is GitHub, which
// a pull request kept from before nerdr knew another is.
type Kind string

const (
	GitHub Kind = ""
	GitLab Kind = "gitlab"
)

// Name is the service's own name.
func (k Kind) Name() string {
	if k == GitLab {
		return "GitLab"
	}
	return "GitHub"
}

// CLI is the program nerdr asks the service with.
func (k Kind) CLI() string {
	if k == GitLab {
		return "glab"
	}
	return "gh"
}

// PRWord is what the service calls a pull request.
func (k Kind) PRWord() string {
	if k == GitLab {
		return "merge request"
	}
	return "pull request"
}

// PRTitle is PRWord with a capital, to start a sentence with.
func (k Kind) PRTitle() string {
	w := k.PRWord()
	return strings.ToUpper(w[:1]) + w[1:]
}

// PRShort is PRWord's short form.
func (k Kind) PRShort() string {
	if k == GitLab {
		return "MR"
	}
	return "PR"
}

// Ref is how the service writes pull request n: #12 on GitHub, !12 on
// GitLab, where #12 is an issue.
func (k Kind) Ref(n int) string {
	if k == GitLab {
		return "!" + strconv.Itoa(n)
	}
	return "#" + strconv.Itoa(n)
}

// ErrUnsupported is what a service says when it cannot do what it was
// asked: errors.Is it to tell that from a failure.
var ErrUnsupported = errors.New("not supported here")

// PR describes a pull request: the one open for a worktree's branch, or one
// of the repository's PRs in the overview.
type PR struct {
	Kind           Kind `json:",omitempty"`
	Number         int
	Title          string
	URL            string
	State          string // OPEN, CLOSED, MERGED
	IsDraft        bool
	Author         string
	Branch         string
	Head           string // the branch's head commit
	Base           string // the branch it is to be merged into
	UpdatedAt      time.Time
	Mergeable      string // MERGEABLE, CONFLICTING, UNKNOWN
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	Reviews        []Review
	// Said is every review that came with words, the earlier ones of a
	// reviewer too, oldest first; only a PR fetched by itself has them.
	// Reviews has each reviewer's latest only.
	Said     []Review
	Comments []Comment
	Checks   []Check
	// Fork is the owner of the fork the branch is in, for a pull request
	// from a fork: the branch is not on origin, only the service's ref
	// for the pull request is (see forges.PRRef).
	Fork string
}

// Ref is how the pull request is written: #12, or !12 on GitLab.
func (pr PR) Ref() string { return pr.Kind.Ref(pr.Number) }

// Review is one reviewer's latest verdict.
type Review struct {
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	Body        string
	SubmittedAt time.Time
	URL         string
	// Association is how the reviewer stands to the repository, as GitHub
	// says it: OWNER, MEMBER, COLLABORATOR, CONTRIBUTOR, NONE and others.
	// GitLab's roles are said in these words too. See Trusted.
	Association string
}

// Comment is a comment in a PR's conversation, or on a line of its code:
// then Path and Line say where. Line is 0 for one on a line no longer in
// the change, or on the whole file.
type Comment struct {
	Author    string
	Body      string
	CreatedAt time.Time
	URL       string
	Path      string
	Line      int
	// Association is as Review's.
	Association string
}

// Check is one CI check/status, normalised across GitHub Actions check
// runs, legacy commit statuses and GitLab's pipeline jobs.
type Check struct {
	Name       string
	Status     string // SUCCESS, FAILURE, PENDING, NEUTRAL, SKIPPED, CANCELLED, ERROR, ""
	DetailsURL string
}

// Trusted reports whether someone who stands to a repository as
// association (a Review's or Comment's) has a say in it: its owner, a
// member of the organisation that owns it, or a collaborator on it. A
// contributor of an earlier pull request, or anyone else, has not.
func Trusted(association string) bool {
	switch strings.ToUpper(association) {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

// Failed reports whether a check's Status means it failed.
func Failed(status string) bool {
	switch strings.ToUpper(status) {
	case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	}
	return false
}

// CISummary folds a PR's checks into one word: "fail" when any failed,
// else "pending" while any runs, else "pass"; "" when there are none.
func CISummary(checks []Check) string {
	if len(checks) == 0 {
		return ""
	}
	pending := false
	for _, c := range checks {
		switch strings.ToUpper(c.Status) {
		case "FAILURE", "ERROR", "TIMED_OUT", "ACTION_REQUIRED", "CANCELLED", "STARTUP_FAILURE":
			return "fail"
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		default:
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "pass"
}

// Status is the short state of a branch's PR, for the tab bar.
type Status struct {
	Kind    Kind `json:",omitempty"`
	Number  int
	URL     string
	State   string // OPEN, CLOSED, MERGED
	CI      string // see CISummary
	HeadOID string // the PR's last head, including after a squash/rebase merge
	Base    string // the branch the PR is to be merged into
	// After is how many commits the branch has on top of a merged or
	// closed PR's head: work carried on after it, which the PR has not
	// got. 0 for an open PR.
	After int
}

// Ref is how the pull request is written: #12, or !12 on GitLab.
func (st Status) Ref() string { return st.Kind.Ref(st.Number) }

// Issue is an issue of a repository.
type Issue struct {
	Kind      Kind `json:",omitempty"`
	Number    int
	Title     string
	Body      string
	URL       string
	Author    string
	Labels    []string
	UpdatedAt time.Time
	// Repo is the repository it is in, as RepoKey names it, filled in by
	// the searches for your issues only.
	Repo string
}

// BranchName suggests a branch for the issue: its number and the first
// words of its title, e.g. 123-login-fails-on-empty-password.
func (is Issue) BranchName() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(is.Number))
	words := 0
	dash := true
	for _, r := range strings.ToLower(is.Title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash {
				if words == 6 {
					return b.String()
				}
				b.WriteByte('-')
				words++
				dash = false
			}
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}

// Task is what the agent is asked to do about the issue.
func (is Issue) Task() string {
	s := "Resolve " + is.Kind.Name() + " issue #" + strconv.Itoa(is.Number) + ": " + is.Title
	if is.URL != "" {
		s += " (" + is.URL + ")"
	}
	if body := strings.TrimSpace(is.Body); body != "" {
		s += "\n\n" + body
	}
	return s
}

// RequestKey is how the searches for the pull requests your review is
// asked for name one: repo as RepoKey names it, and its number.
func RequestKey(repo string, number int) string {
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// FirstLine is s up to its first line break.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
