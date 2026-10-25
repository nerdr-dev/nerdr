package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// This file is what nerdr does with GitHub for a tab, rather than what the
// review panel shows: a light look at the branch's PR for the tab bar,
// opening a PR, and reading an issue to start a tab from.

// ghTimeout bounds each gh call made on nerdr's own behalf: one that
// hangs, on a network that does not answer, must not keep what waits on
// it — the PR watch, the close dialog — waiting for ever. ghLongTimeout is
// for one that reads a lot: every page of a PR's comments, a run's log.
var (
	ghTimeout     = 45 * time.Second
	ghLongTimeout = 2 * time.Minute
)

// gh runs the gh CLI in dir, without the terminal and with prompts off: it
// runs behind nerdr's screen, where nobody could answer one.
func gh(dir string, args ...string) ([]byte, error) {
	return ghWithin(ghTimeout, dir, args...)
}

// ghWithin is gh with a time limit of its own, for what takes longer.
func ghWithin(timeout time.Duration, dir string, args ...string) ([]byte, error) {
	if !Available() {
		return nil, errors.New("the gh CLI is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = append(gitsafe.Env(dir), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	proc.Detach(cmd)
	out, err := proc.Output(cmd)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.New("gh timed out")
		}
		return nil, errors.New(ghErrorMessage(err))
	}
	return out, nil
}

// Status is the short state of a branch's PR, for the tab bar.
type Status struct {
	Number  int
	URL     string
	State   string // OPEN, CLOSED, MERGED
	CI      string // see CISummary
	HeadOID string // the PR's last head, including after a squash/rebase merge
}

// BranchStatus looks up the PR for the branch checked out in dir. ok is
// false when there is none (or gh cannot tell).
func BranchStatus(dir string) (st Status, ok bool) {
	out, err := gh(dir, "pr", "view", "--json", "number,url,state,headRefOid,statusCheckRollup")
	if err != nil {
		return Status{}, false
	}
	var raw rawPR
	if json.Unmarshal(out, &raw) != nil || !ownPR(dir, raw.State, raw.HeadRefOid) {
		return Status{}, false
	}
	pr := raw.toPR()
	return Status{Number: pr.Number, URL: pr.URL, State: pr.State, CI: CISummary(pr.Checks), HeadOID: pr.Head}, true
}

// ownPR reports whether a PR that gh found by the name of the branch
// checked out in dir is this branch's. gh also finds a merged or closed PR
// from an earlier branch of the same name, deleted since, and a new branch
// from main must not show as merged for it. A finished PR is this branch's
// only when the branch has nothing the PR did not: HEAD is its head commit,
// or behind it. An open PR is always the branch's.
func ownPR(dir, state, head string) bool {
	if state == "OPEN" {
		return true
	}
	if head == "" {
		return false
	}
	return proc.Run(gitsafe.Command(dir, "merge-base", "--is-ancestor", "HEAD", head)) == nil
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

// CreatePR opens a pull request for the branch checked out in dir, titled
// and described from its commits, and returns its URL. The branch must be
// pushed first, to origin, whose URL nerdr recorded: the PR goes to that
// repository, named to gh, rather than one gh would pick from the remotes
// the worktree has now (it prefers one called upstream).
func CreatePR(dir, origin string) (string, error) {
	repo, ok := repoArg(origin)
	if !ok {
		return "", errors.New("cannot tell which repository origin (" + origin + ") is")
	}
	args := []string{"pr", "create", "--fill", "--repo", repo}
	if b := git.StackedBase(dir); b != "" && remoteHas(dir, b) {
		// A stacked branch's pull request is against the one it is on,
		// as long as that is on GitHub; GitHub moves it to main when that
		// one is merged.
		args = append(args, "--base", b)
	}
	out, err := gh(dir, args...)
	if err != nil {
		return "", err
	}
	return lastURL(string(out)), nil
}

// remoteHas says whether origin has branch b, as far as the local copy of
// its branches knows.
func remoteHas(dir, b string) bool {
	return proc.Run(gitsafe.Command(dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+b)) == nil
}

// repoArg is how gh's --repo names the repository at a remote URL:
// owner/name on github.com, host/owner/name on another host (GitHub
// Enterprise).
func repoArg(url string) (string, bool) {
	if name, ok := gitHubName(url); ok {
		return name, true
	}
	var host, path string
	if scheme, rest, ok := strings.Cut(url, "://"); ok && scheme != "file" {
		host, path, _ = strings.Cut(rest, "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		host, _, _ = strings.Cut(host, ":") // a port
	} else if before, after, ok := strings.Cut(url, ":"); ok && !strings.Contains(before, "/") {
		// scp-like: [user@]host:owner/name
		host, path = before, after
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if host == "" || !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return host + "/" + owner + "/" + name, true
}

var urlRe = regexp.MustCompile(`https://\S+`)

func lastURL(s string) string {
	all := urlRe.FindAllString(s, -1)
	if len(all) == 0 {
		return strings.TrimSpace(s)
	}
	return all[len(all)-1]
}

// Issue is a GitHub issue to start a tab from. Body is only filled in by
// FetchIssue: the lists leave it out.
type Issue struct {
	Number    int
	Title     string
	Body      string
	URL       string
	Author    string
	Labels    []string
	UpdatedAt time.Time
	// Repo is the repository it is in ("owner/name", lower case), filled in
	// by MyIssues only.
	Repo string
}

// FetchIssue reads issue number from the repository in dir.
func FetchIssue(dir string, number int) (Issue, error) {
	out, err := gh(dir, "issue", "view", strconv.Itoa(number), "--json", issueFields+",body")
	if err != nil {
		return Issue{}, err
	}
	var raw rawIssue
	if json.Unmarshal(out, &raw) != nil {
		return Issue{}, errors.New("could not parse the output from gh")
	}
	return raw.toIssue(), nil
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
	s := "Resolve GitHub issue #" + strconv.Itoa(is.Number) + ": " + is.Title
	if is.URL != "" {
		s += " (" + is.URL + ")"
	}
	if body := strings.TrimSpace(is.Body); body != "" {
		s += "\n\n" + body
	}
	return s
}

// cloneTimeout bounds a clone made with gh, as git's own clone is.
const cloneTimeout = 15 * time.Minute

// CloneRepo clones repo, owner/name on GitHub, into a folder named name in
// parent, with gh, so the login gh has reaches a private repository too.
func CloneRepo(parent, repo, name string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	if _, err := ghWithin(cloneTimeout, parent, "repo", "clone", repo, name, "--", "--quiet"); err != nil {
		return "", err
	}
	return filepath.Join(parent, name), nil
}

// CreateRepo makes a private repository on GitHub, named as the folder
// dir is, makes it dir's origin and pushes dir's branch to it. It returns
// the new repository's URL.
func CreateRepo(dir string) (string, error) {
	out, err := gh(dir, "repo", "create", filepath.Base(dir), "--private",
		"--source", ".", "--remote", "origin", "--push")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(firstLine(string(out))), nil
}
