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

// TestClaudeWaitsForBackgroundCommands covers a Bash command or a monitor
// run in the background: no hook says it starts or ends, but the Stop of
// each turn lists it while it goes on, as claude 2.1.316 gives
// background_tasks. Its news starts a turn whose Stop no longer lists it.
func TestClaudeWaitsForBackgroundCommands(t *testing.T) {
	file := filepath.Join(t.TempDir(), "subagents.json")
	report := func(ev ClaudeEvent) (string, bool) {
		_, p := claudeReport(ev, "tab1", seeSubagents(file, ev.Event, ev.AgentID, false))
		state, _ := p["state"].(string)
		return state, p["background"] == true
	}
	shell := ClaudeTask{ID: "b1", Type: "shell"}
	monitor := ClaudeTask{ID: "m1", Type: "monitor"}
	watch := ClaudeTask{ID: "s1", Type: "monitor", Description: "live updates for artifact b478d532 (comments)"}
	for i, c := range []struct {
		ev         ClaudeEvent
		want       string
		background bool
	}{
		{ClaudeEvent{Event: "UserPromptSubmit"}, "working", false},
		{ClaudeEvent{Event: "PreToolUse", Tool: "Bash"}, "working", false},
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{shell}}, "working", true},
		// The command's news, in a turn of its own; a monitor goes on.
		{ClaudeEvent{Event: "UserPromptSubmit"}, "working", false},
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{monitor}}, "working", true},
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{}}, "idle", false},
		// The watch on an Artifact the agent published waits on comments,
		// as a cron does: the turn is over. A monitor of the agent's own
		// is still work going on.
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{watch}}, "idle", false},
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{watch, monitor}}, "working", true},
		// A subagent listed is left to the count of SubagentStart and
		// SubagentStop, which has none going.
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{{ID: "a9", Type: "subagent"}}}, "idle", false},
		// A subagent counted and a command listed: either keeps it at work.
		{ClaudeEvent{Event: "SubagentStart", AgentID: "a1"}, "", false},
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{shell, {ID: "a1", Type: "subagent"}}}, "working", true},
		{ClaudeEvent{Event: "SubagentStop", AgentID: "a1"}, "working", false},
		{ClaudeEvent{Event: "Stop", BackgroundTasks: []ClaudeTask{shell}}, "working", true},
		{ClaudeEvent{Event: "Stop"}, "idle", false},
		// A subagent's own Stop says nothing of the agent's wait.
		{ClaudeEvent{Event: "Stop", AgentID: "a2", BackgroundTasks: []ClaudeTask{shell}}, "", false},
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

// TestClaudeCommandLeavesAnArtifactWatchOut covers the Stop of an agent
// that published an Artifact: claude keeps a watch on it for comments, a
// monitor that does not end, which leaves the agent waiting on the user.
func TestClaudeCommandLeavesAnArtifactWatchOut(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	t.Setenv("TMPDIR", t.TempDir()) // after the socket: its path must stay short
	var stderr bytes.Buffer
	in := `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false,` +
		`"background_tasks":[{"id":"sq1x2","type":"monitor","status":"running","description":"live updates for artifact Senterkartet (comments and edits)"}],` +
		`"session_crons":[]}`
	if code := ClaudeCommand(strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if r := next(t, got); r.State != "idle" || r.Background || r.Processes != 0 {
		t.Fatalf("stop: %+v", r)
	}
}

// TestClaudeCommandReportsABackgroundCommand covers `nerdr hook claude`
// reading a Stop as claude gives it with a command in the background.
func TestClaudeCommandReportsABackgroundCommand(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	t.Setenv("TMPDIR", t.TempDir()) // after the socket: its path must stay short
	var stderr bytes.Buffer
	in := `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false,` +
		`"background_tasks":[{"id":"b1","type":"shell","status":"running","description":"measure","command":"./measure.sh"}],` +
		`"session_crons":[]}`
	if code := ClaudeCommand(strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if r := next(t, got); r.State != "working" || !r.Background {
		t.Fatalf("stop: %+v", r)
	}
}

// TestClaudeReportsOnlyProcessesAsProcesses covers what the light of its own
// goes by: commands and monitors alone. Anything else going is at work.
func TestClaudeReportsOnlyProcessesAsProcesses(t *testing.T) {
	shell := ClaudeTask{ID: "b1", Type: "shell"}
	monitor := ClaudeTask{ID: "m1", Type: "monitor"}
	watch := ClaudeTask{ID: "s1", Type: "monitor", Description: "live updates for artifact b478d532 (comments)"}
	for i, c := range []struct {
		tasks []ClaudeTask
		w     SubagentWait
		want  any
	}{
		{[]ClaudeTask{shell}, SubagentWait{}, 1},
		{[]ClaudeTask{shell, monitor}, SubagentWait{}, 2},
		{[]ClaudeTask{shell, watch}, SubagentWait{}, 1},
		{[]ClaudeTask{watch}, SubagentWait{}, nil},
		{[]ClaudeTask{shell, {ID: "a1", Type: "subagent"}}, SubagentWait{}, 1}, // the count has it stopped
		{[]ClaudeTask{shell}, SubagentWait{Going: 1}, nil},
		{[]ClaudeTask{shell, {ID: "w1", Type: "workflow"}}, SubagentWait{}, nil},
		{nil, SubagentWait{Going: 1}, nil},
		{nil, SubagentWait{}, nil},
	} {
		_, p := claudeReport(ClaudeEvent{Event: "Stop", BackgroundTasks: c.tasks}, "tab1", c.w)
		if got := p["background_processes"]; got != c.want {
			t.Errorf("%d: %v processes, want %v", i, got, c.want)
		}
	}
}
