package forge

import (
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// Recheck works out again, with git alone, whether st, a PR the service
// found for the branch checked out in dir, is the branch's, and how many
// commits the branch has after it, for a branch that moved since the
// service was asked.
func Recheck(dir string, st Status) (Status, bool) {
	st.After = 0
	if OwnPR(dir, st.State, st.HeadOID) {
		return st, true
	}
	if st.After = CommitsAfter(dir, st.HeadOID, st.Base); st.After == 0 {
		return Status{}, false
	}
	return st, true
}

// OwnPR reports whether a PR that the service found by the name of the
// branch checked out in dir is this branch's. The service also finds a
// merged or closed PR from an earlier branch of the same name, deleted
// since, and a new branch from main must not show as merged for it. A
// finished PR is this branch's only when the branch has nothing the PR
// did not: HEAD is its head commit, or behind it. An open PR is always
// the branch's.
func OwnPR(dir, state, head string) bool {
	if state == "OPEN" {
		return true
	}
	if head == "" {
		return false
	}
	return isAncestor(dir, "HEAD", head)
}

// CommitsAfter is, for a finished PR that OwnPR turned down, how many
// commits the branch checked out in dir has on top of the PR's head that
// base has not got: the branch went on after the PR was merged or closed.
// It is 0 when the branch did not, or it cannot be told: git does not
// know the head or base. The head has to be on HEAD's first-parent line,
// which a branch that went on from it keeps when it merges base in. A
// merge commit puts the head into base too, and so into every branch
// made from base since, the new branch of the same name among them; but
// in those it is the merge commit's second parent, off that line. Squash
// and rebase merges leave it out of base altogether.
func CommitsAfter(dir, head, base string) int {
	if head == "" || base == "" {
		return 0
	}
	var bases []string
	for _, ref := range []string{"refs/remotes/origin/" + base, "refs/heads/" + base} {
		if proc.Run(gitsafe.Command(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")) == nil {
			bases = append(bases, "^"+ref)
		}
	}
	if len(bases) == 0 {
		return 0
	}
	line, err := revCount(dir, "--first-parent", "HEAD", "^"+head+"^{commit}")
	if err != nil || line == 0 || revParse(dir, "HEAD~"+strconv.Itoa(line)) != revParse(dir, head+"^{commit}") {
		return 0
	}
	n, err := revCount(dir, append([]string{"HEAD", "^" + head + "^{commit}"}, bases...)...)
	if err != nil {
		return 0
	}
	return n
}

// Commits is "1 commit" or "n commits".
func Commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return strconv.Itoa(n) + " commits"
}

// Unowned is what is said of a PR that OwnPR turned down for the branch
// checked out in dir: that it is finished, and the branch went on after
// it, or that it is an earlier branch's of the same name.
func Unowned(dir string, pr PR) string {
	word := pr.Kind.PRWord()
	if n := CommitsAfter(dir, pr.Head, pr.Base); n > 0 {
		return "no open " + word + " for this branch; " + pr.Ref() + " is " +
			strings.ToLower(pr.State) + ", and the branch has " + Commits(n) + " after it"
	}
	return "no " + word + " for this branch; " + pr.Ref() + " is from an earlier branch of the same name"
}

// revCount is how many commits git rev-list lists for args.
func revCount(dir string, args ...string) (int, error) {
	out, err := proc.Output(gitsafe.Command(dir, append([]string{"rev-list", "--count"}, args...)...))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// revParse is the commit rev names, "" when git does not know it.
func revParse(dir, rev string) string {
	out, err := proc.Output(gitsafe.Command(dir, "rev-parse", "--verify", "--quiet", rev))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// isAncestor says whether commit a is in b's history, a itself included.
func isAncestor(dir, a, b string) bool {
	return proc.Run(gitsafe.Command(dir, "merge-base", "--is-ancestor", a, b)) == nil
}
