package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

func TestSpinoffEscSkipsTheRest(t *testing.T) {
	m := testModel(t)
	for _, x := range m.tabs {
		x.status = stExited // as detect finds a tab with no agent running
	}
	tb := m.cur()
	tb.Spinoff = &spinoff.Pending{Next: []spinoff.Part{
		{Branch: "fix-a", Summary: "A.", Prompt: "Do A."},
		{Branch: "fix-b", Summary: "B.", Prompt: "Do B."},
	}}
	m.openSpinoff(tb)
	d := m.dialog.(*spinoffDialog)

	d.key(m, keyMsg("ctrl+x"))
	if tb.Spinoff == nil || len(tb.Spinoff.Next) != 1 || m.dialog == nil {
		t.Fatal("ctrl+x skips one part and asks about the next")
	}
	d.key(m, keyMsg("esc"))
	if tb.Spinoff != nil || m.dialog != nil {
		t.Fatal("esc skips every part left, so nothing waits and the dialog is gone")
	}
	if n := len(tb.Messages); n != 1 || !strings.Contains(tb.Messages[0].Text, "fix-b") {
		t.Fatalf("the agent is told what became of the parts: %+v", tb.Messages)
	}
}

// The agent that split its work is told what became of it only once the
// agents of the tabs made have taken their prompts up, or have been waited
// for long enough: told sooner, one that looked for itself took a tab
// still starting for one whose prompt was lost.
func TestSpinoffReportWaitsForTheNewAgents(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	nt := &tab{}
	nt.ID, nt.Name = "new-a", "fix-a"
	m.tabs = append(m.tabs, nt)
	tb.SpinoffReport = []spinoff.Outcome{
		{Part: spinoff.Part{Branch: "fix-a"}, Branch: "fix-a", Dir: "/w/a", Tab: nt.ID},
		{Part: spinoff.Part{Branch: "fix-b"}, Skipped: true},
	}
	now := time.Now()

	m.reportWhenBegun(tb, now)
	if len(tb.Messages) != 0 || len(tb.SpinoffReport) != 2 {
		t.Fatalf("told before fix-a's agent started: %+v", tb.Messages)
	}
	nt.begun = true
	m.reportWhenBegun(tb, now.Add(time.Second))
	if len(tb.Messages) != 1 || !strings.Contains(tb.Messages[0].Text, "has started on it") || tb.SpinoffReport != nil {
		t.Fatalf("not told once it started: %+v", tb.Messages)
	}

	tb.Messages = nil
	nt.begun = false
	tb.SpinoffReport = []spinoff.Outcome{{Part: spinoff.Part{Branch: "fix-a"}, Branch: "fix-a", Tab: nt.ID}}
	tb.spinoffSince = time.Time{}
	m.reportWhenBegun(tb, now)
	m.reportWhenBegun(tb, now.Add(spinoffStartWait))
	if len(tb.Messages) != 1 || !strings.Contains(tb.Messages[0].Text, "not been seen starting") {
		t.Fatalf("not told once waited long enough: %+v", tb.Messages)
	}
}

// A part made into a copilot tab starts copilot with its prompt (-i)
// rather than typing it into copilot's screen, where it was lost while
// copilot was still starting. An agent that cannot take one so has it
// typed in from its queue, as before.
func TestSpinoffPromptStartsCopilot(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "copilot")
	script := "#!/bin/sh\n[ \"$1\" = --help ] && exit 0\nprintf '%s\\n' \"$@\" > " + args + "\nsleep 5\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m.cfg.Agents = map[agents.Kind]agents.Spec{
		agents.Copilot: {Command: []string{fake}},
		agents.Claude:  {Command: []string{"cat"}},
	}

	nt, _ := m.addTabBeside("fix-a", dir, "", nil, agents.Copilot, "Do A.")
	defer nt.pane.Close()
	if len(nt.Messages) != 0 || nt.Goal != "Do A." {
		t.Fatalf("the prompt is still queued: %+v, goal %q", nt.Messages, nt.Goal)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := os.ReadFile(args)
		if strings.Contains(string(got), "-i\nDo A.\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("copilot was started with %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	ct, _ := m.addTabBeside("fix-b", dir, "", nil, agents.Claude, "Do B.")
	defer ct.pane.Close()
	if len(ct.Messages) != 1 || ct.Messages[0].Text != "Do B." || !ct.Messages[0].Auto {
		t.Fatalf("claude's prompt is to be typed in: %+v", ct.Messages)
	}
}

// TestSpinoffPicksTheRepositories: in a tab with several repositories,
// the dialog ticks those the agent named, else those its commits are in,
// else all; ↑↓ and space change them, and with none it makes nothing.
func TestSpinoffPicksTheRepositories(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	var wts []workspace.Worktree
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(root, name)
		git(root, "init", "-q", dir)
		git(dir, "commit", "-q", "--allow-empty", "-m", name)
		wts = append(wts, workspace.Worktree{Repo: dir, Path: dir})
	}
	apiCommit := git(wts[0].Path, "rev-parse", "HEAD")

	m := testModel(t)
	fakeAgent(m, agents.Claude) // CI has no agent on the PATH
	tb := m.cur()
	tb.Worktrees = wts
	ticked := func(pt spinoff.Part) []bool {
		tb.Spinoff = &spinoff.Pending{Next: []spinoff.Part{pt}}
		m.dialog = nil
		m.openSpinoff(tb)
		return m.dialog.(*spinoffDialog).repoOn
	}
	part := spinoff.Part{Branch: "fix-a", Summary: "A.", Prompt: "Do A."}
	if got := ticked(part); !slices.Equal(got, []bool{true, true}) {
		t.Fatalf("with nothing to go by, all: %v", got)
	}
	part.Commits = []string{apiCommit}
	if got := ticked(part); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("the repository the commits are in: %v", got)
	}
	part.Repos = []string{"web", "db"}
	got := ticked(part)
	d := m.dialog.(*spinoffDialog)
	if !slices.Equal(got, []bool{false, true}) || !strings.Contains(d.repoNote, "db") {
		t.Fatalf("those the agent named, and a word on the one there is not: %v %q", got, d.repoNote)
	}
	if v := ansi.Strip(d.view(m, 120, 50)); !strings.Contains(v, "[ ] api") || !strings.Contains(v, "[x] web") {
		t.Fatalf("the dialog shows them ticked:\n%s", v)
	}

	d.key(m, keyMsg(" ")) // web, where the cursor starts: off
	if d.repoOn[1] || d.branch.Value() != "fix-a" {
		t.Fatalf("space turns the repository off, and is not typed: %v %q", d.repoOn, d.branch.Value())
	}
	d.key(m, keyMsg("enter"))
	if !strings.Contains(d.err, "at least one repository") {
		t.Fatalf("with none ticked nothing is made: %q", d.err)
	}
	d.key(m, keyMsg("up"))
	d.key(m, keyMsg(" "))
	if !d.repoOn[0] || d.repoOn[1] {
		t.Fatalf("↑ moves to api, space turns it on: %v", d.repoOn)
	}
	picked := d.agentIdx
	d.key(m, keyMsg("down"))
	d.key(m, keyMsg("down"))
	if d.onRepos || d.agentIdx != picked {
		t.Fatalf("↓ past the last repository goes on to the agents, the one picked kept: on repos %v, agent %d", d.onRepos, d.agentIdx)
	}
	d.key(m, keyMsg(" "))
	if !d.repoOn[0] || d.repoOn[1] {
		t.Fatalf("on the agents, space ticks no repository: %v", d.repoOn)
	}
}

// The dialog for a tab in one repository offers no list of them, until
// ctrl+r shows the others nerdr has; one the agent named is ticked, from
// an agent outside a sandbox only.
func TestSpinoffInAnotherRepository(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"api", "docs", "web"} {
		dir := filepath.Join(root, name)
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	m := testModel(t)
	m.root = root
	fakeAgent(m, agents.Claude)
	tb := m.cur()
	tb.Worktrees = []workspace.Worktree{{Repo: filepath.Join(root, "api"), Path: "/w/fix", Branch: "fix"}}
	open := func(pt spinoff.Part) *spinoffDialog {
		tb.Spinoff = &spinoff.Pending{Next: []spinoff.Part{pt}}
		m.dialog = nil
		m.openSpinoff(tb)
		return m.dialog.(*spinoffDialog)
	}
	part := spinoff.Part{Branch: "fix-a", Summary: "A.", Prompt: "Do A.", Commits: []string{"c1"}}
	d := open(part)
	if v := ansi.Strip(d.view(m, 120, 50)); strings.Contains(v, "Repositories:") || !strings.Contains(v, "ctrl+r") ||
		!strings.Contains(v, "Start the branch with its 1 commit") {
		t.Fatalf("in the tab's one repository, no list, and ctrl+r for more:\n%s", v)
	}

	d.key(m, keyMsg("ctrl+r"))
	if v := ansi.Strip(d.view(m, 120, 50)); !strings.Contains(v, "[x] fix") || !strings.Contains(v, "[ ] docs") || !strings.Contains(v, "[ ] web") {
		t.Fatalf("ctrl+r lists the others, unticked:\n%s", v)
	}
	d.key(m, keyMsg(" ")) // docs, where the cursor went
	d.key(m, keyMsg("ctrl+r"))
	if len(d.repos) != 2 || d.repos[1].Name != "docs" || !d.repoOn[1] {
		t.Fatalf("ctrl+r again keeps those ticked: %+v %v", d.repos, d.repoOn)
	}

	part.Repos = []string{"docs"}
	d = open(part)
	if !slices.Equal(d.repoOn, []bool{false, true}) || d.ownTicked() {
		t.Fatalf("the other repository the agent named, alone: %v", d.repoOn)
	}
	if v := ansi.Strip(d.view(m, 120, 50)); !strings.Contains(v, "writes it anew") || !strings.Contains(v, "not in this tab") {
		t.Fatalf("its commits cannot go there:\n%s", v)
	}

	part.Sandboxed = true
	m.cfg.Agents[agents.Claude] = agents.Spec{Command: []string{"cat"}, Sandboxed: true}
	d = open(part)
	if v := ansi.Strip(d.view(m, 120, 50)); len(d.repos) != 1 || !strings.Contains(v, "named docs: ctrl+r to tick it yourself") {
		t.Fatalf("from a sandbox, the tab's alone, and a word on it with no list:\n%s", v)
	}
	part.Sandboxed, part.Repos = false, []string{"db"}
	d = open(part)
	if v := ansi.Strip(d.view(m, 120, 50)); !strings.Contains(v, "named db, which nerdr does not have") {
		t.Fatalf("one nerdr does not have:\n%s", v)
	}
}

// The part's commits are brought into the tab's own repositories alone,
// not into another that has them too, as a fork does.
func TestSpinoffPicksInTheTabsRepositoriesAlone(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	api, fork := filepath.Join(root, "api"), filepath.Join(root, "fork")
	git(root, "init", "-q", "-b", "main", api)
	git(api, "commit", "-q", "--allow-empty", "-m", "init")
	git(api, "switch", "-q", "-c", "work")
	if err := os.WriteFile(filepath.Join(api, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(api, "add", "a.txt")
	git(api, "commit", "-q", "-m", "a")
	c := git(api, "rev-parse", "HEAD")
	git(api, "switch", "-q", "main")
	git(root, "clone", "-q", api, fork) // has c too
	git(fork, "switch", "-q", "main")

	m := testModel(t)
	m.wtRoot = filepath.Join(root, "worktrees")
	tb := m.cur()
	pt := spinoff.Part{Branch: "split", Summary: "A.", Prompt: "Do A.", Commits: []string{c}}
	repos := []workspace.Repo{{Name: "api", Path: api}, {Name: "fork", Path: fork}}
	res := m.spinoffCreate(tb, pt, agents.Claude, "split", true, false, "", repos, map[string]bool{api: true})().(spinoffMsg)
	if res.err != nil || len(res.wts) != 2 || res.outcome.PickFailed != "" {
		t.Fatalf("made: %+v", res)
	}
	for _, w := range res.wts {
		_, err := os.Stat(filepath.Join(w.Path, "a.txt"))
		if mine := w.Repo == api; mine != (err == nil) {
			t.Errorf("%s: picked %v, want %v", w.Repo, err == nil, mine)
		}
	}
}

// Plans sent one after another wait together, no more than one plan's
// parts: the rest are left out, and the agent is told.
func TestSpinoffPartsWaitingAreBounded(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	parts := func() []spinoff.Part {
		var out []spinoff.Part
		for i := range 15 {
			out = append(out, spinoff.Part{Branch: "p" + strings.Repeat("x", i+1), Summary: "S.", Prompt: "Do."})
		}
		return out
	}
	m.dialog = &closeTab{t: tb} // the dialog waits; the parts are kept
	for range 2 {
		m.onSpinoff(hooks.Report{Method: hooks.Spinoff, Pane: tb.hookKey(), Spinoff: parts()})
	}
	if n := len(tb.Spinoff.Next); n != spinoff.MaxParts {
		t.Fatalf("%d parts wait, want %d", n, spinoff.MaxParts)
	}
	if n := len(tb.Messages); n != 1 || !strings.Contains(tb.Messages[0].Text, "10 of the parts") {
		t.Errorf("the agent is told: %+v", tb.Messages)
	}
}
