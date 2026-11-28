package gitlab

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// fakeGlab answers glab api calls from answers, by the path asked (with
// its query) or a prefix of it ending in "*", and keeps the calls made.
// An answer of timeout is glab timing out. A glab on PATH that does
// nothing makes Available true.
type fakeGlab struct {
	mu      sync.Mutex
	answers map[string]string
	calls   [][]string
}

func newFake(t *testing.T, answers map[string]string) *fakeGlab {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "glab"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := &fakeGlab{answers: answers}
	old := run
	run = f.run
	t.Cleanup(func() { run = old })
	projects.Lock()
	projects.byPath, projects.byID = map[string]rawProject{}, map[string]rawProject{}
	projects.Unlock()
	users.Lock()
	users.m = map[string]rawUser{}
	users.Unlock()
	return f
}

func (f *fakeGlab) run(_ time.Duration, _ string, _ bool, _ []string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.mu.Unlock()
	if len(args) < 2 || args[0] != "api" {
		return nil, errors.New("fake glab: " + strings.Join(args, " "))
	}
	answer := func(a string) ([]byte, error) {
		if a == timeout {
			return nil, errors.New("glab timed out")
		}
		return []byte(a), nil
	}
	if a, ok := f.answers[args[1]]; ok {
		return answer(a)
	}
	for k, a := range f.answers {
		if p, ok := strings.CutSuffix(k, "*"); ok && strings.HasPrefix(args[1], p) {
			return answer(a)
		}
	}
	return nil, &exec.ExitError{Stderr: []byte("glab: 404 Not Found (HTTP 404)")}
}

const timeout = "\x00timeout"

func (f *fakeGlab) called(prefix string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 1 && strings.HasPrefix(c[1], prefix) {
			out = append(out, c)
		}
	}
	return out
}

func TestRawMRToPR(t *testing.T) {
	for _, c := range []struct {
		state, merge string
		conflicts    bool
		wantState    string
		wantMerge    string
	}{
		{"opened", "mergeable", false, "OPEN", "MERGEABLE"},
		{"opened", "not_approved", false, "OPEN", "MERGEABLE"}, // waiting on others, not a conflict
		{"opened", "checking", false, "OPEN", "UNKNOWN"},
		{"opened", "conflict", true, "OPEN", "CONFLICTING"},
		{"opened", "need_rebase", false, "OPEN", "MERGEABLE"}, // the target moved on: no conflict
		{"merged", "", false, "MERGED", "UNKNOWN"},
		{"locked", "", false, "CLOSED", "UNKNOWN"},
	} {
		pr := rawMR{IID: 4, State: c.state, DetailedMergeStatus: c.merge, HasConflicts: c.conflicts}.toPR()
		if pr.State != c.wantState || pr.Mergeable != c.wantMerge || pr.Kind != forge.GitLab || pr.Ref() != "!4" {
			t.Errorf("%s/%s: %+v", c.state, c.merge, pr)
		}
	}
}

func TestCheckStatus(t *testing.T) {
	for _, c := range []struct {
		status string
		allow  bool
		want   string
	}{
		{"success", false, "SUCCESS"},
		{"failed", false, "FAILURE"},
		{"failed", true, "NEUTRAL"}, // allowed to fail: the pipeline passes
		{"manual", true, "NEUTRAL"}, // optional, not run
		{"manual", false, "PENDING"},
		{"canceled", false, "CANCELLED"},
		{"running", false, "PENDING"},
		{"skipped", false, "SKIPPED"},
	} {
		if got := checkStatus(c.status, c.allow); got != c.want {
			t.Errorf("%s %v: %s, want %s", c.status, c.allow, got, c.want)
		}
	}
}

func TestProjectOfURLAndRefs(t *testing.T) {
	if p, ok := projectOfURL("https://gitlab.com/grp/sub/app/-/jobs/12", "/-/jobs/"); !ok || p != "grp/sub/app" {
		t.Errorf("job url: %q %v", p, ok)
	}
	if p, n, ok := cutRef("grp/sub/app!12", "!"); !ok || p != "grp/sub/app" || n != 12 {
		t.Errorf("ref: %q %d %v", p, n, ok)
	}
	if project("grp/sub/app") != "projects/grp%2Fsub%2Fapp" {
		t.Errorf("project: %q", project("grp/sub/app"))
	}
}

// A merge request read in full: its pipeline's jobs, its approvals and a
// reviewer's asking for changes, and its discussions, where a reply in a
// thread on a line is on that line, a comment on a line taken out is on
// the old side, and GitLab's own notes are no one's comments. Who
// commented is said in GitHub's words for how they stand to the project.
func TestFetchPR(t *testing.T) {
	mr := `{"iid":4,"title":"Add login","web_url":"https://gitlab.com/grp/app/-/merge_requests/4","state":"opened",
		"author":{"id":1,"username":"me"},"source_branch":"login","target_branch":"main","sha":"abc",
		"updated_at":"2026-01-02T00:00:00Z","detailed_merge_status":"mergeable","source_project_id":7,"target_project_id":7,
		"head_pipeline":{"id":50,"project_id":7,"status":"failed","web_url":"https://gitlab.com/grp/app/-/pipelines/50"}}`
	f := newFake(t, map[string]string{
		"projects/grp%2Fapp/merge_requests/4": mr,
		"projects/grp%2Fapp/merge_requests/4/approvals": `{"approved":false,"approvals_required":2,"approvals_left":1,
			"approved_by":[{"user":{"id":2,"username":"ann"},"approved_at":"2026-01-03T00:00:00Z"}]}`,
		"projects/grp%2Fapp/merge_requests/4/reviewers": `[{"user":{"id":3,"username":"bob"},"state":"requested_changes"}]`,
		"projects/grp%2Fapp/merge_requests/4/discussions*": `[
			{"notes":[{"id":10,"body":"why?","author":{"id":3,"username":"bob"},"created_at":"2026-01-02T01:00:00Z",
				"position":{"new_path":"a.go","new_line":7}},
				{"id":11,"body":"because","author":{"id":1,"username":"me"},"created_at":"2026-01-02T02:00:00Z"}]},
			{"notes":[{"id":12,"body":"gone?","author":{"id":9,"username":"eve"},"created_at":"2026-01-02T03:00:00Z",
				"position":{"new_path":"b.go","old_path":"b.go","new_line":0,"old_line":3}}]}]
			[{"notes":[{"id":13,"body":"added 1 commit","author":{"id":1,"username":"me"},"created_at":"2026-01-02T04:00:00Z","system":true}]}]`,
		"projects/7/pipelines/50/jobs*": `[{"id":60,"name":"unit","stage":"test","status":"failed","web_url":"https://gitlab.com/grp/app/-/jobs/60"},
			{"id":61,"name":"lint","stage":"test","status":"failed","allow_failure":true,"web_url":"https://gitlab.com/grp/app/-/jobs/61"}]`,
		"projects/grp%2Fapp/members/all/3": `{"access_level":30}`,
		"projects/grp%2Fapp/members/all/1": `{"access_level":50}`,
		"projects/grp%2Fapp/members/all/2": `{"access_level":40}`,
	})
	fg := Forge{Origin: forge.Remote{Name: "origin", Host: "gitlab.com", Path: "grp/app"}}
	pr, err := fg.FetchPR("", 4)
	if err != nil {
		t.Fatal(err)
	}
	if pr.ReviewDecision != "CHANGES_REQUESTED" || len(pr.Reviews) != 2 || pr.Reviews[0].Association != "MEMBER" {
		t.Fatalf("reviews: %q %+v", pr.ReviewDecision, pr.Reviews)
	}
	if len(pr.Checks) != 2 || pr.Checks[0].Name != "test / unit" || pr.Checks[0].Status != "FAILURE" || pr.Checks[1].Status != "NEUTRAL" {
		t.Fatalf("checks: %+v", pr.Checks)
	}
	if len(pr.Comments) != 3 {
		t.Fatalf("comments: %+v", pr.Comments)
	}
	c := pr.Comments
	if c[1].Path != "a.go" || c[1].Line != 7 || c[1].Association != "OWNER" || c[1].URL != pr.URL+"#note_11" {
		t.Errorf("a reply in a thread on a line: %+v", c[1])
	}
	if c[2].Path != "b.go" || c[2].Line != 3 || c[2].Association != "NONE" {
		t.Errorf("a comment on a line taken out, by a stranger: %+v", c[2])
	}
	if c[0].Association != "MEMBER" || forge.Trusted(c[2].Association) {
		t.Errorf("associations: %+v", c)
	}
	if len(f.called("projects/grp%2Fapp/members/all/3")) != 1 {
		t.Error("a member's role should be asked once")
	}
}

// git runs git in dir for a test.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The merge request of the branch is the one from origin: another of the
// same branch name, from someone's fork, is not, and an open one is
// taken over a newer closed one. In a fork, the merge request into the
// project it is a fork of is found there.
func TestCurrentPRFindsOrigins(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, dir, "switch", "-q", "-c", "login")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "work")
	head := gitIn(t, dir, "rev-parse", "HEAD")

	list := `[{"iid":9,"state":"opened","source_branch":"login","source_project_id":99,"target_project_id":5,"sha":"zzz"},
		{"iid":8,"state":"closed","source_branch":"login","source_project_id":7,"target_project_id":5,"sha":"old","updated_at":"2026-02-01T00:00:00Z"},
		{"iid":6,"state":"opened","source_branch":"login","source_project_id":7,"target_project_id":5,"sha":"` + head + `","updated_at":"2026-01-01T00:00:00Z"}]`
	f := newFake(t, map[string]string{
		"projects/me%2Fapp":                    `{"id":7,"path_with_namespace":"me/app","namespace":{"full_path":"me"}}`,
		"projects/me%2Fapp/merge_requests?*":   `[]`,
		"projects/grp%2Fapp/merge_requests?*":  list,
		"projects/grp%2Fapp/merge_requests/6":  `{"iid":6,"state":"opened","source_branch":"login","target_branch":"main","sha":"` + head + `","source_project_id":7,"target_project_id":5}`,
		"projects/grp%2Fapp/merge_requests/6*": `[]`,
		"projects/7":                           `{"id":7,"path_with_namespace":"me/app","namespace":{"full_path":"me"}}`,
	})
	fg := Forge{Origin: forge.Remote{Name: "origin", Host: "gitlab.com", Path: "me/app"},
		Upstream: forge.Remote{Name: "upstream", Host: "gitlab.com", Path: "grp/app"}}
	pr, ok, msg := fg.CurrentPR(dir)
	if !ok || pr.Number != 6 || pr.Fork != "me" {
		t.Fatalf("got %+v %v %q", pr, ok, msg)
	}
	q := f.called("projects/grp%2Fapp/merge_requests?")
	if len(q) != 1 || !strings.Contains(q[0][1], "source_branch=login") || q[0][3] != "gitlab.com" {
		t.Fatalf("asked: %v", q)
	}
}

// The searches read every page, and the one for the merge requests your
// review is asked for looks among everyone's, not only yours.
func TestSearches(t *testing.T) {
	f := newFake(t, map[string]string{
		"user": `{"id":1,"username":"me"}`,
		"merge_requests?*": `[{"iid":4,"references":{"full":"grp/sub/app!4"}}]
			[{"iid":5,"references":{"full":"grp/lib!5"}}]`,
		"issues?*": `[{"iid":3,"title":"T","references":{"full":"grp/lib#3"},"updated_at":"2026-01-01T00:00:00Z"}]`,
	})
	asked, err := ReviewRequested("gitlab.example.com")
	if err != nil || !asked["gitlab.example.com/grp/sub/app#4"] || !asked["gitlab.example.com/grp/lib#5"] {
		t.Fatalf("asked: %v %v", asked, err)
	}
	q := f.called("merge_requests?")[0][1]
	if !strings.Contains(q, "scope=all") || !strings.Contains(q, "reviewer_username=me") || !strings.Contains(q, "state=opened") {
		t.Errorf("review search: %s", q)
	}
	if c := f.called("merge_requests?")[0]; !contains(c, "--paginate") || !contains(c, "gitlab.example.com") {
		t.Errorf("not every page, or not on the host: %v", c)
	}
	repos, err := ReposWithMyPRs("gitlab.com")
	if err != nil || !repos["gitlab.com/grp/sub/app"] || !repos["gitlab.com/grp/lib"] {
		t.Fatalf("repos: %v %v", repos, err)
	}
	issues, err := MyIssues("gitlab.com")
	if err != nil || len(issues) != 1 || issues[0].Repo != "gitlab.com/grp/lib" || issues[0].Kind != forge.GitLab {
		t.Fatalf("issues: %+v %v", issues, err)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A merge request is opened from the commits, into the project's default
// branch, and the logs of a failed job are read from the project it ran
// in, without the terminal's codes.
func TestCreatePRAndLogs(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, dir, "branch", "develop")
	gitIn(t, dir, "switch", "-q", "-c", "login")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "Add login", "-m", "With a form.")
	f := newFake(t, map[string]string{
		"projects/grp%2Fapp":                    `{"id":7,"path_with_namespace":"grp/app","default_branch":"develop"}`,
		"projects/grp%2Fapp/merge_requests":     `{"iid":12,"web_url":"https://gitlab.com/grp/app/-/merge_requests/12"}`,
		"projects/fork%2Fapp/jobs/60/trace":     "\x1b[32mok\x1b[0m\nFAIL x\nERROR: Job failed: exit code 1\n",
		"projects/grp%2Fapp/merge_requests/12*": `{}`,
	})
	fg := Forge{Origin: forge.Remote{Name: "origin", Host: "gitlab.com", Path: "grp/app"}}
	url, err := fg.CreatePR(dir, "git@gitlab.com:grp/app.git")
	if err != nil || url != "https://gitlab.com/grp/app/-/merge_requests/12" {
		t.Fatalf("%q %v", url, err)
	}
	c := f.called("projects/grp%2Fapp/merge_requests")[0]
	for _, want := range []string{"source_branch=login", "target_branch=develop", "title=Add login", "description=With a form.", "POST"} {
		if !contains(c, want) {
			t.Errorf("create lacks %q: %v", want, c)
		}
	}
	logs, err := fg.FailedRunLogs(dir, []forge.Check{{Name: "test / unit", Status: "FAILURE", DetailsURL: "https://gitlab.com/fork/app/-/jobs/60"},
		{Name: "lint", Status: "NEUTRAL", DetailsURL: "https://gitlab.com/fork/app/-/jobs/61"}}, 1000)
	if err != nil || !strings.Contains(logs, "FAIL x") || strings.Contains(logs, "\x1b") || strings.Contains(logs, "jobs 61") {
		t.Fatalf("logs: %q %v", logs, err)
	}
}

func TestNotLoggedIn(t *testing.T) {
	err := &exec.ExitError{Stderr: []byte("glab: 401 Unauthorized (HTTP 401)")}
	if got := errorMessage("git.corp.example", err); !strings.Contains(got, "glab auth login --hostname git.corp.example") {
		t.Errorf("message: %q", got)
	}
	if !NotFound(errors.New("glab: 404 Not Found (HTTP 404)")) || NotFound(errors.New("glab timed out")) {
		t.Error("not found")
	}
}

// A pipeline's trigger jobs are among its checks; and one that failed
// with none of the jobs read failing is a failed check itself.
func TestJobChecksTakeBridges(t *testing.T) {
	newFake(t, map[string]string{
		"projects/7/pipelines/50/jobs*":    `[{"id":60,"name":"unit","stage":"test","status":"success"}]`,
		"projects/7/pipelines/50/bridges*": `[{"id":61,"name":"deploy","stage":"ship","status":"failed"}]`,
		"projects/7/pipelines/51/jobs*":    `[{"id":62,"name":"unit","stage":"test","status":"success"}]`,
	})
	checks, err := jobChecks("", "gitlab.com", rawPipeline{ID: 50, ProjectID: 7, Status: "failed"})
	if err != nil || len(checks) != 2 || checks[1].Name != "ship / deploy" || checks[1].Status != "FAILURE" {
		t.Fatalf("bridges: %+v %v", checks, err)
	}
	checks, err = jobChecks("", "gitlab.com", rawPipeline{ID: 51, ProjectID: 7, Status: "failed"})
	if err != nil || len(checks) != 2 || checks[1].Name != "pipeline" || checks[1].Status != "FAILURE" {
		t.Fatalf("a failure none of the jobs explains: %+v %v", checks, err)
	}
}

// A list of your merge requests fails when a part of one cannot be read:
// the watch must not take a pipeline it could not read for none, and
// say a failure gone that comes back on the next look.
func TestListMyPRsFailsOnAMissingPart(t *testing.T) {
	answers := map[string]string{
		"projects/grp%2Fapp/merge_requests?*":             `[{"iid":4,"state":"opened","source_project_id":7,"target_project_id":7}]`,
		"projects/grp%2Fapp/merge_requests/4/approvals":   `{"approvals_required":0}`,
		"projects/grp%2Fapp/merge_requests/4/reviewers":   `[]`,
		"projects/grp%2Fapp/merge_requests/4/pipelines?*": `[{"id":50,"project_id":7,"status":"success"}]`,
		"projects/7/pipelines/50/jobs*":                   `[{"id":60,"name":"unit","status":"success"}]`,
	}
	newFake(t, answers)
	fg := Forge{Origin: forge.Remote{Name: "origin", Host: "gitlab.com", Path: "grp/app"}}
	prs, err := fg.ListMyPRs("", 10)
	if err != nil || len(prs) != 1 || len(prs[0].Checks) != 1 || prs[0].Checks[0].Status != "SUCCESS" {
		t.Fatalf("list: %+v %v", prs, err)
	}
	delete(answers, "projects/grp%2Fapp/merge_requests/4/pipelines?*")
	newFake(t, answers)
	prs, err = fg.ListMyPRs("", 10)
	if err == nil {
		t.Fatalf("a pipeline that could not be read was taken for none: %+v", prs)
	}
	if fg.NoRepo(err) {
		t.Error("a part's 404 is not the project missing")
	}
	if _, err := fg.ListPRs("", 10); err != nil {
		t.Errorf("the list to show should keep the rest: %v", err)
	}
	// Bridges that timed out are not none: the watch would take a failed
	// trigger job for the pipeline and back.
	answers["projects/grp%2Fapp/merge_requests/4/pipelines?*"] = `[{"id":50,"project_id":7,"status":"success"}]`
	answers["projects/7/pipelines/50/bridges*"] = timeout
	newFake(t, answers)
	if _, err := fg.ListMyPRs("", 10); err == nil {
		t.Error("bridges that timed out were taken for none")
	}
	// Reviewers a GitLab too old to list them does not have are none.
	delete(answers, "projects/7/pipelines/50/bridges*")
	delete(answers, "projects/grp%2Fapp/merge_requests/4/reviewers")
	newFake(t, answers)
	if _, err := fg.ListMyPRs("", 10); err != nil {
		t.Errorf("no reviewers to list: %v", err)
	}
}

// A comment on a whole file, or on a line of a new file gone out of
// date, keeps its file.
func TestCommentOnAFileKeepsIt(t *testing.T) {
	newFake(t, map[string]string{
		"projects/grp%2Fapp/merge_requests/4/discussions*": `[{"notes":[{"id":1,"body":"x","author":{"id":1,"username":"a"},
			"position":{"new_path":"new.go","new_line":0,"old_path":"","old_line":0}}]}]`,
	})
	fg := Forge{Origin: forge.Remote{Name: "origin", Host: "gitlab.com", Path: "grp/app"}}
	cs, _ := fg.discussions("", "grp/app", rawMR{IID: 4})
	if len(cs) != 1 || cs[0].Path != "new.go" {
		t.Fatalf("comments: %+v", cs)
	}
}
