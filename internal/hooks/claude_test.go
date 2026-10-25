package hooks

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeStateFollowsTheTurn covers what each of claude's hook events
// means: the turn's start and its tools are work, Stop is the end of it,
// and a question or a permission is waiting for the user.
func TestClaudeStateFollowsTheTurn(t *testing.T) {
	for _, c := range []struct {
		ev   ClaudeEvent
		want string
	}{
		{ClaudeEvent{Event: "UserPromptSubmit"}, "working"},
		{ClaudeEvent{Event: "PreToolUse", Tool: "Bash"}, "working"},
		{ClaudeEvent{Event: "PostToolUse", Tool: "Bash"}, "working"},
		{ClaudeEvent{Event: "PreToolUse", Tool: "AskUserQuestion"}, "blocked"},
		{ClaudeEvent{Event: "PreToolUse", Tool: "ExitPlanMode"}, "blocked"},
		{ClaudeEvent{Event: "Notification", Type: "permission_prompt"}, "blocked"},
		{ClaudeEvent{Event: "Notification", Type: "idle_prompt", Message: "Claude is waiting for your input"}, ""},
		{ClaudeEvent{Event: "Notification", Message: "Claude needs your permission to use Bash"}, "blocked"},
		{ClaudeEvent{Event: "Notification", Message: "Claude is waiting for your input"}, ""},
		{ClaudeEvent{Event: "Stop"}, "idle"},
		{ClaudeEvent{Event: "SessionStart", Source: "startup"}, "idle"},
		{ClaudeEvent{Event: "SessionStart", Source: "clear"}, "idle"},
		// A subagent starting or stopping says nothing by itself: claude
		// runs one for a recap after the turn is over.
		{ClaudeEvent{Event: "SubagentStop", AgentID: "a1"}, ""},
		{ClaudeEvent{Event: "SubagentStart", AgentID: "a1"}, ""},
		// A compaction can come mid-turn; the agent works on after it.
		{ClaudeEvent{Event: "SessionStart", Source: "compact"}, ""},
		// A subagent's tools are not the tab's agent at work: it may be
		// waiting on the subagent, or for the user.
		{ClaudeEvent{Event: "PreToolUse", Tool: "Bash", AgentID: "a1"}, ""},
		{ClaudeEvent{Event: "PostToolUse", Tool: "Bash", AgentID: "a1"}, ""},
	} {
		if got := ClaudeState(c.ev, SubagentWait{}); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

// TestClaudeWaitsForBackgroundSubagents covers a subagent started in the
// background, in the order a real claude 2.1.302 runs one: the agent's
// turn ends with a Stop while it goes on — reported as work in the
// background — and once its SubagentStop has come claude prompts the
// agent with the news, which ends with a Stop of its own.
func TestClaudeWaitsForBackgroundSubagents(t *testing.T) {
	file := filepath.Join(t.TempDir(), "subagents.json")
	report := func(ev ClaudeEvent) (string, bool) {
		newSession := ev.Event == "SessionStart" && ev.Source != "compact"
		_, p := claudeReport(ev, "tab1", seeSubagents(file, ev.Event, ev.AgentID, newSession))
		state, _ := p["state"].(string)
		return state, p["background"] == true
	}
	for i, c := range []struct {
		ev         ClaudeEvent
		want       string
		background bool
	}{
		{ClaudeEvent{Event: "SessionStart", Source: "startup"}, "idle", false},
		{ClaudeEvent{Event: "UserPromptSubmit"}, "working", false},
		{ClaudeEvent{Event: "SubagentStart", AgentID: "a1"}, "", false},
		{ClaudeEvent{Event: "Stop"}, "working", true}, // a1 is still at it
		{ClaudeEvent{Event: "PreToolUse", Tool: "Bash", AgentID: "a1"}, "", false},
		// A compaction is the same conversation, a1 still in it.
		{ClaudeEvent{Event: "SessionStart", Source: "compact"}, "", false},
		{ClaudeEvent{Event: "SubagentStop", AgentID: "a1"}, "working", false},
		{ClaudeEvent{Event: "UserPromptSubmit"}, "working", false},
		{ClaudeEvent{Event: "Stop"}, "idle", false},
		// A recap's subagent, after the turn: not news.
		{ClaudeEvent{Event: "SubagentStart", AgentID: "r1"}, "", false},
		{ClaudeEvent{Event: "SubagentStop", AgentID: "r1"}, "", false},
		// Two going: the first done is not the end of the wait.
		{ClaudeEvent{Event: "UserPromptSubmit"}, "working", false},
		{ClaudeEvent{Event: "SubagentStart", AgentID: "a2"}, "", false},
		{ClaudeEvent{Event: "SubagentStart", AgentID: "a3"}, "", false},
		{ClaudeEvent{Event: "Stop"}, "working", true},
		{ClaudeEvent{Event: "SubagentStop", AgentID: "a2"}, "", false},
		{ClaudeEvent{Event: "SubagentStop", AgentID: "a3"}, "working", false},
		// One left going when the conversation changes is not waited for.
		{ClaudeEvent{Event: "SubagentStart", AgentID: "a4"}, "", false},
		{ClaudeEvent{Event: "SessionStart", Source: "clear"}, "idle", false},
		{ClaudeEvent{Event: "Stop"}, "idle", false},
	} {
		if got, bg := report(c.ev); got != c.want || bg != c.background {
			t.Errorf("%d %+v: %q background %v, want %q background %v", i, c.ev, got, bg, c.want, c.background)
		}
	}
}

// TestClaudeCompactionReportsOnlyTheSession covers a session start that
// says nothing about the state: it still tells nerdr the conversation.
func TestClaudeCompactionReportsOnlyTheSession(t *testing.T) {
	method, p := claudeReport(ClaudeEvent{Event: "SessionStart", Source: "compact", SessionID: "s2"}, "tab1", SubagentWait{})
	if method != ReportAgentSession || p["agent_session_id"] != "s2" || p["session_start_source"] != "compact" || p["state"] != nil {
		t.Fatalf("%s %v", method, p)
	}
	method, p = claudeReport(ClaudeEvent{Event: "Stop", SessionID: "s2"}, "tab1", SubagentWait{})
	if method != ReportAgent || p["state"] != "idle" || p["agent_session_id"] != "s2" {
		t.Fatalf("%s %v", method, p)
	}
	if method, _ := claudeReport(ClaudeEvent{Event: "PreToolUse", Tool: "Bash", SessionID: "s2", AgentID: "a1"}, "tab1", SubagentWait{}); method != "" {
		t.Fatalf("a subagent's event reported: %s", method)
	}
}

// TestClaudeSettingsHookEveryEventNerdrReads covers the --settings JSON:
// valid, and a hook on each event ClaudeState reads.
func TestClaudeSettingsHookEveryEventNerdrReads(t *testing.T) {
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct{ Type, Command string }
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(ClaudeSettings(false)), &s); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "SubagentStart", "SubagentStop"} {
		h := s.Hooks[ev]
		if len(h) != 1 || len(h[0].Hooks) != 1 || !strings.Contains(h[0].Hooks[0].Command, `"$NERDR_BIN" hook claude`) {
			t.Errorf("%s: %+v", ev, h)
		}
	}
}

// TestClaudeCommandReportsTheWaitInTheBackground covers `nerdr hook
// claude` end to end: a Stop while a subagent it started is going reaches
// nerdr as work in the background.
func TestClaudeCommandReportsTheWaitInTheBackground(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	t.Setenv("TMPDIR", t.TempDir()) // after the socket: its path must stay short
	var stderr bytes.Buffer
	for _, in := range []string{
		`{"hook_event_name":"SubagentStart","session_id":"s1","agent_id":"a1","agent_type":"general-purpose"}`,
		`{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`,
	} {
		if code := ClaudeCommand(strings.NewReader(in), &stderr); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
	}
	if r := next(t, got); r.State != "working" || !r.Background {
		t.Fatalf("stop: %+v", r)
	}
}
