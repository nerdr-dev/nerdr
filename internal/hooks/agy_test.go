package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgyStateFollowsTheTurn covers what each of agy's events means for
// the agent's own conversation, as a real agy 1.3.6 sends them: work from
// the model's call until Stop, and still work after a Stop while its
// subagents are at it.
func TestAgyStateFollowsTheTurn(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		ev   AgyEvent
		want string
	}{
		{AgyEvent{Event: "SessionStart"}, ""},
		{AgyEvent{Event: "PreInvocation"}, "working"},
		{AgyEvent{Event: "PostToolUse"}, "working"},
		{AgyEvent{Event: "PostInvocation"}, ""},
		{AgyEvent{Event: "Stop", FullyIdle: &yes}, "idle"},
		{AgyEvent{Event: "Stop"}, "idle"},
		{AgyEvent{Event: "Stop", FullyIdle: &no}, "working"},
	} {
		if got := AgyState(c.ev); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

// TestAgyTellsTheAgentsConversationFromASubagents runs the hooks a real
// agy sent for a turn with a subagent, in their order: the subagent's
// conversation, its Stop with it, is not the agent's. A new conversation
// started between turns (/clear) is.
func TestAgyTellsTheAgentsConversationFromASubagents(t *testing.T) {
	yes, no := true, false
	invoke := func(event string, n int) AgyEvent {
		ev := AgyEvent{Event: event, ConversationID: "main"}
		ev.ToolCall.Name = "invoke_subagent"
		ev.ToolCall.Args.Subagents = make([]json.RawMessage, n)
		return ev
	}
	var turn agyTurn
	for i, c := range []struct {
		ev  AgyEvent
		own bool
	}{
		{AgyEvent{Event: "SessionStart", ConversationID: "main"}, true},
		{AgyEvent{Event: "PreInvocation", ConversationID: "main"}, true},
		{invoke("PreToolUse", 2), true},
		{invoke("PostToolUse", 2), true},
		{AgyEvent{Event: "SessionStart", ConversationID: "a"}, false},
		{AgyEvent{Event: "SessionStart", ConversationID: "b"}, false},
		{AgyEvent{Event: "PreInvocation", ConversationID: "a"}, false},
		{AgyEvent{Event: "PreInvocation", ConversationID: "main"}, true},
		{AgyEvent{Event: "PostToolUse", ConversationID: "b"}, false},
		{AgyEvent{Event: "Stop", ConversationID: "main", FullyIdle: &no}, true},
		{AgyEvent{Event: "Stop", ConversationID: "b", FullyIdle: &yes}, false},
		{AgyEvent{Event: "Stop", ConversationID: "a", FullyIdle: &yes}, false},
		{AgyEvent{Event: "PreInvocation", ConversationID: "main"}, true},
		// Esc: no Stop. Then /clear.
		{AgyEvent{Event: "SessionStart", ConversationID: "next"}, true},
		{AgyEvent{Event: "PreInvocation", ConversationID: "next"}, true},
		{AgyEvent{Event: "PreInvocation", ConversationID: "main"}, false},
	} {
		if got := turn.see(c.ev); got != c.own {
			t.Fatalf("%d %+v: own %v, want %v (%+v)", i, c.ev, got, c.own, turn)
		}
	}

	// Subagents that failed to start leave the next conversation the
	// agent's.
	turn = agyTurn{Main: "main"}
	turn.see(invoke("PreToolUse", 1))
	failed := invoke("PostToolUse", 1)
	failed.Error = "no such agent"
	turn.see(failed)
	if !turn.see(AgyEvent{Event: "SessionStart", ConversationID: "next"}) || turn.Main != "next" {
		t.Fatalf("%+v", turn)
	}

	// A resumed conversation has no SessionStart: the first seen is the
	// agent's.
	turn = agyTurn{}
	if !turn.see(AgyEvent{Event: "PreInvocation", ConversationID: "resumed"}) || turn.Main != "resumed" {
		t.Fatalf("%+v", turn)
	}

	// Kept between hooks, each a process of its own.
	file := filepath.Join(t.TempDir(), "turn.json")
	if !seeAgy(file, AgyEvent{Event: "PreInvocation", ConversationID: "main"}) || !seeAgy(file, invoke("PreToolUse", 1)) ||
		seeAgy(file, AgyEvent{Event: "SessionStart", ConversationID: "sub"}) {
		t.Fatal("the turn was not kept")
	}
}

// TestAgyCommandReportsOnlyForNerdrsAgy covers `nerdr hook agy` end to
// end: an event of the agy nerdr started reaches nerdr as the tab's
// report; one of an agy that agy runs, or of a subagent, or outside nerdr,
// does not.
func TestAgyCommandReportsOnlyForNerdrsAgy(t *testing.T) {
	s, got := listen(t)
	// After the socket, whose path would be too long in it on macOS.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	t.Setenv("NERDR_AGY_HOOK", "100")
	parents := map[int]int{200: 100, 300: 250}
	defer func(f func(int) int) { parentOf = f }(parentOf)
	parentOf = func(pid int) int { return parents[pid] }
	var stderr bytes.Buffer
	run := func(event, pid, in string) {
		t.Helper()
		if code := AgyCommand([]string{event, pid}, strings.NewReader(in), &stderr); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
	}
	none := func(what string) {
		t.Helper()
		select {
		case r := <-got:
			t.Fatalf("%s reported: %+v", what, r)
		default:
		}
	}

	run("SessionStart", "200", `{"conversationId":"c1","workspacePaths":["/w"],"modelName":"gemini"}`)
	if r := next(t, got); r.Method != ReportAgentSession || r.SessionID != "c1" || r.Source != AgySource || r.Agent != "agy" || r.Pane != "tab1" {
		t.Fatalf("report = %+v", r)
	}
	run("PreInvocation", "200", `{"conversationId":"c1","invocationNum":0}`)
	if r := next(t, got); r.Method != ReportAgent || r.State != "working" || r.SessionID != "c1" || r.Seq <= 0 {
		t.Fatalf("report = %+v", r)
	}
	run("PreToolUse", "200", `{"conversationId":"c1","toolCall":{"name":"invoke_subagent","args":{"Subagents":[{"Prompt":"echo a"}]}}}`)
	none("starting a subagent")
	run("SessionStart", "200", `{"conversationId":"sub"}`)
	run("Stop", "200", `{"conversationId":"sub","fullyIdle":true,"terminationReason":"NO_TOOL_CALL"}`)
	none("a subagent")
	run("Stop", "300", `{"conversationId":"c9","fullyIdle":true}`)
	none("an agy the agent ran")
	run("Stop", "200", `{"conversationId":"c1","fullyIdle":true,"terminationReason":"NO_TOOL_CALL"}`)
	if r := next(t, got); r.State != "idle" || r.SessionID != "c1" {
		t.Fatalf("report = %+v", r)
	}
	t.Setenv("NERDR_AGY_HOOK", "")
	run("Stop", "200", `{"conversationId":"c1"}`)
	none("without nerdr's variable")

	if code := AgyCommand([]string{"Stop", "200"}, strings.NewReader("not json"), &stderr); code == 0 {
		t.Fatal("no event, but exit 0")
	}
}

// TestAgyHooksGoBesideOthers covers putting nerdr's hooks in agy's shared
// hooks file: the other sets kept as they were and in their order, written
// once, a set of nerdr's turned off left off, and a file that is not JSON
// left alone.
func TestAgyHooksGoBesideOthers(t *testing.T) {
	home := t.TempDir()
	file := AgyHooksHome(home)
	if file != filepath.Join(home, ".gemini", "config", "hooks.json") {
		t.Fatalf("home: %s", file)
	}
	if p := AgyHooksProject("/w"); p != filepath.Join("/w", ".agents", "hooks.json") || !strings.HasSuffix(p, filepath.FromSlash(AgyHooksExclude)) {
		t.Fatalf("project: %s", p)
	}
	if wrote, made, err := InstallAgyHooks(file); !wrote || !made || err != nil {
		t.Fatalf("into no file: %v %v %v", wrote, made, err)
	}

	orca := `{
  "orca-status": {
    "Stop": [
      {
        "type": "command",
        "command": "/bin/sh -c 'orca'",
        "timeout": 10
      }
    ]
  },
  "zz-mine": {"enabled": false, "PreInvocation": [{"command": "./mine.sh"}]}
}
`
	_ = os.WriteFile(file, []byte(orca), 0o644)
	if wrote, made, err := InstallAgyHooks(file); !wrote || made || err != nil {
		t.Fatalf("beside orca's: %v %v %v", wrote, made, err)
	}
	b, _ := os.ReadFile(file)
	var got map[string]map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v:\n%s", err, b)
	}
	if !strings.Contains(string(b), `"command": "/bin/sh -c 'orca'"`) || got["zz-mine"]["enabled"] != false || len(got) != 3 {
		t.Fatalf("the others were not kept:\n%s", b)
	}
	if i, j, k := strings.Index(string(b), `"orca-status"`), strings.Index(string(b), `"zz-mine"`), strings.Index(string(b), `"nerdr"`); !(i < j && j < k) {
		t.Errorf("the order changed:\n%s", b)
	}
	for _, ev := range []string{"SessionStart", "PreInvocation", "PreToolUse", "PostToolUse", "Stop"} {
		if _, ok := got["nerdr"][ev]; !ok {
			t.Errorf("no %s hook:\n%s", ev, b)
		}
	}
	if !strings.Contains(string(b), `"matcher": "invoke_subagent"`) || !strings.Contains(string(b), `hook agy Stop \"$PPID\"`) {
		t.Errorf("the hook does not name its event and agy:\n%s", b)
	}
	if wrote, _, _ := InstallAgyHooks(file); wrote {
		t.Fatal("an up-to-date file should not be written")
	}

	off := strings.Replace(string(b), `"nerdr": {`, `"nerdr": {"enabled": false, `, 1)
	_ = os.WriteFile(file, []byte(off), 0o644)
	_, _, _ = InstallAgyHooks(file)
	b, _ = os.ReadFile(file)
	_ = json.Unmarshal(b, &got)
	if got["nerdr"]["enabled"] != false {
		t.Fatalf("nerdr's hooks turned off were turned on:\n%s", b)
	}

	_ = os.WriteFile(file, []byte("{ not json"), 0o644)
	if _, _, err := InstallAgyHooks(file); err == nil {
		t.Fatal("no error for a broken file")
	}
	if b, _ := os.ReadFile(file); string(b) != "{ not json" {
		t.Fatal("a broken file was overwritten")
	}
}
