package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingSSH is an ssh, given as GIT_SSH_COMMAND, that runs what git asks
// of origin locally and adds a line to the file it returns for every
// connection: every login a key behind Touch ID asks a finger for.
func countingSSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "connections")
	script := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\necho x >> " + log + "\nfor a; do last=$a; done\neval \"$last\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	return log
}

func connections(t *testing.T, log string) int {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := strings.Count(string(b), "\n")
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestNewTabConnectsOnceToOrigin: a new tab asks origin once per
// repository, for whatever its branch is, and still starts from origin's
// main as it is now; with a git without fetch --porcelain, as before.
func TestNewTabConnectsOnceToOrigin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		porcelain bool
		// Connections for a new branch, one on origin, a local one.
		new, remote, local int
	}{
		{"porcelain", true, 1, 1, 1},
		{"older git", false, 3, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.porcelain && !hasPorcelain() {
				t.Skip("git is older than 2.41")
			}
			was := hasPorcelain
			hasPorcelain = func() bool { return tc.porcelain }
			t.Cleanup(func() { hasPorcelain = was })

			root := t.TempDir()
			a, push := cloneWithOrigin(t, root, "a")
			log := countingSSH(t)
			origin := filepath.Join(root, "origins", "a.git")
			run(t, a.Path, "git", "remote", "set-url", "origin", "ssh://elsewhere"+origin)
			newest := push()

			plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Sync(); err != nil {
				t.Fatal(err)
			}
			if n := connections(t, log); n != tc.new {
				t.Fatalf("a new branch connected to origin %d times, want %d", n, tc.new)
			}
			if plan.Items[0].Pinned != newest {
				t.Fatalf("pinned to %q, origin's main is %s", plan.Items[0].Pinned, newest)
			}
			wts, err := plan.Execute()
			if err != nil {
				t.Fatal(err)
			}
			if got := revParse(t, wts[0].Path, "HEAD"); got != newest {
				t.Fatalf("worktree starts at %s, not origin's %s", got, newest)
			}

			// Origin has a branch: continued, and fetched in the same go.
			run(t, filepath.Join(root, "others", "a"), "git", "push", "-q", "origin", "HEAD:refs/heads/theirs")
			plan, err = MakePlan(filepath.Join(root, "worktrees"), "theirs", []Repo{a})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Sync(); err != nil {
				t.Fatal(err)
			}
			if plan.Items[0].Mode != "remote" {
				t.Fatalf("mode = %q", plan.Items[0].Mode)
			}
			if n := connections(t, log); n != tc.remote {
				t.Fatalf("origin's branch connected %d times, want %d", n, tc.remote)
			}

			// The local feat, made above.
			if err := Remove("", wts, true); err != nil {
				t.Fatal(err)
			}
			plan, err = MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Items[0].Mode != "local" {
				t.Fatalf("mode = %q", plan.Items[0].Mode)
			}
			if n := connections(t, log); n != tc.local {
				t.Fatalf("a local branch connected %d times, want %d", n, tc.local)
			}
		})
	}
}

// TestStalePlanAsksOriginAgain: the plan's answer stands for origin's main
// only while it is fresh.
func TestStalePlanAsksOriginAgain(t *testing.T) {
	root := t.TempDir()
	a, push := cloneWithOrigin(t, root, "a")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	newest := push()
	plan.Items[0].BaseAt = plan.Items[0].BaseAt.Add(-2 * planFresh)
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Pinned != newest {
		t.Fatalf("pinned to %q, origin's main is now %s", plan.Items[0].Pinned, newest)
	}
}

// TestPrunedBranchIsNotTakenForOrigins: with fetch.prune set, a branch
// gone from origin is not taken for one it has, nor is a ref pruned
// read for origin's answer.
func TestPrunedBranchIsNotTakenForOrigins(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	other := filepath.Join(root, "others", "a")
	run(t, other, "git", "push", "-q", "origin", "HEAD:refs/heads/feat")
	run(t, a.Path, "git", "fetch", "-q", "origin")
	run(t, other, "git", "push", "-q", "origin", ":refs/heads/feat")
	run(t, a.Path, "git", "config", "fetch.prune", "true")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Mode != "new" {
		t.Fatalf("mode = %q", plan.Items[0].Mode)
	}
	if refExists(a.Path, "refs/remotes/origin/feat") {
		t.Fatal("the stale origin/feat was kept")
	}
}

// TestRefMovedBetweenPlanAndSync: the agent moves origin/main after the
// plan's fetch, and again after Sync's: the worktree is made from what
// origin named.
func TestRefMovedBetweenPlanAndSync(t *testing.T) {
	a, newest, marks, _ := pinRepo(t)
	evil := evilCommit(t, a.Path, marks)
	plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	setRef(t, a.Path, "refs/remotes/origin/main", evil)
	afterFetch = func(repo string) { setRef(t, repo, "refs/remotes/origin/main", evil) }
	t.Cleanup(func() { afterFetch = func(string) {} })
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Pinned != newest {
		t.Fatalf("pinned to %s, not origin's %s (the agent's is %s)", plan.Items[0].Pinned, newest, evil)
	}
}

func TestGitAtLeast(t *testing.T) {
	for v, want := range map[string]bool{
		"git version 2.39.5 (Apple Git-154)": false,
		"git version 2.41.0":                 true,
		"git version 2.43.0":                 true,
		"git version 3.0.0":                  true,
		"git version 2.40.1.windows.1":       false,
		"nonsense":                           false,
	} {
		if got := gitAtLeast(v, 2, 41); got != want {
			t.Errorf("gitAtLeast(%q) = %v", v, got)
		}
	}
}

// TestStaleBranchInThePatternsWay: a stale origin/zz, gone from origin,
// is in the way of origin's zz/more, which the plan's pattern for zz
// takes along; zz is still told to be new, and the stale one goes.
func TestStaleBranchInThePatternsWay(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	other := filepath.Join(root, "others", "a")
	run(t, other, "git", "push", "-q", "origin", "HEAD:refs/heads/zz/more")
	run(t, a.Path, "git", "update-ref", "refs/remotes/origin/zz", "HEAD")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "zz", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Mode != "new" {
		t.Fatalf("mode = %q", plan.Items[0].Mode)
	}
	if refExists(a.Path, "refs/remotes/origin/zz") {
		t.Fatal("the stale origin/zz was kept")
	}
}

// TestStaleBranchInTheBranchsWay: a stale origin/zz is in the way of
// origin's zz/more itself: the plan says so rather than take zz/more for a
// branch origin has not, and start it anew.
func TestStaleBranchInTheBranchsWay(t *testing.T) {
	if !hasPorcelain() {
		t.Skip("git is older than 2.41")
	}
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	other := filepath.Join(root, "others", "a")
	run(t, other, "git", "push", "-q", "origin", "HEAD:refs/heads/zz/more")
	run(t, a.Path, "git", "update-ref", "refs/remotes/origin/zz", "HEAD")

	_, err := MakePlan(filepath.Join(root, "worktrees"), "zz/more", []Repo{a})
	if err == nil || !strings.Contains(err.Error(), "git remote prune origin") {
		t.Fatalf("err = %v", err)
	}
}

// TestStaleBranchGoesWithNoBaseToFetch: with no origin/main here to
// fetch, and no branch on origin the pattern matches, the fetch says
// nothing, and the stale origin/feat still goes.
func TestStaleBranchGoesWithNoBaseToFetch(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	run(t, a.Path, "git", "update-ref", "refs/remotes/origin/feat", "HEAD")
	run(t, a.Path, "git", "update-ref", "-d", "refs/remotes/origin/main")
	run(t, a.Path, "git", "symbolic-ref", "-d", "refs/remotes/origin/HEAD")

	has, _, err := remoteBranch(a.Path, "feat", originBaseRef(a.Path))
	if err != nil || has {
		t.Fatalf("has %v, err %v", has, err)
	}
	if refExists(a.Path, "refs/remotes/origin/feat") {
		t.Fatal("the stale origin/feat was kept")
	}
}
