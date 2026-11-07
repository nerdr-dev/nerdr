package core

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// heldCore is a core over a holder in this process, with one tab whose
// agent is script, not started yet.
func heldCore(t *testing.T, cl *holder.Client, script string) (*Core, *Tab) {
	t.Helper()
	held, err := NewHeld(cl)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(script)
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "tab1", Name: "a", Dir: dir, Agent: agents.Codex}}}
	c := &Core{Client: NoClient{}, Tabs: []*Tab{tb}, Wake: make(chan struct{}, 1), Held: held,
		Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{script}}}}}
	return c, tb
}

func agentScript(t *testing.T, body string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// Over a holder, an agent and a terminal run there, marked with what a
// core started later needs to take them up: the agent's sandbox and the
// secret its reports are known by, and which tab each is of.
func TestProgramsRunInTheHolderMarked(t *testing.T) {
	cl := inProcessHolder(t)
	c, tb := heldCore(t, cl, agentScript(t, "echo ready; exec cat\n"))
	c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatalf("not started: %s", tb.Err)
	}
	c.NewShellIn(tb, true)
	progs, err := cl.List()
	if err != nil || len(progs) != 2 {
		t.Fatalf("list: %+v %v", progs, err)
	}
	for _, p := range progs {
		l, ok := ParseLabel(p.Label)
		if !ok || l.Tab != "tab1" {
			t.Fatalf("label of %s: %q", p.ID, p.Label)
		}
		switch l.Kind {
		case HeldAgent:
			if p.Secret != tb.HookKey() || p.Sandboxed != tb.InSandbox || p.Pid != tb.Pane.Pid() {
				t.Errorf("agent: %+v", p)
			}
		case HeldShell:
			if l.N != 1 || p.Secret != "" {
				t.Errorf("shell: %+v %+v", p, l)
			}
		default:
			t.Errorf("kind %q", l.Kind)
		}
	}
}

// Once a held pane has gone quiet, the holder is sent its screen, at the
// position the screen has got to; not again while nothing more is drawn.
func TestQuietScreensAreCheckpointed(t *testing.T) {
	cl := inProcessHolder(t)
	c, tb := heldCore(t, cl, agentScript(t, "printf 'hello from the agent'; exec cat\n"))
	c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatalf("not started: %s", tb.Err)
	}
	id := tb.Pane.HeldID()
	stop := make(chan struct{})
	defer close(stop)
	go c.Checkpoints(stop)
	waitFor(t, "a checkpoint", func() bool {
		pos, _, err := cl.FetchCheckpoint(id)
		return err == nil && pos == int64(len("hello from the agent"))
	})
	_, block, _ := cl.FetchCheckpoint(id)
	sc := term.NewScreen(10, 3)
	if err := sc.Restore(block); err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(strings.TrimSpace(sc.Snapshot().Screen), "\n", ""); got != "hello from the agent" {
		t.Fatalf("checkpoint shows %q", got)
	}
}

// Finding the held panes leaves the plugins' runs and screens alone.
func TestHeldPanesStopsNothing(t *testing.T) {
	cl := inProcessHolder(t)
	c, _ := heldCore(t, cl, agentScript(t, "exec cat\n"))
	run := &PluginRun{Id: "r", Proc: &RunProc{Proc: &signalled{}, Stopped: make(chan struct{})}}
	c.PluginRuns.Runs = map[string]*PluginRun{"k": run}
	c.PluginRuns.ByID = map[string]*PluginRun{"r": run}
	c.Mu.Lock()
	c.heldPanes()
	c.Mu.Unlock()
	if run.Proc.Proc.(*signalled).asked {
		t.Fatal("a run was stopped")
	}
}

// signalled is a program that is gone, and says whether it was asked.
type signalled struct {
	spawn.Proc
	asked bool
}

func (s *signalled) Pid() int { return 1 << 22 }
func (s *signalled) Signal(pg int, sig syscall.Signal) error {
	s.asked = true
	return syscall.ESRCH
}

// A core started after one that crashed takes up the agent and terminal
// the holder kept: the same processes, the agent known by the same secret,
// and the screens as they were, from the checkpoint and the output after
// it.
func TestANewCoreTakesUpWhatTheHolderKept(t *testing.T) {
	h := holder.New()
	clA, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	script := agentScript(t, "echo before; exec cat\n")
	a, tabA := heldCore(t, clA, script)
	a.Start(tabA, false)
	if tabA.Pane == nil {
		t.Fatalf("not started: %s", tabA.Err)
	}
	shell, _ := a.NewShellIn(tabA, true)
	if shell == nil {
		t.Fatal("no terminal")
	}
	waitFor(t, "the agent's first line", func() bool { return strings.Contains(tabA.Pane.Snapshot().Screen, "before") })
	waitFor(t, "the agent to go quiet", func() bool { return tabA.Pane.Quiet() >= CheckpointQuiet })
	a.checkpoint(heldPane{tabA.Pane.HeldID(), tabA.Pane}, map[string]int64{})
	if pos, _, _ := clA.FetchCheckpoint(tabA.Pane.HeldID()); pos <= 0 {
		t.Fatalf("no checkpoint: %d", pos)
	}
	tabA.Pane.Write([]byte("after\r"))
	waitFor(t, "the line after the checkpoint", func() bool { return strings.Contains(tabA.Pane.Snapshot().Screen, "after") })
	pid, shellPid, secret := tabA.Pane.Pid(), shell.Shell.Pid(), tabA.HookKey()
	clA.Close() // the core is gone, its panes with it

	clB, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clB.Shutdown() })
	b, tabB := heldCore(t, clB, script)
	b.Mu.Lock()
	b.TakeUpHeld()
	b.Start(tabB, true)
	b.Mu.Unlock()
	if tabB.Pane == nil || tabB.Pane.Pid() != pid || tabB.HookKey() != secret {
		t.Fatalf("agent not taken up: %+v", tabB.Pane)
	}
	want := tabA.Pane.Text(100)
	waitFor(t, "the screen as it was", func() bool { return tabB.Pane.Text(100) == want })
	if !strings.Contains(want, "before") || !strings.Contains(want, "after") {
		t.Fatalf("the agent showed %q", want)
	}
	if tabB.Pane.Incomplete() {
		t.Error("marked incomplete")
	}
	var shells []*PaneTab
	for _, p := range tabB.Panes() {
		if p.Kind == PaneShell {
			shells = append(shells, p)
		}
	}
	if len(shells) != 1 || shells[0].Shell.Pid() != shellPid || shells[0].N != shell.N {
		t.Fatalf("terminal not taken up: %+v", shells)
	}
	tabB.Pane.Write([]byte("on\r"))
	waitFor(t, "the agent to go on", func() bool { return strings.Count(tabB.Pane.Text(100), "on") >= 2 })
	if b.HeldLeft() != 0 {
		t.Errorf("%d programs left", b.HeldLeft())
	}
}

// A plugin's screen and a run of it, started by a core that crashed, go
// back to their tab: the run with what it printed, and the screen with the
// file it is told of runs in.
func TestANewCoreTakesUpPluginsFromTheHolder(t *testing.T) {
	h := holder.New()
	clA, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	events := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(events, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	spawnLabelled := func(s spawn.Spec, l Label) {
		c := &Core{Held: &Held{Client: clA}}
		c.Hold(&s, l, false, "")
		if _, err := clA.Spawn(s); err != nil {
			t.Fatal(err)
		}
	}
	spawnLabelled(spawn.Spec{Argv: []string{"sh", "-c", "echo screen; exec cat"}, PTY: true, W: 20, H: 4},
		Label{Kind: HeldPluginUI, Tab: "tab1", Plugin: "/p", Key: "k", Events: events})
	spawnLabelled(spawn.Spec{Argv: []string{"sh", "-c", "echo ran; sleep 2; echo later; exec sleep 60"}},
		Label{Kind: HeldRun, Tab: "tab1", Plugin: "/p", Key: "k", Run: "r1", From: "tab1", Watch: true})
	waitFor(t, "the run's first line in the holder", func() bool {
		progs, _ := clA.List()
		for _, p := range progs {
			if l, _ := ParseLabel(p.Label); l.Kind == HeldRun && p.Pos > 0 {
				return true
			}
		}
		return false
	})
	spawnLabelled(spawn.Spec{Argv: []string{"sleep", "60"}}, Label{Kind: HeldShell, Tab: "closed"})
	clA.Close()

	clB, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clB.Shutdown() })
	b, tb := heldCore(t, clB, agentScript(t, "exec cat\n"))
	b.Mu.Lock()
	b.TakeUpHeld()
	b.Mu.Unlock()
	u := b.PluginUIs["tab1\x00/p"]
	if u == nil || u.Key != "k" || u.Events == nil || u.Events.Name() != events {
		t.Fatalf("plugin screen: %+v", u)
	}
	waitFor(t, "the plugin's screen", func() bool { return strings.Contains(u.Pane.Text(10), "screen") })
	b.PluginEvent(tb, "/p", map[string]any{"run": "r1"})
	if got, _ := os.ReadFile(events); !strings.Contains(string(got), "r1") {
		t.Fatalf("events file: %q", got)
	}
	run := b.PluginRuns.ByID["r1"]
	if run == nil || run.T != tb || run.From != tb || !run.Watch || run.Dir != "/p" {
		t.Fatalf("run: %+v", run)
	}
	waitFor(t, "what the run printed", func() bool {
		run.Proc.Mu.Lock()
		defer run.Proc.Mu.Unlock()
		return strings.Contains(string(run.Proc.Raw), "ran")
	})
	// What it printed before the crash was told of then: only what comes
	// after goes to the plugin's screen and the agent watching.
	b.Mu.Lock()
	b.PumpRuns(time.Now())
	b.Mu.Unlock()
	if got, _ := os.ReadFile(events); strings.Contains(string(got), "ran") {
		t.Fatalf("told again: %q", got)
	}
	waitFor(t, "the run's next line", func() bool {
		b.Mu.Lock()
		defer b.Mu.Unlock()
		b.PumpRuns(time.Now())
		got, _ := os.ReadFile(events)
		return strings.Contains(string(got), "later")
	})
	if got, _ := os.ReadFile(events); strings.Contains(string(got), "ran") {
		t.Fatalf("told again: %q", got)
	}
	if b.HeldLeft() != 1 {
		t.Errorf("%d programs left, want the closed tab's terminal", b.HeldLeft())
	}
}

// A review going on when the core crashed is taken up by the core started
// again: the session kept it, the holder its reviewer, which sent its
// review before the crash and is the same process after.
func TestANewCoreTakesUpAReview(t *testing.T) {
	h := holder.New()
	clA, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	script := agentScript(t, "echo agent; exec cat\n")
	a, tabA := heldCore(t, clA, script)
	a.Root = t.TempDir()
	reviewer := []string{agentScript(t, "echo reviewing; exec cat\n")}
	a.Cfg.Agents[agents.Claude] = agents.Spec{Command: reviewer}
	a.Cfg.Agents[agents.OpenCode] = agents.Spec{Command: reviewer}
	a.Start(tabA, false)
	a.StartReview(tabA, Reviewer{Kind: agents.Claude}, "main", "tree1", "the parser")
	a.AddReviewer(tabA, Reviewer{Kind: agents.OpenCode})
	if tabA.Review == nil || len(tabA.Review.Seats) != 2 {
		t.Fatalf("no review: %+v", tabA.Review)
	}
	r := tabA.Review.Seats[0].R
	a.OnReview(hooks.Report{Pane: r.HookKey(), Review: &codereview.Result{Verdict: "approve", Summary: "fine"}})
	if tabA.Review == nil || tabA.Review.Seats[0].Res == nil {
		t.Fatal("the review ended, or the verdict was not taken")
	}
	phase, round, pid := tabA.Review.Phase, tabA.Review.Round, r.Pane.Pid()
	clA.Close()

	sess, err := state.Load(a.Root)
	if err != nil || len(sess.Tabs) != 1 || sess.Tabs[0].Review == nil {
		t.Fatalf("session: %+v %v", sess, err)
	}
	clB, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clB.Shutdown() })
	b, _ := heldCore(t, clB, script)
	b.Root = a.Root
	tabB := &Tab{AgentState: Agent{Tab: sess.Tabs[0], Light: StStarting}}
	b.Tabs = []*Tab{tabB}
	b.Mu.Lock()
	b.TakeUpHeld()
	b.Start(tabB, true)
	b.Mu.Unlock()
	run := tabB.Review
	if run == nil || run.Phase != phase || run.Round != round || run.Focus != "the parser" || len(run.Seats) != 2 {
		t.Fatalf("review: %+v", run)
	}
	s := run.Seats[0]
	if s.R.Pane == nil || s.R.Pane.Pid() != pid || s.R.ReviewOf != tabB || s.Res == nil || s.Res.Verdict != "approve" {
		t.Fatalf("reviewer: %+v", s)
	}
	if o := run.Seats[1]; o.R.Pane == nil || o.Res != nil || o.R.Agent != agents.OpenCode {
		t.Fatalf("second reviewer: %+v", o)
	}
	if p := s.R.PaneOf(); p == nil || p.Kind != PaneReview {
		t.Fatalf("reviewer's pane: %+v", p)
	}
	if tabB.Tab.Review != nil {
		t.Error("the kept review left on the tab")
	}
	if b.HeldLeft() != 0 {
		t.Errorf("%d programs left", b.HeldLeft())
	}
}

// Without a holder the session keeps no review, as before.
func TestNoReviewSavedWithoutAHolder(t *testing.T) {
	c := &Core{Client: NoClient{}, Root: t.TempDir()}
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "x", Name: "x"}}}
	tb.Review = &ReviewRun{Id: "r", Round: 1}
	c.Tabs = []*Tab{tb}
	c.SaveSession()
	sess, err := state.Load(c.Root)
	if err != nil || len(sess.Tabs) != 1 || sess.Tabs[0].Review != nil {
		t.Fatalf("session: %+v %v", sess, err)
	}
}

// An agent beside a tab's own, kept by the holder, is the tab's to take
// up, not a program left over; and so is one of a tab that would sleep.
func TestHeldAgentsBesideATab(t *testing.T) {
	h := holder.New()
	clA, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	script := agentScript(t, "exec cat\n")
	a, tabA := heldCore(t, clA, script)
	extra := &Tab{AgentState: Agent{Tab: state.Tab{ID: "extra1", Name: "a", Dir: tabA.Dir, Agent: agents.Codex}}, PartOf: tabA}
	tabA.PaneList = append(tabA.Panes(), &PaneTab{Kind: PaneAgent, A: extra})
	a.Start(extra, false)
	if extra.Pane == nil {
		t.Fatalf("not started: %s", extra.Err)
	}
	clA.Close()

	clB, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clB.Shutdown() })
	b, tabB := heldCore(t, clB, script)
	extraB := &Tab{AgentState: Agent{Tab: extra.Tab}, PartOf: tabB}
	tabB.PaneList = append(tabB.Panes(), &PaneTab{Kind: PaneAgent, A: extraB})
	if n := b.HeldLeft(); n != 0 {
		t.Errorf("%d programs left over", n)
	}
	if !b.HeldAgent(tabB) {
		t.Error("the tab could sleep with its agent running unseen")
	}
	b.Start(extraB, true)
	if extraB.Pane == nil || extraB.Pane.Pid() != extra.Pane.Pid() {
		t.Fatalf("not taken up: %+v", extraB.Pane)
	}
	if b.HeldAgent(tabB) {
		t.Error("still waiting once taken up")
	}
}

// An agent started in a sandbox stays in one for a core started after a
// crash, the config since turned the other way: the holder's mark is
// what counts, not the config. (A program the holder has no mark of
// counts as in one: see the holder's TestSpawnKeepsTheSpecsIDAndMark.)
func TestTheSandboxMarkOutlivesACrash(t *testing.T) {
	h := holder.New()
	clA, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	script := agentScript(t, "exec cat\n")
	a, tabA := heldCore(t, clA, script)
	a.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{script}, Sandboxed: true}
	a.Start(tabA, false)
	if tabA.Pane == nil || !tabA.InSandbox {
		t.Fatalf("not started in a sandbox: %s", tabA.Err)
	}
	pid := tabA.Pane.Pid()
	clA.Close() // crashed

	clB, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clB.Shutdown() })
	b, tabB := heldCore(t, clB, script) // sandboxed = false now
	b.Mu.Lock()
	b.TakeUpHeld()
	cmd := b.Start(tabB, true)
	b.Mu.Unlock()
	if tabB.Pane == nil || tabB.Pane.Pid() != pid {
		t.Fatalf("agent not taken up: %+v", tabB.Pane)
	}
	if !tabB.InSandbox || !b.Confined(hooks.Report{}, tabB) {
		t.Error("the agent lost its sandbox with the crash")
	}
	if cmd != nil {
		t.Error("an agent taken up is checked again") // it was, when it started
	}

}
