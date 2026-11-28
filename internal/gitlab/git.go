package gitlab

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// What GitLab's answers are read with, and the git a merge request's
// branch is found with.

var jsonUnmarshal = json.Unmarshal

// currentBranch is the branch checked out in dir, "" when none is.
func currentBranch(dir string) string { return git.CurrentBranch(dir) }

// stackedBase is the branch the one checked out in dir is stacked on, ""
// when it is on none.
func stackedBase(dir string) string { return git.StackedBase(dir) }

// remoteHas says whether origin has branch b, as far as the local copy of
// its branches knows.
func remoteHas(dir, b string) bool {
	return proc.Run(gitsafe.Command(dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+b)) == nil
}

// forkMRNumber is the merge request from a fork that branch was fetched
// from (see workspace.FetchForkPR), 0 when it is not one.
func forkMRNumber(dir, branch string) int {
	out, err := proc.Output(gitsafe.Command(dir, "config", "--get", "branch."+branch+".merge"))
	if err != nil {
		return 0
	}
	ref := strings.TrimSpace(string(out))
	n, ok := strings.CutPrefix(ref, "refs/merge-requests/")
	if !ok {
		return 0
	}
	num, err := strconv.Atoi(strings.TrimSuffix(n, "/head"))
	if err != nil || !strings.HasSuffix(n, "/head") {
		return 0
	}
	return num
}
