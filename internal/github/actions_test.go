package github

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssueBranchName(t *testing.T) {
	for title, want := range map[string]string{
		"Login fails on empty password!":          "12-login-fails-on-empty-password",
		"  [Bug] Crash: when   saving ":           "12-bug-crash-when-saving",
		"one two three four five six seven eight": "12-one-two-three-four-five-six",
		"æøå": "12",
	} {
		if got := (Issue{Number: 12, Title: title}).BranchName(); got != want {
			t.Errorf("%q: %q, want %q", title, got, want)
		}
	}
}

func TestIssueTask(t *testing.T) {
	got := Issue{Number: 3, Title: "Fix it", Body: " details \n", URL: "https://x/3"}.Task()
	want := "Resolve GitHub issue #3: Fix it (https://x/3)\n\ndetails"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestCISummary(t *testing.T) {
	c := func(s ...string) []Check {
		var out []Check
		for _, x := range s {
			out = append(out, Check{Status: x})
		}
		return out
	}
	for _, tc := range []struct {
		checks []Check
		want   string
	}{
		{nil, ""},
		{c("SUCCESS", "SKIPPED"), "pass"},
		{c("SUCCESS", "IN_PROGRESS"), "pending"},
		{c("IN_PROGRESS", "FAILURE"), "fail"},
	} {
		if got := CISummary(tc.checks); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.checks, got, tc.want)
		}
	}
}

func TestLastURL(t *testing.T) {
	out := "Creating pull request for x into main\n\nhttps://github.com/o/r/pull/7\n"
	if got := lastURL(out); got != "https://github.com/o/r/pull/7" {
		t.Fatal(got)
	}
}

func TestOwnPR(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	// An earlier branch of the same name, merged into main with a merge
	// commit: its head is in main's history, and so in any branch from main.
	git("switch", "-q", "-c", "readme")
	git("commit", "-q", "--allow-empty", "-m", "old work")
	old := git("rev-parse", "HEAD")
	if !ownPR(dir, "MERGED", old) {
		t.Fatal("a merged PR whose head is HEAD should be the branch's")
	}
	git("switch", "-q", "main")
	git("merge", "-q", "--no-ff", "-m", "merge", "readme")
	git("branch", "-q", "-D", "readme")

	git("switch", "-q", "-c", "readme")
	if ownPR(dir, "MERGED", old) {
		t.Fatal("a new branch from main took the old branch's merged PR")
	}
	git("commit", "-q", "--allow-empty", "-m", "new work")
	if ownPR(dir, "CLOSED", old) {
		t.Fatal("a new branch with work took the old branch's closed PR")
	}
	if ownPR(dir, "MERGED", "") {
		t.Fatal("a merged PR with no head commit should not be the branch's")
	}
	if !ownPR(dir, "OPEN", old) {
		t.Fatal("an open PR is always the branch's")
	}
}

// testRepo makes a repository on main with one commit, and the git to
// run in it, which answers with what git printed.
func testRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	return dir, git
}

// fakeGh puts a gh first on PATH whose pr view prints the JSON of a PR
// with this state, head and base.
func fakeGh(t *testing.T, state, head, base string) {
	t.Helper()
	bin := t.TempDir()
	js := fmt.Sprintf(`{"number":16,"url":"https://github.com/o/r/pull/16","state":%q,"headRefOid":%q,"baseRefName":%q,"statusCheckRollup":[]}`,
		state, head, base)
	if err := os.WriteFile(filepath.Join(bin, "pr.json"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat '" + filepath.Join(bin, "pr.json") + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A branch that went on after its PR was rebase-merged shows the PR, with
// the commits after it; a new branch of the same name from main does not.
func TestCommitsAfterRebaseMerge(t *testing.T) {
	dir, git := testRepo(t)
	git("switch", "-q", "-c", "brew")
	git("commit", "-q", "--allow-empty", "-m", "work")
	head := git("rev-parse", "HEAD")
	// GitHub's rebase merge: the same change, as a new commit on main.
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "work (rebased)")
	git("update-ref", "refs/remotes/origin/main", "main")

	git("switch", "-q", "brew")
	git("commit", "-q", "--allow-empty", "-m", "more work")
	if n := commitsAfter(dir, head, "main"); n != 1 {
		t.Fatalf("commits after the rebase-merged PR: %d, want 1", n)
	}
	fakeGh(t, "MERGED", head, "main")
	st, ok := BranchStatus(dir)
	if !ok || st.State != "MERGED" || st.Number != 16 || st.After != 1 {
		t.Fatalf("branch status: %+v %v", st, ok)
	}
	if n := commitsAfter(dir, head, "trunk"); n != 0 {
		t.Fatalf("with a base git does not know, %d commits after", n)
	}
	if n := commitsAfter(dir, strings.Repeat("1", 40), "main"); n != 0 {
		t.Fatalf("with a head git does not have, %d commits after", n)
	}

	git("switch", "-q", "main")
	git("branch", "-q", "-D", "brew")
	git("switch", "-q", "-c", "brew")
	git("commit", "-q", "--allow-empty", "-m", "new work")
	if n := commitsAfter(dir, head, "main"); n != 0 {
		t.Fatalf("a new branch from main has %d commits after the old PR", n)
	}
	if st, ok := BranchStatus(dir); ok {
		t.Fatalf("a new branch from main took the old branch's PR: %+v", st)
	}
}

// With a merge commit the PR's head is in main, and in every branch made
// from it since: a new branch of the same name must not show the PR. One
// that went on from the head itself does.
func TestCommitsAfterMergeCommit(t *testing.T) {
	dir, git := testRepo(t)
	git("switch", "-q", "-c", "brew")
	git("commit", "-q", "--allow-empty", "-m", "work")
	head := git("rev-parse", "HEAD")
	git("switch", "-q", "main")
	git("merge", "-q", "--no-ff", "-m", "merge", "brew")
	git("update-ref", "refs/remotes/origin/main", "main")
	fakeGh(t, "MERGED", head, "main")

	git("switch", "-q", "brew")
	git("commit", "-q", "--allow-empty", "-m", "more work")
	git("commit", "-q", "--allow-empty", "-m", "and more")
	if n := commitsAfter(dir, head, "main"); n != 2 {
		t.Fatalf("commits after the merged PR: %d, want 2", n)
	}
	if st, ok := BranchStatus(dir); !ok || st.After != 2 {
		t.Fatalf("branch status: %+v %v", st, ok)
	}

	// Main moves on and is merged into the branch: the branch still went
	// on from the PR, with the merge of main as one commit more.
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "on main")
	git("update-ref", "refs/remotes/origin/main", "main")
	git("switch", "-q", "brew")
	git("merge", "-q", "--no-ff", "-m", "merge main", "main")
	if n := commitsAfter(dir, head, "main"); n != 3 {
		t.Fatalf("commits after the merged PR with main merged in: %d, want 3", n)
	}

	git("switch", "-q", "main")
	git("branch", "-q", "-D", "brew")
	git("switch", "-q", "-c", "brew")
	git("commit", "-q", "--allow-empty", "-m", "new work")
	if n := commitsAfter(dir, head, "main"); n != 0 {
		t.Fatalf("a new branch from main has %d commits after the old PR", n)
	}
	if st, ok := BranchStatus(dir); ok {
		t.Fatalf("a new branch from main took the old branch's PR: %+v", st)
	}
	// Only the local main has the merge: still not the branch's.
	git("update-ref", "-d", "refs/remotes/origin/main")
	if n := commitsAfter(dir, head, "main"); n != 0 {
		t.Fatalf("with only the local main, %d commits after the old PR", n)
	}
}

// An open PR, or a merged one the branch is still at, is the branch's
// with nothing after it.
func TestBranchStatusNothingAfter(t *testing.T) {
	dir, git := testRepo(t)
	git("switch", "-q", "-c", "brew")
	git("commit", "-q", "--allow-empty", "-m", "work")
	head := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/main", "main")
	for _, state := range []string{"OPEN", "MERGED"} {
		fakeGh(t, state, head, "main")
		if st, ok := BranchStatus(dir); !ok || st.State != state || st.After != 0 {
			t.Fatalf("%s: %+v %v", state, st, ok)
		}
	}
}

func TestRepoArg(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:Owner/Repo.git":             "owner/repo",
		"https://github.com/owner/repo":             "owner/repo",
		"git@ghe.example.com:team/app.git":          "ghe.example.com/team/app",
		"ssh://git@ghe.example.com:2222/team/app":   "ghe.example.com/team/app",
		"https://user@ghe.example.com/team/app.git": "ghe.example.com/team/app",
		"/srv/git/app.git":                          "",
		"file:///srv/git/app.git":                   "",
	} {
		got, ok := repoArg(url)
		if got != want || ok != (want != "") {
			t.Errorf("repoArg(%q) = %q, %v; want %q", url, got, ok, want)
		}
	}
}

// A gh that hangs is given up on: the PR watch waits for its answer
// before it asks again, and would otherwise wait for good.
func TestGhThatHangsIsGivenUpOn(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := ghTimeout
	ghTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ghTimeout = old })
	start := time.Now()
	if _, err := ListPRs(t.TempDir(), 10); err == nil {
		t.Error("a gh that hangs answered")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("waited %s", took)
	}
}
