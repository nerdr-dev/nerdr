package github

import (
	"fmt"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// Forge is GitHub, github.com or GitHub Enterprise, as forges asks a
// repository's service: gh finds which host from the repository's
// remotes itself.
type Forge struct{}

func (Forge) Kind() forge.Kind { return forge.GitHub }
func (Forge) Available() bool  { return Available() }
func (Forge) NoRepo(err error) bool {
	return NotOnGitHub(err)
}
func (Forge) CurrentPR(dir string) (forge.PR, bool, string) { return CurrentPR(dir) }
func (Forge) FetchPR(dir string, n int) (forge.PR, error)   { return FetchPR(dir, n) }
func (Forge) ListMyPRs(dir string, limit int) ([]forge.PR, error) {
	return ListMyPRs(dir, limit)
}
func (Forge) ListPRs(dir string, limit int) ([]forge.PR, error) { return ListPRs(dir, limit) }
func (Forge) BranchPR(dir, branch string) (int, string, string, bool) {
	return BranchPR(dir, branch)
}
func (Forge) ForkPR(dir, branch string) (int, string, bool) { return ForkPR(dir, branch) }
func (Forge) BranchStatus(dir string) (forge.Status, bool)  { return BranchStatus(dir) }
func (Forge) FailedRunLogs(dir string, checks []forge.Check, limit int) (string, error) {
	return FailedRunLogs(dir, checks, limit)
}
func (Forge) CreatePR(dir, origin string) (string, error)       { return CreatePR(dir, origin) }
func (Forge) FetchIssue(dir string, n int) (forge.Issue, error) { return FetchIssue(dir, n) }
func (Forge) ListIssues(dir string, limit int) ([]forge.Issue, error) {
	return ListIssues(dir, limit)
}

// PRRef is the ref GitHub keeps pull request n's head at, in the
// repository it is to be merged into: a fork's too.
func (Forge) PRRef(n int) string { return fmt.Sprintf("refs/pull/%d/head", n) }
