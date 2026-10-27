package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wantInOrder checks that got has each of want, in that order, and none of
// not.
func wantInOrder(t *testing.T, got string, want, not []string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("lacks %q (in order):\n%s", w, got)
		}
		at += i + len(w)
	}
	for _, n := range not {
		if strings.Contains(got, n) {
			t.Fatalf("has %q:\n%s", n, got)
		}
	}
}

// Claude's log: what was said and the tools run, without tool results,
// thinking, reminders or side conversations.
func TestClaudeTranscript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	writeFile(t, filepath.Join(dir, "projects", "-w-x", "s1.jsonl"), strings.Join([]string{
		`{"type":"user","cwd":"/w/x","message":{"role":"user","content":"fix the parser"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"META"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"THINKING"},{"type":"text","text":"Looking."},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"TOOL OUTPUT"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"SIDECHAIN"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>REMINDER</system-reminder>"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Fixed it."}]}}`,
		`not json`,
	}, "\n"))
	got, err := Transcript(nil, Claude, "s1", "/w/x")
	if err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, got, []string{"## user\n\nfix the parser", "## claude\n\nLooking.", "→ Bash: {\"command\":\"go test ./...\"}", "## claude\n\nFixed it."},
		[]string{"META", "THINKING", "TOOL OUTPUT", "SIDECHAIN", "REMINDER"})

	if _, err := Transcript(nil, Claude, "", "/w/x"); err == nil {
		t.Fatal("no id, no claude conversation to read")
	}

	// Colliding claudeProject names (/a/b vs /a-b -> -a-b) with wrong or
	// missing cwd in the log must not match /a/b.
	writeFile(t, filepath.Join(dir, "projects", "-a-b", "collide.jsonl"),
		`{"type":"user","cwd":"/a-b","message":{"role":"user","content":"wrong repo"}}`)
	writeFile(t, filepath.Join(dir, "projects", "-a-b", "nocwd.jsonl"),
		`{"type":"user","message":{"role":"user","content":"no cwd"}}`)
	for _, id := range []string{"collide", "nocwd"} {
		if got := FirstPromptIn(Claude, id, "/a/b"); got != "" {
			t.Fatalf("colliding project (%s) matched /a/b: %q", id, got)
		}
		if _, err := Transcript(nil, Claude, id, "/a/b"); err == nil {
			t.Fatalf("Transcript accepted colliding project (%s) for /a/b", id)
		}
	}
	if got := FirstPromptIn(Claude, "collide", "/a-b"); got != "wrong repo" {
		t.Fatalf("matching cwd /a-b should succeed: %q", got)
	}
}

// codex's rollout, found by id or as the newest started in the worktree.
func TestCodexTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	wt := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "10", "02")
	writeFile(t, filepath.Join(day, "rollout-2026-10-02T10-00-00-old.jsonl"),
		`{"type":"session_meta","payload":{"cwd":"`+wt+`"}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"OLD"}]}}`)
	writeFile(t, filepath.Join(day, "rollout-2026-10-02T11-00-00-new.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"cwd":"` + wt + `"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"DEVELOPER"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>ENV</environment_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review it"}]}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"rg foo"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","output":"TOOL OUTPUT"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"wait","arguments":"{\"cell\":1}"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Found two bugs."}]}}`,
	}, "\n"))
	writeFile(t, filepath.Join(day, "rollout-2026-10-02T12-00-00-elsewhere.jsonl"), `{"type":"session_meta","payload":{"cwd":"/elsewhere"}}`)

	got, err := Transcript(nil, Codex, "", wt)
	if err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, got, []string{"## user\n\nreview it", "→ exec: rg foo", `→ wait: {"cell":1}`, "## codex\n\nFound two bugs."},
		[]string{"OLD", "DEVELOPER", "ENV", "TOOL OUTPUT"})
	if got, err := Transcript(nil, Codex, "old", wt); err != nil || !strings.Contains(got, "OLD") {
		t.Fatalf("by id: %v\n%s", err, got)
	}
	if _, err := Transcript(nil, Codex, "old", "/anywhere"); err == nil {
		t.Fatal("read a codex conversation from another worktree")
	}
	writeFile(t, filepath.Join(day, "rollout-2026-10-02T13-00-00-nocwd.jsonl"),
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"NO CWD"}]}}`)
	if _, err := Transcript(nil, Codex, "nocwd", wt); err == nil {
		t.Fatal("accepted a codex rollout with missing cwd when dir is set")
	}
	for _, bad := range []string{"*", "../old", "--help"} {
		if ValidSessionID(bad) {
			t.Fatalf("ValidSessionID(%q) = true", bad)
		}
	}
}

// opencode's export, in its current shape and the one before.
func TestOpenCodeExport(t *testing.T) {
	for name, data := range map[string]string{
		"current": `{"info":{},"messages":[
			{"type":"user","text":"add a flag"},
			{"type":"assistant","content":[{"type":"reasoning","text":"REASONING"},{"type":"tool","name":"read","state":{"input":{"path":"main.go"},"content":[{"text":"TOOL OUTPUT"}]}},{"type":"text","text":"Added."}]}]}`,
		"older": `{"info":{},"messages":[
			{"info":{"role":"user"},"parts":[{"type":"text","text":"add a flag"}]},
			{"info":{"role":"assistant"},"parts":[{"type":"tool","tool":"read","state":{"input":{"path":"main.go"},"output":"TOOL OUTPUT"}},{"type":"text","text":"Added."}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			turns, err := parseOpenCodeExport([]byte(data))
			if err != nil {
				t.Fatal(err)
			}
			wantInOrder(t, render(OpenCode, turns), []string{"## user\n\nadd a flag", `→ read: {"path":"main.go"}`, "## opencode\n\nAdded."},
				[]string{"REASONING", "TOOL OUTPUT"})
		})
	}

	// openCodeTurns verifies directory ownership from the exported info
	// (even when the session is older than the 50 returned by session list)
	// and rejects sessions from other directories.
	wt := t.TempDir()
	bin := filepath.Join(t.TempDir(), "opencode")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"list\" ]; then echo '[]'; exit 0; fi\n" +
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"export\" ]; then\n" +
		"  case \"$3\" in\n" +
		"    old51) echo '{\"info\":{\"directory\":\"" + wt + "\"},\"messages\":[{\"type\":\"user\",\"text\":\"from 51st\"}]}' ;;\n" +
		"    other) echo '{\"info\":{\"directory\":\"/elsewhere\"},\"messages\":[{\"type\":\"user\",\"text\":\"secret\"}]}' ;;\n" +
		"    nodir) echo '{\"info\":{},\"messages\":[{\"type\":\"user\",\"text\":\"no dir\"}]}' ;;\n" +
		"  esac\n" +
		"fi\n"
	writeFile(t, bin, script)
	_ = os.Chmod(bin, 0o755)
	specs := map[Kind]Spec{OpenCode: {Command: []string{bin}}}
	if got, err := Transcript(specs, OpenCode, "old51", wt); err != nil || !strings.Contains(got, "from 51st") {
		t.Fatalf("older session in same dir should succeed via export info.directory: %v\n%s", err, got)
	}
	if _, err := Transcript(specs, OpenCode, "other", wt); err == nil {
		t.Fatal("accepted opencode session from another directory")
	}
	if _, err := Transcript(specs, OpenCode, "nodir", wt); err == nil {
		t.Fatal("accepted opencode session with unknown directory")
	}
}

// Copilot's events log.
func TestCopilotTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".copilot", "session-state", "c1", "workspace.yaml"), "cwd: /w\n")
	writeFile(t, filepath.Join(home, ".copilot", "session-state", "c1", "events.jsonl"), strings.Join([]string{
		`{"type":"session.start","data":{}}`,
		`{"type":"user.message","data":{"content":"rename it"}}`,
		`{"type":"assistant.message","data":{"content":"Renaming.","toolRequests":[{"name":"bash","arguments":{"command":"git mv a b"}}]}}`,
		`{"type":"tool.execution_complete","data":{"result":"TOOL OUTPUT"}}`,
	}, "\n"))
	got, err := Transcript(nil, Copilot, "c1", "/w")
	if err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, got, []string{"## user\n\nrename it", "## copilot\n\nRenaming.", `→ bash: {"command":"git mv a b"}`}, []string{"TOOL OUTPUT"})
	if _, err := Transcript(nil, Copilot, "c1", "/other"); err == nil {
		t.Fatal("accepted copilot session from another worktree")
	}
	_ = os.Remove(filepath.Join(home, ".copilot", "session-state", "c1", "workspace.yaml"))
	if _, err := Transcript(nil, Copilot, "c1", "/w"); err == nil {
		t.Fatal("accepted copilot session with missing workspace.yaml when dir is set")
	}
}

// A long conversation keeps its first question and its end.
func TestLongTranscriptKeepsItsEnds(t *testing.T) {
	turns := []turn{{"user", "THE FIRST QUESTION"}}
	for i := 0; i < 400; i++ {
		turns = append(turns, turn{"claude", strings.Repeat("x", 2000)})
	}
	turns = append(turns, turn{"user", strings.Repeat("y", 3*maxSaid)}, turn{"claude", "THE LAST ANSWER"})
	got := render(Claude, turns)
	if len(got) > maxTranscript+1024 {
		t.Fatalf("%d bytes, more than %d", len(got), maxTranscript)
	}
	wantInOrder(t, got, []string{"THE FIRST QUESTION", "is left out", "more characters]", "THE LAST ANSWER"}, nil)
}

// cursor-agent's transcript, with what the user typed wrapped in a tag.
func TestCursorTranscript(t *testing.T) {
	data, dir := t.TempDir(), t.TempDir()
	t.Setenv("CURSOR_DATA_DIR", data)
	t.Setenv("CURSOR_CONFIG_DIR", t.TempDir())
	real, _ := filepath.EvalSymlinks(dir)
	project := strings.Trim(nonAlnum.ReplaceAllString(real, "-"), "-")
	writeFile(t, filepath.Join(data, "projects", project, "agent-transcripts", "c1", "c1.jsonl"), strings.Join([]string{
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_query>\nfix the parser\n</user_query>"}]}}`,
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_info>INFO</user_info>"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Looking."},{"type":"tool_use","name":"Shell","input":{"command":"go test ./..."}}]}}`,
		`{"type":"turn_ended","status":"success"}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Fixed it."}]}}`,
	}, "\n"))
	got, err := Transcript(nil, Cursor, "c1", dir)
	if err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, got, []string{"## user\n\nfix the parser", "## cursor\n\nLooking.", "→ Shell: {\"command\":\"go test ./...\"}", "## cursor\n\nFixed it."},
		[]string{"INFO", "user_query", "turn_ended"})
}

// Gemini CLI's .jsonl log: messages written again as they grow, and a
// rewind that drops what came after.
func TestGeminiTranscript(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".gemini", "projects.json"), `{"projects":{"`+dir+`":"my-app"}}`)
	chats := filepath.Join(home, ".gemini", "tmp", "my-app", "chats")
	writeFile(t, filepath.Join(chats, "session-2026-10-02T10-00-a1b2c3d4.jsonl"), strings.Join([]string{
		`{"sessionId":"a1b2c3d4-0000-4000-8000-000000000000","projectHash":"x","startTime":"t"}`,
		`{"id":"m1","type":"user","content":[{"text":"fix the parser"}]}`,
		`{"id":"m2","type":"gemini","content":"Look"}`,
		`{"id":"m2","type":"gemini","content":"Looking.","toolCalls":[{"name":"run_shell_command","args":{"command":"go test ./..."}}]}`,
		`{"id":"m3","type":"user","content":"DROPPED"}`,
		`{"$rewindTo":"m3"}`,
		`{"id":"m4","type":"info","content":"INFO"}`,
		`{"id":"m5","type":"gemini","content":"Fixed it."}`,
	}, "\n"))
	for _, id := range []string{"a1b2c3d4-0000-4000-8000-000000000000", ""} {
		got, err := Transcript(nil, Gemini, id, dir)
		if err != nil {
			t.Fatal(err)
		}
		wantInOrder(t, got, []string{"## user\n\nfix the parser", "## gemini\n\nLooking.", "→ run_shell_command: {\"command\":\"go test ./...\"}", "## gemini\n\nFixed it."},
			[]string{"DROPPED", "INFO", "Look\n"})
	}
	if l := Resume(nil, Gemini, "", dir, nil); l.SessionID != "a1b2c3d4-0000-4000-8000-000000000000" {
		t.Errorf("resume the newest here: %+v", l)
	}
	if _, err := Transcript(nil, Gemini, "a1b2c3d4-0000-4000-8000-000000000000", "/other"); err == nil {
		t.Fatal("accepted gemini session from unknown worktree")
	}
}
