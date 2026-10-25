package github

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRawPRToPR(t *testing.T) {
	data := `{
		"number": 42,
		"title": "Fix the thing",
		"url": "https://github.com/o/r/pull/42",
		"state": "OPEN",
		"isDraft": false,
		"mergeable": "MERGEABLE",
		"reviewDecision": "CHANGES_REQUESTED",
		"latestReviews": [
			{"author": {"login": "alice"}, "state": "APPROVED", "body": "", "submittedAt": "2026-01-01T00:00:00Z", "url": "https://x/1"},
			{"author": {"login": "bob"}, "state": "CHANGES_REQUESTED", "body": "fix this", "submittedAt": "2026-01-02T00:00:00Z", "url": "https://x/2"}
		],
		"comments": [
			{"author": {"login": "carol"}, "body": "looks good", "createdAt": "2026-01-03T00:00:00Z", "url": "https://x/3"}
		],
		"statusCheckRollup": [
			{"__typename": "CheckRun", "name": "build", "workflowName": "CI", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "https://x/4"},
			{"__typename": "CheckRun", "name": "test", "workflowName": "CI", "status": "IN_PROGRESS", "conclusion": "", "detailsUrl": "https://x/5"},
			{"__typename": "StatusContext", "context": "legacy-ci", "state": "FAILURE", "targetUrl": "https://x/6"}
		]
	}`

	var raw rawPR
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pr := raw.toPR()

	if pr.Number != 42 || pr.Title != "Fix the thing" || pr.ReviewDecision != "CHANGES_REQUESTED" {
		t.Fatalf("basic fields wrong: %+v", pr)
	}
	if len(pr.Reviews) != 2 || pr.Reviews[0].Author != "alice" || pr.Reviews[1].State != "CHANGES_REQUESTED" {
		t.Fatalf("reviews wrong: %+v", pr.Reviews)
	}
	if len(pr.Comments) != 1 || pr.Comments[0].Author != "carol" {
		t.Fatalf("comments wrong: %+v", pr.Comments)
	}
	if len(pr.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d: %+v", len(pr.Checks), pr.Checks)
	}
	if pr.Checks[0].Name != "CI / build" || pr.Checks[0].Status != "SUCCESS" {
		t.Fatalf("CheckRun (completed) normalised wrong: %+v", pr.Checks[0])
	}
	if pr.Checks[1].Name != "CI / test" || pr.Checks[1].Status != "IN_PROGRESS" {
		t.Fatalf("CheckRun (running, no conclusion yet) normalised wrong: %+v", pr.Checks[1])
	}
	if pr.Checks[2].Name != "legacy-ci" || pr.Checks[2].Status != "FAILURE" {
		t.Fatalf("StatusContext normalised wrong: %+v", pr.Checks[2])
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("no pull requests found for branch \"master\"\nextra"); got != `no pull requests found for branch "master"` {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine("single line"); got != "single line" {
		t.Fatalf("firstLine = %q", got)
	}
}

func TestCurrentPRNoRemote(t *testing.T) {
	if !Available() {
		t.Skip("gh not available")
	}
	dir := t.TempDir()
	// Not even a git repo, let alone one with a GitHub remote.
	_, ok, msg := CurrentPR(dir)
	if ok {
		t.Fatal("expected ok=false for a non-git directory")
	}
	if msg == "" {
		t.Fatal("expected a non-empty explanatory message")
	}
}

func TestGitHubName(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/Acme/Nerdr.git":   "acme/nerdr",
		"https://github.com/acme/nerdr":       "acme/nerdr",
		"git@github.com:acme/nerdr.git":       "acme/nerdr",
		"ssh://git@github.com/acme/nerdr.git": "acme/nerdr",
		"https://gitlab.com/acme/nerdr.git":   "",
		"git@github.example.com:acme/nerdr":   "",
		"https://github.com/acme":             "",
	} {
		got, ok := gitHubName(url)
		if got != want || ok != (want != "") {
			t.Errorf("gitHubName(%q) = %q, %v; want %q", url, got, ok, want)
		}
	}
}

// TestParseIssues covers what the issue lists read from gh: the labels'
// and author's names, the repository from a search, newest first.
func TestParseIssues(t *testing.T) {
	out := `[
	 {"number":3,"title":"Old","url":"u3","author":{"login":"ann"},"labels":[],"updatedAt":"2026-09-01T10:00:00Z","repository":{"nameWithOwner":"Me/App"}},
	 {"number":9,"title":"Login fails","url":"u9","author":{"login":"bo"},"labels":[{"name":"bug"},{"name":"p1"}],"updatedAt":"2026-10-01T10:00:00Z"}
	]`
	issues, err := parseIssues([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Number != 9 || issues[0].Author != "bo" ||
		strings.Join(issues[0].Labels, ",") != "bug,p1" || issues[1].Repo != "me/app" {
		t.Fatalf("issues = %+v", issues)
	}
}

// gh with no login says so in several lines; nerdr says it in one.
func TestNotLoggedIn(t *testing.T) {
	for _, msg := range []string{
		"To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.",
		"HTTP 401: Bad credentials (https://api.github.com/graphql)\nTry authenticating with:  gh auth login",
	} {
		if !NotLoggedIn(msg) {
			t.Errorf("not taken for no login: %q", msg)
		}
	}
	if NotLoggedIn("no pull requests found for branch \"main\"") {
		t.Error("an ordinary message taken for no login")
	}
}
