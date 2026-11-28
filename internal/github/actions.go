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

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/statedir"
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
	return ghEnv(timeout, dir, nil, args...)
}

// ghEnv is ghWithin with env added to gh's environment.
func ghEnv(timeout time.Duration, dir string, env []string, args ...string) ([]byte, error) {
	return ghBase(timeout, dir, gitsafe.Env(dir), env, args...)
}

// ghBase is ghEnv on base, the environment for git, rather than that
// gitsafe gives for dir.
func ghBase(timeout time.Duration, dir string, base, env []string, args ...string) ([]byte, error) {
	if !Available() {
		return nil, errors.New("the gh CLI is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = append(base, "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	cmd.Env = append(cmd.Env, env...)
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

// BranchStatus looks up the PR for the branch checked out in dir. ok is
// false when there is none (or gh cannot tell).
func BranchStatus(dir string) (st Status, ok bool) {
	out, err := gh(dir, "pr", "view", "--json", "number,url,state,headRefOid,baseRefName,statusCheckRollup")
	if err != nil {
		return Status{}, false
	}
	var raw rawPR
	if json.Unmarshal(out, &raw) != nil {
		return Status{}, false
	}
	pr := raw.toPR()
	return forge.Recheck(dir, Status{Number: pr.Number, URL: pr.URL, State: pr.State, CI: forge.CISummary(pr.Checks), HeadOID: pr.Head, Base: pr.Base})
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
	// gh pr create counts the uncommitted changes with a git status of
	// its own, which nerdr cannot give --ignore-submodules: it would run
	// git inside every gitlink's folder, with that folder's config, an
	// agent's to write (see gitsafe's ignoreDirtySubmodules), and a
	// .gitmodules there overrides the setting in the environment. gh
	// needs the commits alone, so it is given an index that is not there:
	// with no gitlink in it, the status looks into no folder's repository.
	tmp, err := statedir.MkdirTemp("nerdr-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	out, err := ghEnv(ghTimeout, dir, []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}, args...)
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

// cloneTimeout bounds a clone made with gh, as git's own clone is.
const cloneTimeout = 15 * time.Minute

// CloneRepo clones repo, owner/name on host (github.com or a GitHub
// Enterprise server), into a folder named name in parent, with gh, so the
// login gh has reaches a private repository too.
func CloneRepo(parent, host, repo, name string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	// Not pinned to a repository parent is inside: see gitsafe.EnvNoRepo.
	if _, err := ghBase(cloneTimeout, parent, gitsafe.EnvNoRepo(), hostEnv(host), "repo", "clone", repo, name, "--", "--quiet"); err != nil {
		return "", err
	}
	return filepath.Join(parent, name), nil
}

// hostEnv has gh work on host, github.com too: a GH_HOST the user has
// set, for a GitHub Enterprise server, must not take its place.
func hostEnv(host string) []string {
	if host == "" {
		host = forge.GitHubHost
	}
	return []string{"GH_HOST=" + host}
}

// CreateRepo makes a private repository on host, github.com or a GitHub
// Enterprise server, named as the folder dir is, makes it dir's origin
// and pushes dir's branch to it. It returns the new repository's URL.
func CreateRepo(dir, host string) (string, error) {
	out, err := ghEnv(ghTimeout, dir, hostEnv(host), "repo", "create", filepath.Base(dir), "--private",
		"--source", ".", "--remote", "origin", "--push")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(firstLine(string(out))), nil
}
