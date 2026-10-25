package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"nerdr/internal/agents"
	"nerdr/internal/hooks"
	"nerdr/internal/spinoff"
	"nerdr/internal/workspace"

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
