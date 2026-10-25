// Package github fetches pull-request status (reviews, checks, comments) for
// a worktree's checked-out branch via the `gh` CLI, the same way the git
// package shells out to `git` — no API tokens to manage, it uses whatever
// `gh auth` session the user already has.
package github

import (
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// PR describes a pull request: the one open for a worktree's branch, or one
// of the repository's PRs in the overview.
type PR struct {
	Number         int
	Title          string
	URL            string
	State          string // OPEN, CLOSED, MERGED
	IsDraft        bool
	Author         string
	Branch         string
	Head           string // the branch's head commit
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
	// from a fork: the branch is not on origin, only refs/pull/N/head is.
	Fork string
}

// Review is one reviewer's latest verdict.
type Review struct {
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	Body        string
	SubmittedAt time.Time
	URL         string
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
}

// Check is one CI check/status, normalised across GitHub Actions check runs
// and legacy commit statuses (which use different field names).
type Check struct {
	Name       string
	Status     string // SUCCESS, FAILURE, PENDING, NEUTRAL, SKIPPED, CANCELLED, ERROR, ""
	DetailsURL string
}

// Available reports whether the gh CLI is installed.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// CurrentPR fetches the PR for worktreePath's checked-out branch. ok is
// false when gh isn't installed, the repo has no GitHub remote, or there's
// no PR for the branch — msg explains which (gh's own message, which is
// already short and clear for these cases).
func CurrentPR(worktreePath string) (pr PR, ok bool, msg string) {
	if !Available() {
		return PR{}, false, "the gh CLI is not installed"
	}
	pr, err := viewPR(worktreePath)
	if err != nil {
		return PR{}, false, err.Error()
	}
	if !ownPR(worktreePath, pr.State, pr.Head) {
		return PR{}, false, "no pull request for this branch; #" + strconv.Itoa(pr.Number) +
			" is from an earlier branch of the same name"
	}
	return pr, true, ""
}

// FetchPR fetches one PR in full, including comment bodies — what ListPRs
// deliberately leaves out.
func FetchPR(repoPath string, number int) (PR, error) {
	if !Available() {
		return PR{}, errors.New("the gh CLI is not installed")
	}
	return viewPR(repoPath, strconv.Itoa(number))
}

// viewPR runs `gh pr view` for the branch checked out in dir, or for the
// PR named by args.
func viewPR(dir string, args ...string) (PR, error) {
	argv := append([]string{"pr", "view"}, args...)
	argv = append(argv, "--json", prViewFields)
	out, err := gh(dir, argv...)
	if err != nil {
		return PR{}, err
	}
	var raw rawPR
	if json.Unmarshal(out, &raw) != nil {
		return PR{}, errors.New("could not parse the output from gh")
	}
	pr := raw.toPR()
	for _, rv := range raw.Reviews {
		if strings.TrimSpace(rv.Body) != "" {
			pr.Said = append(pr.Said, rv.toReview())
		}
	}
	// gh pr view has the conversation's comments, not those on the code's
	// lines, which are most of a review's: those come from the API.
	pr.Comments = append(pr.Comments, lineComments(dir, pr.URL)...)
	sort.SliceStable(pr.Comments, func(i, j int) bool { return pr.Comments[i].CreatedAt.Before(pr.Comments[j].CreatedAt) })
	return pr, nil
}

// prPathRe takes the repository and number out of a PR's URL.
var prPathRe = regexp.MustCompile(`^https://[^/]+/([^/]+/[^/]+)/pull/(\d+)`)

// lineComments are the comments on the lines of the PR at url; none when
// they cannot be read, as the rest of the PR still can.
func lineComments(dir, url string) []Comment {
	mm := prPathRe.FindStringSubmatch(url)
	if mm == nil {
		return nil
	}
	// Every page of them: given longer than one call.
	out, err := ghWithin(ghLongTimeout, dir, "api", "--paginate", "repos/"+mm[1]+"/pulls/"+mm[2]+"/comments",
		"--jq", `.[] | {author: .user.login, body, created: .created_at, url: .html_url, path, line: (.line // 0)}`)
	if err != nil {
		return nil
	}
	var cs []Comment
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var c struct {
			Author, Body, URL, Path string
			Created                 time.Time
			Line                    int
		}
		if dec.Decode(&c) != nil {
			return cs
		}
		cs = append(cs, Comment{Author: c.Author, Body: c.Body, CreatedAt: c.Created, URL: c.URL, Path: c.Path, Line: c.Line})
	}
}

const (
	prViewFields = "number,title,url,state,isDraft,author,updatedAt,headRefName,headRefOid," +
		"isCrossRepository,headRepositoryOwner,mergeable,reviewDecision,latestReviews,reviews,statusCheckRollup,comments"

	// Comments come with the list so a row can say whether a PR has any
	// without opening it — a question you want answered for the whole list
	// at once. It is the heaviest field by far, which is affordable only
	// because the list is open PRs of one author: a handful, not hundreds.
	// Each reviewer's latest review and the branch's head commit come too:
	// nerdr watches the list for checks that come to fail and reviews that
	// come in (see the app's prwatch.go).
	prListFields = "number,title,url,state,isDraft,author,updatedAt,headRefName,headRefOid," +
		"isCrossRepository,headRepositoryOwner,reviewDecision,latestReviews,statusCheckRollup,comments"

	// prAllFields is what the list of everyone's pull requests asks for:
	// that of yours without the comments, which would be heavy for a
	// repository's every open one.
	prAllFields = "number,title,url,state,isDraft,author,updatedAt,headRefName,headRefOid," +
		"isCrossRepository,headRepositoryOwner,reviewDecision,latestReviews,statusCheckRollup"
)

// NotOnGitHub reports whether err, from asking gh about a repository, is
// because the repository has no GitHub remote: asking again will not help.
func NotOnGitHub(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no git remotes") || strings.Contains(msg, "known github host")
}

// Failed reports whether a check's Status means it failed.
func Failed(status string) bool {
	switch strings.ToUpper(status) {
	case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	}
	return false
}

// ListMyPRs returns the open pull requests you opened on this repository,
// most recently updated first. Merged and closed ones are done with: they
// would only bury the ones still waiting on someone. limit caps how many
// GitHub is asked for.
func ListMyPRs(repoPath string, limit int) ([]PR, error) {
	if !Available() {
		return nil, errors.New("the gh CLI is not installed")
	}
	return listPRs(repoPath, "--state", "open", "--author", "@me",
		"--limit", strconv.Itoa(limit), "--json", prListFields)
}

// ListPRs returns the repository's open pull requests, whoever opened
// them, most recently updated first; limit caps how many GitHub is asked
// for.
func ListPRs(repoPath string, limit int) ([]PR, error) {
	if !Available() {
		return nil, errors.New("the gh CLI is not installed")
	}
	return listPRs(repoPath, "--state", "open", "--limit", strconv.Itoa(limit), "--json", prAllFields)
}

// listPRs is `gh pr list` with args, most recently updated first.
func listPRs(repoPath string, args ...string) ([]PR, error) {
	out, err := gh(repoPath, append([]string{"pr", "list"}, args...)...)
	if err != nil {
		return nil, err
	}
	var raws []rawPR
	if json.Unmarshal(out, &raws) != nil {
		return nil, errors.New("could not parse the output from gh")
	}
	prs := make([]PR, 0, len(raws))
	for _, r := range raws {
		prs = append(prs, r.toPR())
	}
	// gh lists by creation order; what matters when reviewing is what moved
	// last.
	sort.SliceStable(prs, func(i, j int) bool { return prs[j].UpdatedAt.Before(prs[i].UpdatedAt) })
	return prs, nil
}

// BranchPR is the newest pull request, in any state, from branch in the
// repository at repoPath, and the commit its head was at last. ok is false
// when there is none, or gh cannot say.
func BranchPR(repoPath, branch string) (number int, state, head string, ok bool) {
	if !Available() {
		return 0, "", "", false
	}
	out, err := gh(repoPath, "pr", "list", "--head", branch, "--state", "all",
		"--limit", "1", "--json", "number,state,headRefOid")
	if err != nil {
		return 0, "", "", false
	}
	var prs []struct {
		Number     int
		State      string
		HeadRefOid string `json:"headRefOid"`
	}
	if json.Unmarshal(out, &prs) != nil || len(prs) == 0 {
		return 0, "", "", false
	}
	return prs[0].Number, prs[0].State, prs[0].HeadRefOid, true
}

// ForkPR is the open pull request from a fork whose branch is named
// branch, in the repository at repoPath: a branch of that name made here
// would not be it. ok is false when there is none, or gh cannot say.
func ForkPR(repoPath, branch string) (number int, owner string, ok bool) {
	if !Available() {
		return 0, "", false
	}
	out, err := gh(repoPath, "pr", "list", "--head", branch, "--state", "open",
		"--limit", "10", "--json", "number,isCrossRepository,headRepositoryOwner")
	if err != nil {
		return 0, "", false
	}
	var prs []struct {
		Number            int       `json:"number"`
		IsCrossRepository bool      `json:"isCrossRepository"`
		HeadOwner         rawAuthor `json:"headRepositoryOwner"`
	}
	if json.Unmarshal(out, &prs) != nil {
		return 0, "", false
	}
	for _, pr := range prs {
		if pr.IsCrossRepository {
			return pr.Number, forkOwner(true, pr.HeadOwner.Login), true
		}
	}
	return 0, "", false
}

func ghErrorMessage(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		msg := strings.TrimSpace(string(ee.Stderr))
		if NotLoggedIn(msg) {
			return NotLoggedInMessage
		}
		if msg != "" {
			// gh's own messages are already short and clear, e.g.
			// `no pull requests found for branch "master"` or
			// `no git remotes found`.
			return firstLine(msg)
		}
	}
	return "gh failed: " + err.Error()
}

// NotLoggedInMessage is what nerdr says when gh has no GitHub login, or
// one GitHub no longer takes: gh's own message for it takes several lines
// and the first of them does not say what is wrong.
const NotLoggedInMessage = "gh is not logged in to GitHub: run gh auth login in a terminal"

// NotLoggedIn is whether gh's message stderr says it has no login, or one
// GitHub turned down.
func NotLoggedIn(stderr string) bool {
	return strings.Contains(stderr, "gh auth login") || strings.Contains(stderr, "HTTP 401")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

type rawAuthor struct {
	Login string `json:"login"`
}

type rawReview struct {
	Author      rawAuthor `json:"author"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	SubmittedAt time.Time `json:"submittedAt"`
	URL         string    `json:"url"`
}

func (rv rawReview) toReview() Review {
	return Review{Author: rv.Author.Login, State: rv.State, Body: rv.Body, SubmittedAt: rv.SubmittedAt, URL: rv.URL}
}

type rawComment struct {
	Author    rawAuthor `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	URL       string    `json:"url"`
}

// rawCheck covers both statusCheckRollup shapes: a GitHub Actions CheckRun
// (name/workflowName/status/conclusion) and a legacy commit StatusContext
// (context/state).
type rawCheck struct {
	Typename     string `json:"__typename"`
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Context      string `json:"context"`
	State        string `json:"state"`
	DetailsURL   string `json:"detailsUrl"`
	TargetURL    string `json:"targetUrl"`
}

func (c rawCheck) normalise() Check {
	if c.Typename == "StatusContext" {
		return Check{Name: c.Context, Status: c.State, DetailsURL: c.TargetURL}
	}
	name := c.Name
	if c.WorkflowName != "" {
		name = c.WorkflowName + " / " + c.Name
	}
	status := c.Conclusion
	if status == "" {
		status = c.Status // still running: QUEUED/IN_PROGRESS, no conclusion yet
	}
	return Check{Name: name, Status: status, DetailsURL: c.DetailsURL}
}

type rawPR struct {
	Number            int          `json:"number"`
	Title             string       `json:"title"`
	URL               string       `json:"url"`
	State             string       `json:"state"`
	IsDraft           bool         `json:"isDraft"`
	Author            rawAuthor    `json:"author"`
	UpdatedAt         time.Time    `json:"updatedAt"`
	HeadRefName       string       `json:"headRefName"`
	HeadRefOid        string       `json:"headRefOid"`
	IsCrossRepository bool         `json:"isCrossRepository"`
	HeadOwner         rawAuthor    `json:"headRepositoryOwner"`
	Mergeable         string       `json:"mergeable"`
	ReviewDecision    string       `json:"reviewDecision"`
	LatestReviews     []rawReview  `json:"latestReviews"`
	Reviews           []rawReview  `json:"reviews"`
	Comments          []rawComment `json:"comments"`
	StatusCheckRollup []rawCheck   `json:"statusCheckRollup"`
}

func (r rawPR) toPR() PR {
	pr := PR{
		Number:         r.Number,
		Title:          r.Title,
		URL:            r.URL,
		State:          r.State,
		IsDraft:        r.IsDraft,
		Author:         r.Author.Login,
		Branch:         r.HeadRefName,
		Head:           r.HeadRefOid,
		UpdatedAt:      r.UpdatedAt,
		Fork:           forkOwner(r.IsCrossRepository, r.HeadOwner.Login),
		Mergeable:      r.Mergeable,
		ReviewDecision: r.ReviewDecision,
	}
	for _, rv := range r.LatestReviews {
		pr.Reviews = append(pr.Reviews, rv.toReview())
	}
	for _, c := range r.Comments {
		pr.Comments = append(pr.Comments, Comment{
			Author: c.Author.Login, Body: c.Body, CreatedAt: c.CreatedAt, URL: c.URL,
		})
	}
	for _, c := range r.StatusCheckRollup {
		pr.Checks = append(pr.Checks, c.normalise())
	}
	return pr
}

// forkOwner is PR.Fork: the head's owner when the head is another
// repository's, "" when it is the repository's own.
func forkOwner(cross bool, owner string) string {
	switch {
	case !cross:
		return ""
	case owner == "":
		return "a fork"
	}
	return owner
}

// runIDRe pulls the workflow run out of a check's details URL, which is
// what `gh run view` needs; the check itself carries no run id.
var runIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// FailedRunLogs returns the logs of the failed jobs behind checks, trimmed
// to something an agent can actually be given: a full job log runs to
// hundreds of kilobytes, nearly all of it setup noise.
func FailedRunLogs(repoPath string, checks []Check, limit int) (string, error) {
	if !Available() {
		return "", errors.New("the gh CLI is not installed")
	}
	seen := map[string]bool{}
	var out strings.Builder
	for _, c := range checks {
		switch strings.ToUpper(c.Status) {
		case "FAILURE", "ERROR", "TIMED_OUT", "ACTION_REQUIRED":
		default:
			continue
		}
		m := runIDRe.FindStringSubmatch(c.DetailsURL)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true

		raw, err := ghWithin(ghLongTimeout, repoPath, "run", "view", m[1], "--log-failed")
		if err != nil {
			out.WriteString("(could not read the log for " + c.Name + ": " + err.Error() + ")\n")
			continue
		}
		out.WriteString("--- " + c.Name + " (run " + m[1] + ") ---\n")
		out.WriteString(condenseLog(string(raw), limit))
		out.WriteString("\n")
	}
	if out.Len() == 0 {
		return "", errors.New("no failed workflow run to read")
	}
	return out.String(), nil
}

// condenseLog keeps the part of a job log that says what went wrong. A
// full job log is hundreds of kilobytes, and neither end of it is the
// answer: the head is runner provisioning, and the tail is post-job
// cleanup — checking out, unsetting git credentials — which runs *after*
// the failure. What matters is the output leading up to each line the
// runner marked "##[error]", so that is the window kept.
func condenseLog(log string, limit int) string {
	if limit <= 0 {
		limit = 8000
	}
	lines := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")

	// Lines of context before an error marker. Enough to hold a compiler's
	// or a test runner's output, short enough not to reach back into
	// dependency downloads.
	const context = 80

	var windows [][2]int
	for i, line := range lines {
		if !strings.Contains(line, "##[error]") {
			continue
		}
		start := max(0, i-context)
		if n := len(windows); n > 0 && start <= windows[n-1][1]+1 {
			windows[n-1][1] = i // overlaps the previous window: merge
			continue
		}
		windows = append(windows, [2]int{start, i})
	}
	if len(windows) == 0 {
		// Nothing was marked; the tail is the best guess left.
		return tailOf(log, limit)
	}

	var b strings.Builder
	for i, w := range windows {
		if i > 0 {
			b.WriteString("\n…\n")
		}
		b.WriteString(strings.Join(lines[w[0]:w[1]+1], "\n"))
	}
	// Later errors are usually the informative ones (the first may be a
	// tool warning), so trim from the front.
	return tailOf(b.String(), limit)
}

func tailOf(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[len(s)-limit:]
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:] // start on a whole line
	}
	return "…\n" + s
}

// searchLimit is the most pull requests the search behind ReposWithMyPRs
// returns; GitHub's search goes no further.
const searchLimit = 1000

// ReposWithMyPRs is the github.com repositories ("owner/name", lower case)
// you have open pull requests in, found with one search: so only those
// need asking for their pull requests, rather than every repository there
// is. The search can be a minute behind; a pull request just opened is
// found at a later look.
func ReposWithMyPRs() (map[string]bool, error) {
	if !Available() {
		return nil, errors.New("the gh CLI is not installed")
	}
	out, err := gh("", "search", "prs", "--author", "@me", "--state", "open",
		"--limit", strconv.Itoa(searchLimit), "--json", "repository")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
	}
	if json.Unmarshal(out, &prs) != nil {
		return nil, errors.New("could not parse the output from gh")
	}
	if len(prs) >= searchLimit {
		return nil, errors.New("too many pull requests to search")
	}
	repos := map[string]bool{}
	for _, pr := range prs {
		repos[strings.ToLower(pr.Repository.NameWithOwner)] = true
	}
	return repos, nil
}

// ReviewRequested is the open pull requests your review is asked for, on
// you or on a team of yours, as "owner/name#number" (lower case), found
// with one search. The search can be a minute behind.
func ReviewRequested() (map[string]bool, error) {
	if !Available() {
		return nil, errors.New("the gh CLI is not installed")
	}
	out, err := gh("", "search", "prs", "--review-requested", "@me", "--state", "open",
		"--limit", strconv.Itoa(searchLimit), "--json", "number,repository")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		Number     int `json:"number"`
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
	}
	if json.Unmarshal(out, &prs) != nil {
		return nil, errors.New("could not parse the output from gh")
	}
	asked := map[string]bool{}
	for _, pr := range prs {
		asked[RequestKey(pr.Repository.NameWithOwner, pr.Number)] = true
	}
	return asked, nil
}

// RequestKey is how ReviewRequested names a pull request.
func RequestKey(repo string, number int) string {
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// RepoNames is the github.com repositories ("owner/name", lower case) the
// remotes of the repository at repoPath point to, and whether it has a
// remote anywhere else: GitHub Enterprise, say, which the search behind
// ReposWithMyPRs does not cover.
func RepoNames(repoPath string) (names []string, elsewhere bool, err error) {
	out, err := proc.Output(gitsafe.Command(repoPath, "remote", "-v"))
	if err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, ok := gitHubName(fields[1])
		switch {
		case !ok:
			elsewhere = true
		case !seen[name]:
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, elsewhere, nil
}

// gitHubName is the "owner/name" (lower case) of a github.com remote URL,
// in any of the forms git takes: https://github.com/o/r.git,
// git@github.com:o/r.git, ssh://git@github.com/o/r.
func gitHubName(url string) (string, bool) {
	rest := ""
	for _, p := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:", "git://github.com/"} {
		if r, ok := strings.CutPrefix(url, p); ok {
			rest = r
			break
		}
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	owner, name, ok := strings.Cut(rest, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return strings.ToLower(owner + "/" + name), true
}
