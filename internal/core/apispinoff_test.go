package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// spinoffCore is a core with one tab, on a worktree of a repository of its
// own with a commit on a branch of its, whose agent sent two parts: the
// first with its prompt and the commit, the second with neither yet.
func spinoffCore(t *testing.T) (*Core, *Tab, string) {
	t.Helper()
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	repo := filepath.Join(root, "api")
	git(root, "init", "-q", "-b", "main", repo)
	git(repo, "commit", "-q", "--allow-empty", "-m", "init")
	git(repo, "switch", "-q", "-c", "work")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "a.txt")
	git(repo, "commit", "-q", "-m", "a")
	c := git(repo, "rev-parse", "HEAD")

	tab := &Tab{}
	tab.Tab = state.Tab{ID: "t1", Name: "work", Dir: repo, Agent: agents.Claude, Group: "g",
		Worktrees: []workspace.Worktree{{Repo: repo, Path: repo, Branch: "work"}}}
	tab.Light = StBlocked
	tab.Spinoff = &spinoff.Pending{Next: []spinoff.Part{
		{ID: "p1", Branch: "fix-a", Summary: "A.", Prompt: "Do A.", Commits: []string{c}},
		{ID: "p2", Branch: "fix-b", Summary: "B."},
	}}
	m := &Core{Client: NoClient{}, Root: root, WtRoot: filepath.Join(root, "worktrees"), Tabs: []*Tab{tab},
		Agents: []agents.Kind{agents.Codex, agents.Claude}, AgentsAt: time.Now()}
	return m, tab, repo
}

// TestAPIStateHasTheSpinoff: the parts waiting are in the tab's state, in
// order, with whether each prompt has come but not the prompt.
func TestAPIStateHasTheSpinoff(t *testing.T) {
	m, tab, _ := spinoffCore(t)
	tab.SpinoffFailed = &SpinoffFailure{Branch: "fix-a", Err: "taken"}
	s := m.APIState().Tabs[0].Spinoff
	if s == nil || len(s.Parts) != 2 || s.Done != 0 || s.Failed == nil || s.Failed.Err != "taken" {
		t.Fatalf("spinoff %+v", s)
	}
	p1, p2 := s.Parts[0], s.Parts[1]
	if p1.ID != "p1" || p1.Branch != "fix-a" || p1.Summary != "A." || !p1.PromptReady || len(p1.Commits) != 1 ||
		p1.Kind != "claude" || strings.Join(p1.Kinds, ",") != "codex,claude" {
		t.Fatalf("part 1 %+v", p1)
	}
	if p2.ID != "p2" || p2.PromptReady {
		t.Fatalf("part 2 %+v", p2)
	}
	tab.Spinoff = nil
	if s := m.APIState().Tabs[0].Spinoff; s != nil {
		t.Fatalf("none wait: %+v", s)
	}
}

// TestAPISpinoffAnswersTheFirstAlone: only the first part is answered, and
// what cannot be done is said.
func TestAPISpinoffAnswersTheFirstAlone(t *testing.T) {
	m, tab, _ := spinoffCore(t)
	no := false
	for _, tc := range []struct {
		q    APISpinoffMake
		want string
	}{
		{APISpinoffMake{Tab: "nope", Part: "p1"}, `no tab "nope"`},
		{APISpinoffMake{Tab: "t1"}, "name the part"},
		{APISpinoffMake{Tab: "t1", Part: "p2"}, "p2 is not the first"},
		{APISpinoffMake{Tab: "t1", Part: "p9"}, "no part p9 waits"},
		{APISpinoffMake{Tab: "t1", Part: "p1", Kind: "gemini"}, `no agent "gemini"`},
		{APISpinoffMake{Tab: "t1", Part: "p1", Branch: "a b"}, "use letters"},
		{APISpinoffMake{Tab: "t1", Part: "p1", Repos: []string{"web"}}, "no repository web"},
		{APISpinoffMake{Tab: "t1", Part: "p1", Reuse: &no, Repos: []string{"web"}}, "no repository web"},
	} {
		if _, err := m.APISpinoffCreate(tc.q, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.q, err, tc.want)
		}
	}
	if _, err := m.APISpinoffSkip("t1", "p2"); err == nil || !strings.Contains(err.Error(), "not the first") {
		t.Errorf("skip of the second: %v", err)
	}
	tab.SpinoffBusy = "making fix-a…"
	if _, err := m.APISpinoffSkip("t1", "p1"); err == nil || !strings.Contains(err.Error(), "being made") {
		t.Errorf("skip while it is made: %v", err)
	}
	tab.SpinoffBusy = ""

	tab.Spinoff.Next[0].Sandboxed = true
	if _, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1", Kind: "codex"}, nil); err == nil ||
		!strings.Contains(err.Error(), "not set sandboxed") {
		t.Errorf("an agent not sandboxed for a part from one in a sandbox: %v", err)
	}
	if _, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1"}, nil); err == nil ||
		!strings.Contains(err.Error(), "no agent is set sandboxed") {
		t.Errorf("no agent sandboxed: %v", err)
	}
	tab.Spinoff.Next[0].Sandboxed = false

	if _, err := m.APISpinoffSkip("t1", "p1"); err != nil {
		t.Fatal(err)
	}
	if s := m.APIState().Tabs[0].Spinoff; s == nil || len(s.Parts) != 1 || s.Parts[0].ID != "p2" || s.Done != 1 {
		t.Fatalf("after the skip: %+v", s)
	}
	if _, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p2"}, nil); err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Errorf("a part whose prompt has not come: %v", err)
	}
	if _, err := m.APISpinoffSkip("t1", "p2"); err != nil {
		t.Fatal(err)
	}
	// The agent is told, as no tab made is to be waited for.
	if tab.Spinoff != nil || m.APIState().Tabs[0].Spinoff != nil || len(tab.Messages) != 1 || !strings.Contains(tab.Messages[0].Text, "fix-b") {
		t.Fatalf("all gone through: %+v %+v", tab.Spinoff, tab.Messages)
	}
}

// TestAPISpinoffCreate makes the first part's branch as the dialog does,
// starting with its commits; a name taken is kept as the failure, the
// part still first, until it is tried again.
func TestAPISpinoffCreate(t *testing.T) {
	m, tab, repo := spinoffCore(t)
	cmd, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1", Branch: "main"}, nil)
	if err != nil || tab.SpinoffBusy != "making main…" {
		t.Fatalf("%v, busy %q", err, tab.SpinoffBusy)
	}
	if s := m.APIState().Tabs[0].Spinoff; s.Busy != "making main…" {
		t.Fatalf("busy in the state: %+v", s)
	}
	m.SpinoffMade(cmd().(SpinoffMsg))
	s := m.APIState().Tabs[0].Spinoff
	if s == nil || s.Busy != "" || s.Failed == nil || s.Failed.Branch != "main" || !strings.Contains(s.Failed.Err, "already exists") ||
		s.Parts[0].ID != "p1" {
		t.Fatalf("a branch taken: %+v", s)
	}

	cmd, err = m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1", Repos: []string{"api"}}, nil)
	if err != nil || tab.SpinoffFailed != nil {
		t.Fatalf("%v, failed %+v", err, tab.SpinoffFailed)
	}
	res := cmd().(SpinoffMsg)
	if res.Err != nil || len(res.Wts) != 1 || res.Kind != agents.Claude || res.Group != "g" ||
		!res.Outcome.Reused || res.Outcome.Elsewhere || res.Outcome.Branch != "fix-a" || filepath.Clean(res.Wts[0].Repo) != repo {
		t.Fatalf("made: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(res.Wts[0].Path, "a.txt")); err != nil {
		t.Errorf("the branch starts with the part's commit: %v", err)
	}
}

// TestAPISpinoffCreateWritesAnew: reuse false starts the branch without
// the part's commits, with the agent named.
func TestAPISpinoffCreateWritesAnew(t *testing.T) {
	m, _, _ := spinoffCore(t)
	no := false
	cmd, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1", Branch: "fix-a2", Kind: "Codex", Reuse: &no}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := cmd().(SpinoffMsg)
	if res.Err != nil || res.Outcome.Reused || res.Kind != agents.Codex || res.Outcome.Branch != "fix-a2" {
		t.Fatalf("made: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(res.Wts[0].Path, "a.txt")); err == nil {
		t.Error("the commit was brought in")
	}
}

// spinoffAsked has t's repository ask for a setup not yet allowed, so
// making a part waits for the user's answer.
func spinoffAsked(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, workspace.RepoConfigFile), []byte("setup = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAPISpinoffHeldWhileSetupIsAsked: while the setup is asked about,
// the part can be neither made again nor skipped, and the answer makes
// it, the next part still waiting.
func TestAPISpinoffHeldWhileSetupIsAsked(t *testing.T) {
	m, tab, repo := spinoffCore(t)
	spinoffAsked(t, repo)
	if _, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(m.Asking) != 1 || m.Asking[0].Kind != AskRepoSetup || tab.SpinoffBusy == "" {
		t.Fatalf("asked %+v, busy %q", m.Asking, tab.SpinoffBusy)
	}
	if _, err := m.APISpinoffSkip("t1", "p1"); err == nil || !strings.Contains(err.Error(), "being made") {
		t.Errorf("skip while the setup is asked about: %v", err)
	}
	if _, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1", Branch: "fix-a2"}, nil); err == nil {
		t.Error("made a second time while the setup is asked about")
	}
	if cmd := (SpinoffCreate{T: m.Ref(tab), Part: tab.Spinoff.Next[0], Kind: agents.Claude, Name: "fix-a3"}).Do(m); cmd != nil || len(m.Asking) != 1 {
		t.Errorf("a request from the TUI meanwhile is not taken: %d asked", len(m.Asking))
	}
	cmd := m.Answer(m.Asking[0].ID, RunDeclined)
	if cmd == nil || tab.SpinoffBusy != "making fix-a…" {
		t.Fatalf("the answer makes it: busy %q", tab.SpinoffBusy)
	}
	if res := cmd().(SpinoffMsg); res.Err != nil || res.Outcome.Part.ID != "p1" || res.Outcome.Branch != "fix-a" {
		t.Fatalf("made %+v", res)
	}
	if ids := []string{tab.Spinoff.Next[0].ID, tab.Spinoff.Next[1].ID}; ids[0] != "p1" || ids[1] != "p2" {
		t.Fatalf("parts %v", ids)
	}
}

// TestAPISpinoffGoneWhileSetupIsAsked: a part gone through some other way
// while its setup was asked about is not made by the answer, nor is the
// next in its place.
func TestAPISpinoffGoneWhileSetupIsAsked(t *testing.T) {
	m, tab, repo := spinoffCore(t)
	spinoffAsked(t, repo)
	if _, err := m.APISpinoffCreate(APISpinoffMake{Tab: "t1", Part: "p1"}, nil); err != nil {
		t.Fatal(err)
	}
	m.SpinoffNext(tab, spinoff.Outcome{Part: tab.Spinoff.Next[0], Skipped: true})
	if cmd := m.Answer(m.Asking[0].ID, AnswerAllowed); cmd != nil || tab.SpinoffBusy != "" {
		t.Fatalf("the answer made something: busy %q", tab.SpinoffBusy)
	}
	if len(tab.Spinoff.Next) != 1 || tab.Spinoff.Next[0].ID != "p2" {
		t.Fatalf("parts %+v", tab.Spinoff.Next)
	}
}

// TestSpinoffHold: a client making the part elsewhere holds it, the first
// alone and once; it is let go of by its own job, or in time.
func TestSpinoffHold(t *testing.T) {
	m, tab, _ := spinoffCore(t)
	var told []Event
	m.Observe = func(ev Event) { told = append(told, ev) }
	hold := func(job, part string) string {
		told = nil
		SpinoffHold{Job: job, T: m.Ref(tab), Part: part, Busy: "making fix-a on box…"}.Do(m)
		return told[len(told)-1].(SpinoffHeldMsg).Err
	}
	if err := hold("j1", "p2"); !strings.Contains(err, "no longer the one") {
		t.Errorf("held the second part: %q", err)
	}
	if err := hold("j1", "p1"); err != "" || tab.SpinoffBusy != "making fix-a on box…" {
		t.Fatalf("%q, busy %q", err, tab.SpinoffBusy)
	}
	if err := hold("j2", "p1"); !strings.Contains(err, "being made already") {
		t.Errorf("held twice: %q", err)
	}
	SpinoffHold{Job: "j2", T: m.Ref(tab), Release: true}.Do(m)
	if tab.SpinoffBusy == "" {
		t.Error("another job let go of the hold")
	}
	m.ExpireSpinoffHolds(time.Now())
	if tab.SpinoffBusy == "" {
		t.Error("let go of before its time")
	}
	m.ExpireSpinoffHolds(time.Now().Add(SpinoffHoldFor + time.Second))
	if tab.SpinoffBusy != "" || tab.SpinoffHold != "" {
		t.Errorf("not let go of in time: %q", tab.SpinoffBusy)
	}
	if err := hold("j3", "p1"); err != "" {
		t.Fatal(err)
	}
	SpinoffHold{Job: "j3", T: m.Ref(tab), Release: true}.Do(m)
	if tab.SpinoffBusy != "" {
		t.Error("its own job did not let go of it")
	}
}
