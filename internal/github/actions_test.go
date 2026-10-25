package github

import (
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
