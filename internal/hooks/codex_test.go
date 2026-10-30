package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodexStateFollowsTheTurn covers what each of codex's events means,
// as a real codex 0.163 sends them: a turn is work from the prompt until
// Stop, or Interrupt when it is cut short, and a permission waits for the
// user. A subagent's events carry its agent_id.
func TestCodexStateFollowsTheTurn(t *testing.T) {
	for _, c := range []struct {
		ev   CodexEvent
		want string
	}{
		{CodexEvent{Event: "SessionStart", Source: "startup"}, "idle"},
		{CodexEvent{Event: "SessionStart", Source: "resume"}, "idle"},
		{CodexEvent{Event: "SessionStart", Source: "clear"}, "idle"},
		{CodexEvent{Event: "SessionStart", Source: "compact"}, ""},
		{CodexEvent{Event: "UserPromptSubmit"}, "working"},
		{CodexEvent{Event: "PreToolUse"}, "working"},
		{CodexEvent{Event: "PostToolUse"}, "working"},
		{CodexEvent{Event: "PermissionRequest"}, "blocked"},
		{CodexEvent{Event: "Stop"}, "idle"},
		// Esc, at work or at a permission: no Stop comes.
		{CodexEvent{Event: "Interrupt"}, "idle"},
		{CodexEvent{Event: "SessionEnd"}, ""},
		// A subagent works and ends inside the agent's turn.
		{CodexEvent{Event: "SubagentStart", AgentID: "a1"}, ""},
		{CodexEvent{Event: "PreToolUse", AgentID: "a1"}, ""},
		{CodexEvent{Event: "PostToolUse", AgentID: "a1"}, ""},
		{CodexEvent{Event: "SubagentStop", AgentID: "a1"}, ""},
		// But its question waits for the user all the same.
		{CodexEvent{Event: "PermissionRequest", AgentID: "a1"}, "blocked"},
	} {
		if got := CodexState(c.ev); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

// TestCodexReportsItsSessionNotASubagents covers which conversation the
// reports say the agent is in: its own, never with a subagent's report,
// though codex gives a subagent's events the agent's session_id.
func TestCodexReportsItsSessionNotASubagents(t *testing.T) {
	method, p := codexReport(CodexEvent{Event: "SessionStart", Source: "startup", SessionID: "s1"}, "tab1")
	if method != ReportAgent || p["state"] != "idle" || p["agent_session_id"] != "s1" ||
		p["session_start_source"] != "startup" || p["source"] != CodexSource || p["agent"] != "codex" {
		t.Fatalf("%s %v", method, p)
	}
	method, p = codexReport(CodexEvent{Event: "SessionStart", Source: "compact", SessionID: "s1"}, "tab1")
	if method != ReportAgentSession || p["state"] != nil || p["agent_session_id"] != "s1" {
		t.Fatalf("compaction: %s %v", method, p)
	}
	if method, p := codexReport(CodexEvent{Event: "SubagentStop", SessionID: "s1", AgentID: "a1"}, "tab1"); method != "" {
		t.Fatalf("a subagent's end reported: %s %v", method, p)
	}
	method, p = codexReport(CodexEvent{Event: "PermissionRequest", SessionID: "s1", AgentID: "a1"}, "tab1")
	if method != ReportAgent || p["state"] != "blocked" || p["agent_session_id"] != nil {
		t.Fatalf("%s %v", method, p)
	}
}

// TestCodexCommandReportsToTheTab covers `nerdr hook codex` end to end:
// codex's JSON on stdin reaches nerdr as the tab's report, and outside
// nerdr nothing is sent.
func TestCodexCommandReportsToTheTab(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	var stderr bytes.Buffer
	in := `{"session_id":"01a1","turn_id":"01a2","hook_event_name":"Stop","permission_mode":"bypassPermissions","stop_hook_active":false,"last_assistant_message":"done"}`
	if code := CodexCommand(strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	r := next(t, got)
	if r.Method != ReportAgent || r.Pane != "tab1" || r.Source != CodexSource || r.Agent != "codex" ||
		r.State != "idle" || r.SessionID != "01a1" || r.Seq <= 0 {
		t.Fatalf("report = %+v", r)
	}
	if code := CodexCommand(strings.NewReader("not json"), &stderr); code == 0 {
		t.Fatal("no event, but exit 0")
	}
	t.Setenv("NERDR_SOCKET", "")
	if code := CodexCommand(strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("outside nerdr: exit %d", code)
	}
	select {
	case r := <-got:
		t.Fatalf("reported outside nerdr: %+v", r)
	default:
	}
}

// TestCodexArgsHookEveryEventNerdrReads covers the -c arguments: one per
// event CodexState reads, each running `nerdr hook codex` quietly.
func TestCodexArgsHookEveryEventNerdrReads(t *testing.T) {
	args := CodexArgs()
	if len(args) != 2*len(codexEvents) {
		t.Fatalf("args: %q", args)
	}
	joined := strings.Join(args, "\n")
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "Interrupt"} {
		if !strings.Contains(joined, "hooks."+ev+"=[{hooks=[{type=\"command\",command='"+codexHookCommand+"'") {
			t.Errorf("no hook for %s in %q", ev, joined)
		}
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-c" {
			t.Errorf("args[%d] = %q", i, args[i])
		}
	}
	// codex clamps a longer Interrupt timeout, with a warning at start.
	if !strings.Contains(joined, "hooks.Interrupt=[{hooks=[{type=\"command\",command='"+codexHookCommand+"',timeout=3}]}]") {
		t.Errorf("Interrupt timeout not 3 s in %q", joined)
	}
}

// fakeCodex is a codex whose app server lists hooks and records what it
// is asked to write, in the shape codex 0.163 answers.
func fakeCodex(t *testing.T, hooks []codexHook) (argv []string, writes, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	writes, argsLog = filepath.Join(dir, "writes"), filepath.Join(dir, "args")
	list, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"cwd": "/", "hooks": hooks}}})
	script := `#!/bin/sh
printf '%s\n' "$@" > '` + argsLog + `'
while IFS= read -r line; do
  case "$line" in
  *'"method":"initialize"'*) echo '{"id":1,"result":{}}' ;;
  *'"method":"hooks/list"'*) echo '{"method":"note","params":{}}'; printf '{"id":2,"result":%s}\n' '` + string(list) + `' ;;
  *'"method":"config/batchWrite"'*) printf '%s\n' "$line" >> '` + writes + `'; echo '{"id":3,"result":{"status":"ok"}}' ;;
  esac
done
`
	file := filepath.Join(dir, "codex")
	if err := os.WriteFile(file, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{file}, writes, argsLog
}

func nerdrsCodexHooks(trust string) []codexHook {
	var hs []codexHook
	for _, ev := range codexEvents {
		hs = append(hs, codexHook{Key: "/<session-flags>/config.toml:" + ev + ":0:0", Command: codexHookCommand,
			Source: "sessionFlags", Enabled: true, Hash: "sha256:" + ev, Trust: trust})
	}
	return hs
}

// TestTrustCodexHooks covers asking codex about nerdr's hooks: trusted
// ones are used as they are, untrusted ones are trusted only when nerdr
// may write, and only nerdr's own, through codex's own config writer.
func TestTrustCodexHooks(t *testing.T) {
	argv, writes, argsLog := fakeCodex(t, nerdrsCodexHooks("trusted"))
	if trusted, wrote, err := TrustCodexHooks(argv, os.Environ(), true); !trusted || wrote || err != nil {
		t.Fatalf("all trusted: %v %v %v", trusted, wrote, err)
	}
	if b, _ := os.ReadFile(argsLog); !strings.HasPrefix(string(b), "app-server\n-c\nhooks.SessionStart=") {
		t.Errorf("codex was run with %q", b)
	}
	if _, err := os.Stat(writes); err == nil {
		t.Error("wrote with all trusted")
	}

	hs := nerdrsCodexHooks("untrusted")
	hs[1].Trust = "trusted"
	hs[2].Enabled = false // the user turned it off
	hs = append(hs, codexHook{Key: "/home/u/.codex/hooks.json:stop:0:0", Command: "orca", Source: "user", Enabled: true, Trust: "untrusted"})
	argv, writes, _ = fakeCodex(t, hs)
	if trusted, wrote, err := TrustCodexHooks(argv, os.Environ(), false); trusted || wrote || err != nil {
		t.Fatalf("untrusted, may not write: %v %v %v", trusted, wrote, err)
	}
	if _, err := os.Stat(writes); err == nil {
		t.Fatal("wrote when it may not")
	}
	if trusted, wrote, err := TrustCodexHooks(argv, os.Environ(), true); !trusted || !wrote || err != nil {
		t.Fatalf("untrusted, may write: %v %v %v", trusted, wrote, err)
	}
	b, _ := os.ReadFile(writes)
	var req struct {
		Params struct {
			Edits []map[string]any `json:"edits"`
		} `json:"params"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if len(req.Params.Edits) != len(codexEvents)-2 {
		t.Fatalf("edits: %v", req.Params.Edits)
	}
	e := req.Params.Edits[0]
	if e["keyPath"] != `hooks.state."/<session-flags>/config.toml:SessionStart:0:0".trusted_hash` || e["value"] != "sha256:SessionStart" || e["mergeStrategy"] != "upsert" {
		t.Errorf("edit: %v", e)
	}
	for _, e := range req.Params.Edits {
		if strings.Contains(e["keyPath"].(string), "hooks.json") || strings.Contains(e["keyPath"].(string), ":"+codexEvents[1]+":") || strings.Contains(e["keyPath"].(string), ":"+codexEvents[2]+":") {
			t.Errorf("trusted what is not nerdr's to trust: %v", e)
		}
	}

	argv, _, _ = fakeCodex(t, nerdrsCodexHooks("trusted")[:3])
	if trusted, _, err := TrustCodexHooks(argv, os.Environ(), true); trusted || err == nil {
		t.Fatalf("a codex that took only some hooks: %v %v", trusted, err)
	}
	if trusted, _, err := TrustCodexHooks([]string{filepath.Join(t.TempDir(), "none")}, os.Environ(), true); trusted || err == nil {
		t.Fatalf("no codex: %v %v", trusted, err)
	}
}

// CodexDeveloperInstructions asks codex's app server, in the folder codex
// starts in and with its -c options, for the instructions it would have.
func TestCodexDeveloperInstructions(t *testing.T) {
	dir := t.TempDir()
	argsLog, reqLog := filepath.Join(dir, "args"), filepath.Join(dir, "req")
	answer := filepath.Join(dir, "answer")
	script := `#!/bin/sh
printf '%s\n' "$PWD" "$CODEX_HOME" "$@" > '` + argsLog + `'
while IFS= read -r line; do
  case "$line" in
  *'"method":"initialize"'*) echo '{"id":1,"result":{}}' ;;
  *'"method":"config/read"'*) printf '%s\n' "$line" > '` + reqLog + `'; echo '{"method":"note","params":{}}'; cat '` + answer + `' ;;
  esac
done
`
	file := filepath.Join(dir, "codex")
	if err := os.WriteFile(file, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Resolved, as $PWD is: macOS keeps its temp folders behind a link.
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ answer, want string }{
		{`{"id":2,"result":{"config":{"developer_instructions":"Be \"brief\".\nAlways."}}}`, "Be \"brief\".\nAlways."},
		{`{"id":2,"result":{"config":{"developer_instructions":null}}}`, ""},
	} {
		if err := os.WriteFile(answer, []byte(c.answer+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := CodexDeveloperInstructions([]string{file}, []string{"CODEX_HOME=/codex"}, []string{`model="o3"`}, work)
		if err != nil || got != c.want {
			t.Fatalf("%q, %v; want %q", got, err, c.want)
		}
	}
	if b, _ := os.ReadFile(argsLog); string(b) != work+"\n/codex\napp-server\n-c\nmodel=\"o3\"\n" {
		t.Fatalf("started as %q", b)
	}
	if b, _ := os.ReadFile(reqLog); !strings.Contains(string(b), `"cwd":"`+work+`"`) {
		t.Fatalf("asked %q", b)
	}
	if err := os.WriteFile(answer, []byte(`{"id":2,"error":{"message":"bad config"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexDeveloperInstructions([]string{file}, nil, nil, work); err == nil {
		t.Fatal("an error should say so")
	}
}
