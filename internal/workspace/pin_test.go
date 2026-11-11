package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests here play a sandboxed agent that can write the main checkout's
// refs and objects, but not its .git/config or .git/hooks, and wants the
// user's allowed setup command, or a hook, to run its code.

// evilCommit makes, in repo, a commit off the base that is the agent's: it
// has a setup the repository's .nerdr.toml would run and a post-checkout
// hook, which each leave a mark in marks if run. It moves no branch.
func evilCommit(t *testing.T, repo, marks string) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "evil")
	run(t, repo, "git", "worktree", "add", "-q", "--detach", tmp, "HEAD")
	if err := os.MkdirAll(filepath.Join(tmp, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ntouch " + filepath.Join(marks, "hook") + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "hooks", "post-checkout"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, tmp, "git", "add", "hooks")
	id := commit(t, tmp, "evil.sh", "touch "+filepath.Join(marks, "setup")+"\n")
	run(t, repo, "git", "worktree", "remove", "--force", tmp)
	return id
}

// pinRepo is a clone with origin, a .nerdr.toml whose setup runs evil.sh
// when the checkout has one, hooks taken from the worktree (as husky
// sets), and that config allowed. It gives the repository, a commit
// pushed to origin, where the marks go and the allowed file.
func pinRepo(t *testing.T) (Repo, string, string, string) {
	t.Helper()
	root := t.TempDir()
	a, push := cloneWithOrigin(t, root, "a")
	newest := push()
	run(t, a.Path, "git", "config", "core.hooksPath", "hooks")
	marks := t.TempDir()
	setup := "touch " + filepath.Join(marks, "ran") + "; if [ -f evil.sh ]; then sh evil.sh; fi"
	if err := os.WriteFile(filepath.Join(a.Path, RepoConfigFile), []byte("setup = '"+setup+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed.json")
	c, _ := LoadRepoConfig(a.Path)
	if err := Allow(allowed, a.Path, c); err != nil {
		t.Fatal(err)
	}
	return a, newest, marks, allowed
}

func marked(marks, name string) bool {
	_, err := os.Stat(filepath.Join(marks, name))
	return err == nil
}

func setRef(t *testing.T, repo, ref, id string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "update-ref", ref, id)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref %s: %v\n%s", ref, err, out)
	}
}

// makeTab runs what a new tab does: Sync, Execute and Prepare.
func makeTab(t *testing.T, a Repo, sandboxed bool, allowed string) (Plan, []Worktree, []string) {
	t.Helper()
	plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	plan.Sandboxed = sandboxed
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	return plan, wts, Prepare(wts, plan.SetupIn(), allowed, func(string) {})
}

// TestBaseIsOriginsCommitNotTheRef moves refs/remotes/origin/main to the
// agent's commit after the fetch: the worktree is still made from the
// commit origin named, setup runs on that, and neither the agent's hook
// nor its setup does.
func TestBaseIsOriginsCommitNotTheRef(t *testing.T) {
	a, newest, marks, allowed := pinRepo(t)
	evil := evilCommit(t, a.Path, marks)
	afterFetch = func(repo string) { setRef(t, repo, "refs/remotes/origin/main", evil) }
	t.Cleanup(func() { afterFetch = func(string) {} })

	plan, wts, warnings := makeTab(t, a, true, allowed)
	if got := revParse(t, wts[0].Path, "HEAD"); got != newest {
		t.Fatalf("worktree made from %s, not origin's %s (the agent's is %s)", got, newest, evil)
	}
	if _, err := os.Stat(filepath.Join(wts[0].Path, "evil.sh")); err == nil {
		t.Fatal("the worktree has the agent's files")
	}
	if marked(marks, "setup") || marked(marks, "hook") {
		t.Fatal("the agent's code ran")
	}
	if !marked(marks, "ran") || len(warnings) != 0 {
		t.Fatalf("setup did not run on origin's commit: %v", warnings)
	}
	if got := revParse(t, a.Path, "main"); got != newest {
		t.Fatalf("local main brought to %s, not origin's %s", got, newest)
	}
	if got := plan.Describe(); len(got) != 1 || !strings.Contains(got[0], "from origin/main") {
		t.Fatalf("describe = %q", got)
	}
}

// TestCheckoutIsOfTheCommitNotTheBranch moves the new branch to the
// agent's commit after git has made it and before its files are checked
// out, which git worktree add would do by the branch's name.
func TestCheckoutIsOfTheCommitNotTheBranch(t *testing.T) {
	a, newest, marks, allowed := pinRepo(t)
	evil := evilCommit(t, a.Path, marks)
	beforeCheckout = func(string) { setRef(t, a.Path, "refs/heads/feat", evil) }
	t.Cleanup(func() { beforeCheckout = func(string) {} })

	_, wts, warnings := makeTab(t, a, true, allowed)
	if got := revParse(t, wts[0].Path, "HEAD"); got != newest {
		t.Fatalf("worktree at %s, not origin's %s", got, newest)
	}
	if _, err := os.Stat(filepath.Join(wts[0].Path, "evil.sh")); err == nil {
		t.Fatal("the worktree has the agent's files")
	}
	if marked(marks, "setup") || marked(marks, "hook") {
		t.Fatal("the agent's code ran")
	}
	if !marked(marks, "ran") || len(warnings) != 0 {
		t.Fatalf("setup did not run on origin's commit: %v", warnings)
	}
}

// TestNoSetupWhenTheBranchMoved: a worktree no longer at the commit origin
// named by the time setup would run is not taken for the base's.
func TestNoSetupWhenTheBranchMoved(t *testing.T) {
	a, _, marks, _ := pinRepo(t)
	evil := evilCommit(t, a.Path, marks)
	plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SetupIn()[wts[0].Path] {
		t.Fatal("a worktree at origin's commit is not set up")
	}
	setRef(t, a.Path, "refs/heads/feat", evil)
	if plan.SetupIn()[wts[0].Path] {
		t.Fatal("a worktree moved off origin's commit is still set up")
	}
}

// TestUnpinnedBaseIsTrustedOnlyForTheUser covers the bases nerdr could not
// have origin name: the local main of a repository without origin, and
// origin's main as last fetched. A sandboxed agent can move either, so a
// tab one asked for gets no setup, nor a hook from inside the worktree; a
// tab the user asked for does, as before.
func TestUnpinnedBaseIsTrustedOnlyForTheUser(t *testing.T) {
	for _, tc := range []struct {
		name      string
		unfetched bool
	}{{"no origin", false}, {"unfetched", true}} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, marks, allowed := pinRepo(t)
			if !tc.unfetched {
				run(t, a.Path, "git", "remote", "remove", "origin")
			}
			// The agent's commit is where the base is: on main.
			evil := evilCommit(t, a.Path, marks)
			setRef(t, a.Path, "refs/heads/main", evil)
			setRef(t, a.Path, "refs/remotes/origin/main", evil)

			for _, sandboxed := range []bool{true, false} {
				plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
				if err != nil {
					t.Fatal(err)
				}
				plan.Unfetched, plan.Sandboxed = tc.unfetched, sandboxed
				if _, err := plan.Sync(); err != nil {
					t.Fatal(err)
				}
				if plan.Items[0].Pinned != "" {
					t.Fatalf("pinned to %s with no origin to ask", plan.Items[0].Pinned)
				}
				wts, err := plan.Execute()
				if err != nil {
					t.Fatal(err)
				}
				warnings := Prepare(wts, plan.SetupIn(), allowed, func(string) {})
				if sandboxed {
					if marked(marks, "setup") || marked(marks, "hook") || marked(marks, "ran") {
						t.Fatal("setup or a worktree hook ran for a tab a sandboxed agent asked for")
					}
					if len(warnings) != 1 || !strings.Contains(warnings[0], "setup not run") {
						t.Fatalf("warnings = %v", warnings)
					}
				} else if !marked(marks, "ran") || !marked(marks, "hook") {
					t.Fatal("setup or the hook did not run for a tab the user asked for")
				}
				if err := Remove("", wts, true); err != nil {
					t.Fatal(err)
				}
				run(t, a.Path, "git", "branch", "-q", "-D", "feat")
			}
		})
	}
}

// TestFailedCheckoutLeavesNothing: a hook that fails takes the worktree
// and the new branch with it, so trying again starts clean.
func TestFailedCheckoutLeavesNothing(t *testing.T) {
	a, _, _, _ := pinRepo(t)
	hooks := t.TempDir()
	run(t, a.Path, "git", "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Execute(); err == nil {
		t.Fatal("a failing hook did not fail the worktree")
	}
	if _, err := os.Stat(plan.Items[0].Path); err == nil {
		t.Fatal("the worktree was left")
	}
	if refExists(a.Path, "refs/heads/feat") {
		t.Fatal("the branch was left")
	}
}
