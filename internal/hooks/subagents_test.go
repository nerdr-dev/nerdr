package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Separate processes matter: a Go mutex cannot protect hook commands.
func TestSubagentHooksConcurrentProcesses(t *testing.T) {
	if file := os.Getenv("NERDR_TEST_SUBAGENT_FILE"); file != "" {
		for i := 0; i < 8; i++ {
			id := os.Getenv("NERDR_TEST_SUBAGENT_ID") + "-" + strconv.Itoa(i)
			w := seeSubagents(file, os.Getenv("NERDR_TEST_SUBAGENT_EVENT"), id, false)
			if w.Ends {
				fmt.Println("LAST_SUBAGENT_STOPPED")
			}
		}
		return
	}
	file := filepath.Join(t.TempDir(), "subagents.json")
	const processes = 16
	run := func(event string) int {
		t.Helper()
		type result struct {
			output []byte
			err    error
		}
		results := make(chan result, processes)
		for i := 0; i < processes; i++ {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSubagentHooksConcurrentProcesses$")
			cmd.Env = append(os.Environ(), "NERDR_TEST_SUBAGENT_FILE="+file,
				"NERDR_TEST_SUBAGENT_EVENT="+event, "NERDR_TEST_SUBAGENT_ID="+strconv.Itoa(i))
			go func() { b, err := cmd.CombinedOutput(); results <- result{b, err} }()
		}
		ends := 0
		for i := 0; i < processes; i++ {
			r := <-results
			if r.err != nil {
				t.Errorf("hook: %v\n%s", r.err, r.output)
			}
			ends += strings.Count(string(r.output), "LAST_SUBAGENT_STOPPED")
		}
		return ends
	}
	if ends := run("SubagentStart"); ends != 0 {
		t.Fatalf("starts ended waiting: %d", ends)
	}
	if w := seeSubagents(file, "Stop", "", false); w.Going != processes*8 {
		t.Fatalf("lost concurrent starts: got %d, want %d", w.Going, processes*8)
	}
	if ends := run("SubagentStop"); ends != 1 {
		t.Fatalf("last stop reported %d times", ends)
	}
	if w := seeSubagents(file, "", "", false); w.Going != 0 {
		t.Fatalf("lost concurrent stops: %d remain", w.Going)
	}
}

// TestAgyHooksConcurrentProcesses: the PreToolUse hooks of subagents
// started in one step may run side by side, and each must count, or a
// subagent's conversation is taken for the agent's.
func TestAgyHooksConcurrentProcesses(t *testing.T) {
	if file := os.Getenv("NERDR_TEST_AGY_FILE"); file != "" {
		ev := AgyEvent{Event: "PreToolUse", ConversationID: "main"}
		ev.ToolCall.Name = agySubagentTool
		for range 8 {
			seeAgy(file, ev)
		}
		return
	}
	file := filepath.Join(t.TempDir(), "turn.json")
	if !seeAgy(file, AgyEvent{Event: "PreInvocation", ConversationID: "main"}) {
		t.Fatal("the agent's own event")
	}
	const processes = 16
	errs := make(chan error, processes)
	for range processes {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAgyHooksConcurrentProcesses$")
		cmd.Env = append(os.Environ(), "NERDR_TEST_AGY_FILE="+file)
		go func() {
			if b, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("%v\n%s", err, b)
				return
			}
			errs <- nil
		}()
	}
	for range processes {
		if err := <-errs; err != nil {
			t.Errorf("hook: %v", err)
		}
	}
	for i := range processes * 8 {
		if seeAgy(file, AgyEvent{Event: "SessionStart", ConversationID: "sub" + strconv.Itoa(i)}) {
			t.Fatalf("subagent %d of %d taken for the agent: a start was lost", i+1, processes*8)
		}
	}
	if !seeAgy(file, AgyEvent{Event: "SessionStart", ConversationID: "next"}) {
		t.Fatal("the next conversation is the agent's")
	}
}
