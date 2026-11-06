package core

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/holder"
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
