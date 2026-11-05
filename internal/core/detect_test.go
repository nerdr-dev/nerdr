package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// Startup titles can animate even though no task has been submitted.
// Exercise the real screen rules, including an idle hook which the screen
// overrides after StaleIdle, without mistaking that override for a work hook.
func TestStartupSpinnerIsNotUnseenWork(t *testing.T) {
	for _, kind := range []agents.Kind{agents.Codex, agents.Claude} {
		for _, hooked := range []bool{false, true} {
			t.Run(string(kind)+map[bool]string{false: "/screen", true: "/idle-hook"}[hooked], func(t *testing.T) {
				a := silent(t)
				a.Agent = kind
				a.Light = StStarting
				now := time.Now()
				if hooked {
					a.Hook = HookAuthority{Source: "test", State: detect.Idle, At: now.Add(-StaleIdle)}
				}
				a.Pane.Focus(false) // focus reports are not user input
				a.Pane.Feed([]byte("\x1b]0;⠙ Agent\x07"))
				a.Classify(false, now)
				if a.Light != StWorking || a.Worked || a.Tasked {
					t.Fatalf("startup: light=%s worked=%v tasked=%v", a.Light.Label(), a.Worked, a.Tasked)
				}
				// A second tick must not turn a screen-derived Hook.State into a task.
				a.Classify(false, now.Add(time.Second))
				if a.Worked {
					t.Fatal("startup counted as work on the next tick")
				}
				a.Pane.Feed([]byte("\x1b]0;✳ Agent\x07"))
				if hooked {
					a.Hook.State = detect.Idle
				}
				a.Classify(false, now.Add(2*time.Second))
				a.Classify(false, now.Add(2*time.Second+IdleAnyway))
				if a.Light != StIdle || a.Worked {
					t.Fatalf("after startup: light=%s worked=%v", a.Light.Label(), a.Worked)
				}
			})
		}
	}
}

func TestTaskFinishesUnseen(t *testing.T) {
	for _, source := range []string{"typed", "queue", "hook"} {
		t.Run(source, func(t *testing.T) {
			a := silent(t)
			a.Agent = agents.Codex
			now := time.Now()
			switch source {
			case "typed":
				a.Pane.Input([]byte("do the task\r"))
			case "queue":
				a.AwaitWork = true
			case "hook":
				tb := &Tab{AgentState: *a}
				c := &Core{Client: NoClient{}, Tabs: []*Tab{tb}}
				c.OnHook(hooks.Report{Method: hooks.ReportAgent, Pane: tb.HookKey(), Source: "test", State: "working"})
				a = &tb.AgentState
			}
			for turn := 0; turn < 2; turn++ {
				a.Pane.Feed([]byte("\x1b]0;⠙ Agent\x07"))
				if source == "hook" {
					a.Hook.State = detect.Working
				}
				a.Classify(false, now)
				if a.Light != StWorking || !a.Worked || !a.Begun || a.AwaitWork {
					t.Fatalf("task not taken up: %+v", a)
				}
				a.Pane.Feed([]byte("\x1b]0;✳ Agent\x07"))
				if source == "hook" {
					a.Hook.State = detect.Idle
				}
				a.Classify(false, now.Add(time.Second))
				a.Classify(false, now.Add(time.Second+IdleAnyway))
				if a.Light != StDone {
					t.Fatalf("finished unseen: %s", a.Light.Label())
				}
				a.Seen()
				if a.Worked || a.Light != StIdle {
					t.Fatal("Seen did not clear completion")
				}
				now = now.Add(time.Minute)
			}
		})
	}
}

func TestStartupPromptCountsAsTaskAndResetsOnRestart(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\nsleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "first", Name: "first", Dir: dir, Agent: agents.Codex, Messages: []state.Message{{Text: "do the first task", Auto: true}}}}}
	c := &Core{Client: NoClient{}, Tabs: []*Tab{tb}, Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{script}}}}}
	c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatal(tb.Err)
	}
	t.Cleanup(func() { tb.Pane.Close() })
	waitFor(t, "prompt passed as argument", func() bool {
		b, _ := os.ReadFile(script + ".args")
		return strings.Contains(string(b), "do the first task")
	})
	if !tb.Tasked || tb.Pane.Inputs() != 0 || len(tb.Messages) != 0 {
		t.Fatal("startup prompt was not recorded as a task")
	}
	now := time.Now()
	tb.Pane.Feed([]byte("\x1b]0;⠙ Agent\x07"))
	tb.Classify(false, now)
	tb.Pane.Feed([]byte("\x1b]0;✳ Agent\x07"))
	tb.Classify(false, now.Add(time.Second))
	tb.Classify(false, now.Add(time.Second+IdleAnyway))
	if tb.Light != StDone || !tb.Worked {
		t.Fatal("startup task did not finish unseen")
	}
	tb.Pane.Close()
	c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatal(tb.Err)
	}
	if tb.Tasked || tb.Worked {
		t.Fatal("new process inherited task/completion state")
	}
}
