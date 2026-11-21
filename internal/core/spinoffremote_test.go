package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The target refuses what it cannot make, before it does anything, and
// says why in the dialog.
func TestSpinoffLandRefuses(t *testing.T) {
	root := t.TempDir()
	cl := &keptClient{}
	c := &Core{Root: root, WtRoot: root + "/wt", Wake: make(chan struct{}, 1), Client: cl,
		Cfg:    config.Config{Install: "x", Agents: map[agents.Kind]agents.Spec{}},
		Agents: []agents.Kind{agents.Codex}, AgentsAt: time.Now()}
	c.Scan.Repos = []workspace.Repo{{Name: "proj", Path: root + "/proj"}}
	ok := SpinoffLand{Job: "j1", Kind: agents.Codex, Name: "part-a", Part: spinoff.Part{Branch: "part-a"},
		Repos: []SpinoffWhere{{Name: "proj", Path: root + "/proj", Own: true}}}
	if why := c.landPartRefusal(ok); why != "" {
		t.Fatalf("refused: %s", why)
	}
	for want, q := range map[string]SpinoffLand{
		"claude is not on PATH there": func() SpinoffLand { q := ok; q.Kind = agents.Claude; return q }(),
		"not set sandboxed there":     func() SpinoffLand { q := ok; q.Part.Sandboxed = true; return q }(),
		"no repository /elsewhere":    func() SpinoffLand { q := ok; q.Repos = []SpinoffWhere{{Name: "x", Path: "/elsewhere"}}; return q }(),
		"commits did not come":        func() SpinoffLand { q := ok; q.Reuse = true; return q }(),
		"no origin to clone it from":  func() SpinoffLand { q := ok; q.Repos = []SpinoffWhere{{Name: "x"}}; return q }(),
		"no spinoff ../x":             func() SpinoffLand { q := ok; q.Job = "../x"; return q }(),
	} {
		cl.evs = nil
		q.Do(c)
		if len(cl.evs) != 1 {
			t.Fatalf("%s: told %v", want, cl.evs)
		}
		if msg, _ := cl.evs[0].(SpinoffLandedMsg); !strings.Contains(msg.Err, want) {
			t.Errorf("want %q, told %+v", want, cl.evs[0])
		}
	}
	if len(c.Tabs) != 0 {
		t.Error("a tab was made")
	}
}

// The report on parts made on another machine does not wait for their
// agents, which this core cannot see.
func TestSpinoffReportDoesNotWaitForAnotherMachine(t *testing.T) {
	c := &Core{Wake: make(chan struct{}, 1), Client: &keptClient{}}
	tb := &Tab{}
	tb.ID = "1"
	tb.SpinoffReport = []spinoff.Outcome{{Part: spinoff.Part{Branch: "a"}, Branch: "a", Tab: "9", Machine: "box"}}
	c.Tabs = []*Tab{tb}
	c.ReportWhenBegun(tb, time.Now())
	if tb.SpinoffReport != nil || len(tb.Messages) != 1 || !strings.Contains(tb.Messages[0].Text, "on the machine box") {
		t.Fatalf("report %+v, messages %+v", tb.SpinoffReport, tb.Messages)
	}
}

// A refusal after the commits came lets go of them.
func TestSpinoffLandRefusedDropsTheCommits(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "in")
	os.Mkdir(dir, 0o700)
	c := &Core{Root: root, WtRoot: root + "/wt", Wake: make(chan struct{}, 1), Client: &keptClient{},
		Cfg: config.Config{Install: "x"}, Agents: []agents.Kind{agents.Codex}, AgentsAt: time.Now(),
		movesIn: map[string]string{"j1": dir}}
	SpinoffLand{Job: "j1", Kind: agents.Claude, Reuse: true, Repos: []SpinoffWhere{{Name: "p", Path: root}}}.Do(c)
	if c.movesIn["j1"] != "" {
		t.Fatal("the commits are kept")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the folder is left")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// With the agent no longer set sandboxed once the worktree is made for a
// part from a sandbox, what was made goes again, and the client is told
// it was not made: the part waits for the user.
func TestSpinoffLandedSandboxGoneUndoes(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	repo, wt := filepath.Join(root, "proj"), filepath.Join(root, "wt", "part-a")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "commit", "-q", "--allow-empty", "-m", "first"},
		{"-C", repo, "worktree", "add", "-q", "-b", "part-a", wt}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitsafe.Trust(repo, wt)
	c := &Core{Root: root, Wake: make(chan struct{}, 1), Client: &keptClient{}, Cfg: config.Config{Install: "x"}}
	w := workspace.Worktree{Repo: repo, Path: wt, Branch: "part-a"}
	q := SpinoffLand{Job: "j1", Kind: agents.Codex, Name: "part-a", Part: spinoff.Part{Branch: "part-a", Sandboxed: true}}
	cmd := c.onSpinoffLanded(spinoffLanded{q: q, res: SpinoffMsg{Wts: []workspace.Worktree{w},
		Plan: workspace.Plan{Branch: "part-a", Dir: wt}, Outcome: spinoff.Outcome{Part: q.Part, Branch: "part-a"}}})
	if cmd == nil {
		t.Fatal("no command")
	}
	r, _ := cmd().(Reply)
	msg, _ := r.Ev.(SpinoffLandedMsg)
	if !strings.Contains(msg.Err, "no longer set sandboxed") || len(c.Tabs) != 0 {
		t.Fatalf("told %+v, %d tabs", r.Ev, len(c.Tabs))
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("the worktree is left")
	}
	if out, _ := exec.Command("git", "-C", repo, "branch", "--list", "part-a").Output(); strings.TrimSpace(string(out)) != "" {
		t.Error("the branch is left")
	}
}

// A repository cloned since the repositories were last looked for is
// found in the folder it was cloned into, and not cloned again.
func TestFindRepoTakesTheCloneThere(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	p := filepath.Join(root, "proj")
	for _, args := range [][]string{{"init", "-q", p}, {"-C", p, "remote", "add", "origin", "https://example.com/o/proj.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	got := findRepo(MoveRepo{Name: "proj", Origin: "git@example.com:o/proj.git"}, nil, root, true)
	if got.Path != p || got.Err != "" {
		t.Fatalf("found %+v", got)
	}
	other := findRepo(MoveRepo{Name: "proj", Origin: "https://example.com/o/other.git"}, nil, root, false)
	if other.Err == "" || other.Path != "" {
		t.Fatalf("took a folder of another origin: %+v", other)
	}
}
