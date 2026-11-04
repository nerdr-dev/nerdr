package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrokStateFollowsTheTurn covers each event as Grok Build 1.0.70 sends
// it.
func TestGrokStateFollowsTheTurn(t *testing.T) {
	for _, c := range []struct {
		ev   GrokEvent
		want string
	}{
		{GrokEvent{Event: "SessionStart"}, ""},
		{GrokEvent{Event: "UserPromptSubmit"}, "working"},
		{GrokEvent{Event: "PostToolUse"}, "working"},
		{GrokEvent{Event: "PostToolUseFailure"}, "working"},
		{GrokEvent{Event: "PermissionDenied"}, "working"},
		{GrokEvent{Event: "Notification", NotificationType: "permission_prompt"}, "blocked"},
		{GrokEvent{Event: "Notification", NotificationType: "idle_prompt"}, "idle"},
		{GrokEvent{Event: "Notification", NotificationType: "task_complete"}, ""},
		{GrokEvent{Event: "Stop"}, "idle"},
		{GrokEvent{Event: "StopFailure"}, "idle"},
		{GrokEvent{Event: "StopCancelled"}, "idle"},
	} {
		if got := GrokState(c.ev); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

func TestGrokReports(t *testing.T) {
	// /new or /resume: the conversation it is now in.
	if method, p := grokReport(GrokEvent{Event: "SessionStart", SessionID: "s2"}, "t"); method != ReportAgentSession || p["agent_session_id"] != "s2" || p["agent"] != "grok" {
		t.Errorf("session start: %q %v", method, p)
	}
	// At its end grok stops the old one too, after the new one: it must not
	// take the tab back there.
	if method, p := grokReport(GrokEvent{Event: "Stop", SessionID: "s1"}, "t"); method != ReportAgent || p["state"] != "idle" || p["source"] != GrokSource || p["agent_session_id"] != nil {
		t.Errorf("stop: %q %v", method, p)
	}
	// A subagent's stop is not the agent's, and its conversation not its.
	if method, _ := grokReport(GrokEvent{Event: "Stop", SessionID: "sub", SubagentType: "explore"}, "t"); method != "" {
		t.Errorf("subagent reported: %q", method)
	}
	if method, _ := grokReport(GrokEvent{Event: "Notification", NotificationType: "task_complete"}, "t"); method != "" {
		t.Errorf("says nothing, reported: %q", method)
	}
}

// The payload of a real hook: the fields are read from it, and the name it
// spells its own way is not.
func TestGrokEventIsRead(t *testing.T) {
	var ev GrokEvent
	in := `{"hookEventName":"notification","sessionId":"s1","notificationType":"permission_prompt","notification_type":"permission_prompt"}`
	if err := json.Unmarshal([]byte(in), &ev); err != nil {
		t.Fatal(err)
	}
	ev.Event = "Notification"
	if ev.SessionID != "s1" || GrokState(ev) != "blocked" {
		t.Errorf("%+v", ev)
	}
}

func TestGrokCommandReportsOnlyForNerdrsGrok(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	t.Setenv("NERDR_GROK_HOOK", "100")
	parents := map[int]int{200: 100, 300: 250}
	defer func(f func(int) int) { parentOf = f }(parentOf)
	parentOf = func(pid int) int { return parents[pid] }
	var stderr bytes.Buffer
	run := func(event, pid, in string) {
		t.Helper()
		if code := GrokCommand([]string{event, pid}, strings.NewReader(in), &stderr); code != 0 {
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

	run("SessionStart", "200", `{"sessionId":"s1"}`)
	if r := next(t, got); r.Method != ReportAgentSession || r.SessionID != "s1" || r.Source != GrokSource || r.Agent != "grok" || r.Pane != "tab1" {
		t.Fatalf("report = %+v", r)
	}
	run("Stop", "200", `{"sessionId":"s1"}`)
	if r := next(t, got); r.Method != ReportAgent || r.State != "idle" {
		t.Fatalf("report = %+v", r)
	}

	// A grok the agent ran: its parent is not nerdr.
	run("Stop", "300", `{"sessionId":"s9"}`)
	none("another grok")

	// Outside nerdr.
	t.Setenv("NERDR_GROK_HOOK", "")
	run("Stop", "200", `{"sessionId":"s1"}`)
	none("a grok outside nerdr")
}

func TestGrokHooksFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := GrokHooksHome(home)
	wrote, made, err := InstallGrokHooks(file)
	if err != nil || !wrote || !made {
		t.Fatalf("wrote %v made %v: %v", wrote, made, err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type, Command string
			}
		}
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	for _, ev := range grokEvents {
		g := f.Hooks[ev]
		if len(g) != 1 || len(g[0].Hooks) != 1 || g[0].Hooks[0].Type != "command" || !strings.Contains(g[0].Hooks[0].Command, "hook grok "+ev+` "$PPID"`) {
			t.Errorf("%s: %+v", ev, g)
		}
	}
	if wrote, _, err := InstallGrokHooks(file); wrote || err != nil {
		t.Errorf("a second time: wrote %v, %v", wrote, err)
	}
	// An older version of nerdr's is replaced.
	if err := os.WriteFile(file, []byte(`{"x":"nerdr hook grok Stop"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, made, err := InstallGrokHooks(file); !wrote || made || err != nil {
		t.Errorf("older: wrote %v made %v, %v", wrote, made, err)
	}
	// The user's own is left, which turns them off.
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, _, err := InstallGrokHooks(file); wrote || err != nil {
		t.Errorf("the user's: wrote %v, %v", wrote, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "{}\n" {
		t.Errorf("changed the user's: %q", b)
	}
}

func TestGrokHooksInAWorktreeRefuseLinks(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	if err := os.Symlink(other, filepath.Join(dir, ".grok")); err != nil {
		t.Skip(err)
	}
	if _, _, err := InstallGrokHooksProject(dir); err == nil {
		t.Fatal("followed the link out of the worktree")
	}
	if _, err := os.Stat(filepath.Join(other, "hooks", "nerdr.json")); err == nil {
		t.Fatal("wrote outside the worktree")
	}
}
