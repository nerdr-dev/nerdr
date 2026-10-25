package workspace

import (
	"path/filepath"
	"testing"

	gitdata "github.com/nerdr-dev/nerdr/internal/git"
)

// A branch stacked on another tab's starts where that one is, unpushed
// commits and all, and its work is counted against it.
func TestStackedBranchStartsOnTheOther(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	wtRoot := filepath.Join(root, "worktrees")
	plan, err := MakePlan(wtRoot, "first", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	first, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	run(t, first[0].Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "unpushed")
	tip := revParse(t, first[0].Path, "HEAD")

	plan, err = MakePlanOn(wtRoot, "second", []Repo{a}, "first")
	if err != nil {
		t.Fatal(err)
	}
	if it := plan.Items[0]; it.Base != "first" || !it.Stacked {
		t.Fatalf("plan item = %+v", it)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	second, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got := revParse(t, second[0].Path, "HEAD"); got != tip {
		t.Fatalf("second starts at %s, not first's %s", got, tip)
	}
	if b := gitdata.DetectBase(second[0].Path); b != "first" {
		t.Errorf("review base = %q", b)
	}
	if b := baseRef(second[0].Path); b != "first" {
		t.Errorf("base = %q", b)
	}
	// Once the branch it was on is gone, main is the base again.
	run(t, first[0].Repo, "git", "worktree", "remove", "--force", first[0].Path)
	run(t, first[0].Repo, "git", "branch", "-D", "first")
	if b := baseRef(second[0].Path); b != "origin/main" {
		t.Errorf("base after first is gone = %q", b)
	}
	if _, err := MakePlanOn(wtRoot, "third", []Repo{a}, "gone"); err == nil {
		t.Error("stacked on a branch that does not exist")
	}
}

// A repository whose default branch is not main starts new branches from
// it, as origin/HEAD says.
func TestBaseFollowsOriginsDefaultBranch(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	run(t, a.Path, "git", "push", "-q", "origin", "HEAD:develop")
	run(t, a.Path, "git", "remote", "set-head", "origin", "develop")
	if b := baseRef(a.Path); b != "origin/develop" {
		t.Errorf("base = %q", b)
	}
	if b := gitdata.DetectBase(a.Path); b != "origin/develop" {
		t.Errorf("review base = %q", b)
	}
}

// A new tab's branch starts from main even when the repository's own
// checkout is on a stacked branch: what that one is on is its own affair.
func TestPlanIgnoresTheCheckoutsStack(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	run(t, a.Path, "git", "branch", "first")
	run(t, a.Path, "git", "switch", "-q", "-c", "second")
	if err := gitdata.SetStackedBase(a.Path, "second", "first"); err != nil {
		t.Fatal(err)
	}
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "third", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if it := plan.Items[0]; it.Base != "origin/main" || it.Stacked {
		t.Fatalf("plan item = %+v", it)
	}
}
