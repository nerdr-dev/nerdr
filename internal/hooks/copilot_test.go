package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestCopilotStateFollowsTheTurn covers what each of copilot's events
// means, as a real copilot 1.0.98 sends them: a turn is work from the
// prompt until Stop, and a permission prompt waits for the user. A
// subagent's events carry its agent_id, its Stop too.
func TestCopilotStateFollowsTheTurn(t *testing.T) {
	for _, c := range []struct {
		ev   CopilotEvent
		want string
	}{
		{CopilotEvent{Event: "UserPromptSubmit"}, "working"},
		// It comes with the first prompt, after UserPromptSubmit.
		{CopilotEvent{Event: "SessionStart", Source: "new"}, ""},
		{CopilotEvent{Event: "PreToolUse"}, "working"},
		{CopilotEvent{Event: "PostToolUse"}, "working"},
		{CopilotEvent{Event: "PostToolUseFailure"}, "working"},
		{CopilotEvent{Event: "Notification", Type: "permission_prompt"}, "blocked"},
		{CopilotEvent{Event: "Notification", Type: "other"}, ""},
		// It comes for tools allowed anyway: no question asked.
		{CopilotEvent{Event: "PermissionRequest"}, ""},
		{CopilotEvent{Event: "Stop"}, "idle"},
		{CopilotEvent{Event: "SessionEnd"}, ""},
		// A subagent's turn is inside the agent's own.
		{CopilotEvent{Event: "UserPromptSubmit", AgentID: "a1"}, ""},
		{CopilotEvent{Event: "PostToolUse", AgentID: "a1"}, ""},
		{CopilotEvent{Event: "Stop", AgentID: "a1"}, ""},
		{CopilotEvent{Event: "SubagentStop", AgentID: "a1"}, ""},
		// But its question waits for the user all the same.
		{CopilotEvent{Event: "Notification", Type: "permission_prompt", AgentID: "a1"}, "blocked"},
		// Nor does one starting, by itself; see subagents.go.
		{CopilotEvent{Event: "SubagentStart", AgentID: "a1"}, ""},
	} {
		if got := CopilotState(c.ev, SubagentWait{}); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

// TestCopilotWaitsForBackgroundSubagents covers a subagent started in the
// background, as a real copilot 1.0.98 runs one: the agent's turn ends
// with a Stop while it goes on, and once its SubagentStop has come copilot
// prompts the agent with the news, which ends with a Stop of its own.
func TestCopilotWaitsForBackgroundSubagents(t *testing.T) {
	file := filepath.Join(t.TempDir(), "subagents.json")
	state := func(ev CopilotEvent) string {
		return CopilotState(ev, seeSubagents(file, ev.Event, ev.AgentID, ev.Event == "SessionStart" && ev.AgentID == ""))
	}
	for i, c := range []struct {
		ev   CopilotEvent
		want string
	}{
		{CopilotEvent{Event: "UserPromptSubmit"}, "working"},
		{CopilotEvent{Event: "SessionStart", Source: "new"}, ""},
		{CopilotEvent{Event: "PreToolUse"}, "working"},
		{CopilotEvent{Event: "SubagentStart", AgentID: "a1"}, ""},
		{CopilotEvent{Event: "UserPromptSubmit", AgentID: "a1"}, ""},
		{CopilotEvent{Event: "Stop"}, "working"}, // a1 is still at it
		{CopilotEvent{Event: "Stop", AgentID: "a1"}, ""},
		{CopilotEvent{Event: "SubagentStop", AgentID: "a1"}, "working"},
		{CopilotEvent{Event: "UserPromptSubmit"}, "working"},
		{CopilotEvent{Event: "Stop"}, "idle"},
		// One left going when the conversation changes is not waited for.
		{CopilotEvent{Event: "SubagentStart", AgentID: "a2"}, ""},
		{CopilotEvent{Event: "SessionStart", Source: "new"}, ""},
		{CopilotEvent{Event: "Stop"}, "idle"},
	} {
		if got := state(c.ev); got != c.want {
			t.Errorf("%d %+v: %q, want %q", i, c.ev, got, c.want)
		}
	}
}

// TestCopilotReportsItsSessionNotASubagents covers which conversation the
// reports say the agent is in: its own, from the session's start, never
// with a subagent's report.
func TestCopilotReportsItsSessionNotASubagents(t *testing.T) {
	method, p := copilotReport(CopilotEvent{Event: "SessionStart", Source: "new", SessionID: "s1"}, "tab1", SubagentWait{})
	if method != ReportAgentSession || p["state"] != nil || p["agent_session_id"] != "s1" ||
		p["source"] != CopilotSource || p["agent"] != "copilot" {
		t.Fatalf("%s %v", method, p)
	}
	if method, p := copilotReport(CopilotEvent{Event: "Stop", SessionID: "s1", AgentID: "a1"}, "tab1", SubagentWait{}); method != "" {
		t.Fatalf("a subagent's Stop reported: %s %v", method, p)
	}
	method, p = copilotReport(CopilotEvent{Event: "Notification", Type: "permission_prompt", SessionID: "s1", AgentID: "a1"}, "tab1", SubagentWait{})
	if method != ReportAgent || p["state"] != "blocked" || p["agent_session_id"] != nil {
		t.Fatalf("%s %v", method, p)
	}
}

// TestCopilotCommandReportsToTheTab covers `nerdr hook copilot <event>`
// end to end: the event its hook names, with copilot's JSON on stdin,
// reaches nerdr as the tab's report; outside nerdr nothing is sent.
func TestCopilotCommandReportsToTheTab(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	var stderr bytes.Buffer
	in := `{"hook_event_name":"Stop","session_id":"11c8","timestamp":"2026-10-18T18:16:18.696Z","stop_reason":"end_turn","stop_hook_active":false}`
	if code := CopilotCommand([]string{"Stop"}, strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	r := next(t, got)
	if r.Method != ReportAgent || r.Pane != "tab1" || r.Source != CopilotSource || r.Agent != "copilot" ||
		r.State != "idle" || r.SessionID != "11c8" || r.Seq <= 0 {
		t.Fatalf("report = %+v", r)
	}
	if code := CopilotCommand([]string{"Stop"}, strings.NewReader("not json"), &stderr); code == 0 {
		t.Fatal("no event, but exit 0")
	}
	t.Setenv("NERDR_TAB", "")
	if code := CopilotCommand([]string{"Stop"}, strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("outside nerdr: exit %d", code)
	}
	select {
	case r := <-got:
		t.Fatalf("reported outside nerdr: %+v", r)
	default:
	}
}

// TestCopilotPluginHooksEveryEventNerdrReads covers the plugin: a manifest
// marked as nerdr's, and a hook for each event CopilotState reads, naming
// it.
func TestCopilotPluginHooksEveryEventNerdrReads(t *testing.T) {
	files := copilotPlugin()
	if !bytes.Contains(files[filepath.Join(".plugin", "plugin.json")], []byte(CopilotMarker)) {
		t.Error("the manifest is not marked as nerdr's")
	}
	var h struct {
		Version int                         `json:"version"`
		Hooks   map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(files[filepath.Join("hooks", "hooks.json")], &h); err != nil || h.Version != 1 {
		t.Fatalf("hooks.json: %v %+v", err, h)
	}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "Stop", "SubagentStart", "SubagentStop"} {
		hs := h.Hooks[ev]
		if len(hs) != 1 || hs[0]["type"] != "command" || hs[0]["bash"] != `"$NERDR_BIN" hook copilot `+ev+` >/dev/null 2>&1 || true` {
			t.Errorf("%s: %v", ev, hs)
		}
	}
}

// TestCopilotPluginInstallUpdatesOnlyNerdrs covers installing the plugin:
// written when missing, an outdated copy of nerdr's brought up to date,
// and one the user took over left alone.
func TestCopilotPluginInstallUpdatesOnlyNerdrs(t *testing.T) {
	home := t.TempDir()
	dir := CopilotPluginHome(home)
	if dir != filepath.Join(home, ".copilot", "nerdr-plugin") {
		t.Fatalf("home: %s", dir)
	}
	if p := CopilotPluginProject("/w"); p != filepath.Join("/w", ".copilot", "nerdr-plugin") || !strings.HasSuffix(p+"/", filepath.FromSlash(CopilotPluginExclude)) {
		t.Fatalf("project: %s", p)
	}
	if HasCopilotPlugin(dir) {
		t.Fatal("a plugin before it was installed")
	}
	if wrote, err := InstallCopilotPlugin(dir); !wrote || err != nil {
		t.Fatalf("first install: %v %v", wrote, err)
	}
	if !HasCopilotPlugin(dir) {
		t.Fatal("no plugin once installed")
	}
	if wrote, _ := InstallCopilotPlugin(dir); wrote {
		t.Fatal("an up-to-date install should write nothing")
	}
	hooksFile := filepath.Join(dir, "hooks", "hooks.json")
	_ = os.WriteFile(hooksFile, []byte("{}"), 0o644)
	if wrote, _ := InstallCopilotPlugin(dir); !wrote {
		t.Fatal("nerdr's outdated copy was not updated")
	}
	if b, _ := os.ReadFile(hooksFile); !bytes.Equal(b, copilotPlugin()[filepath.Join("hooks", "hooks.json")]) {
		t.Fatal("the update did not write the hooks")
	}
	_ = os.WriteFile(filepath.Join(dir, ".plugin", "plugin.json"), []byte(`{"name":"mine"}`), 0o644)
	_ = os.WriteFile(hooksFile, []byte("{}"), 0o644)
	if wrote, _ := InstallCopilotPlugin(dir); wrote {
		t.Fatal("the user's own copy was replaced")
	}

	// A symbolic link, hard link or FIFO in the worktree must not have
	// nerdr overwrite a host file or block.
	wt := t.TempDir()
	proj := CopilotPluginProject(wt)
	if _, err := InstallCopilotPlugin(proj); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o644)
	_ = os.Remove(filepath.Join(proj, "hooks", "hooks.json"))
	if err := os.Symlink(victim, filepath.Join(proj, "hooks", "hooks.json")); err == nil {
		if _, err := InstallCopilotPlugin(proj); err == nil {
			t.Fatal("InstallCopilotPlugin wrote through a symbolic link")
		}
		if got, _ := os.ReadFile(victim); string(got) != "keep" {
			t.Fatalf("victim overwritten: %q", got)
		}
	}

	// Replacing a parent folder (.copilot, .opencode, .agents) with a
	// symbolic link in a worktree is rejected too.
	for _, tc := range []struct {
		dot string
		run func(string) error
	}{
		{".copilot", func(d string) error { _, err := InstallCopilotPlugin(CopilotPluginProject(d)); return err }},
		{".opencode", func(d string) error { _, err := InstallOpenCodePlugin(OpenCodePluginProject(d)); return err }},
		{".agents", func(d string) error { _, _, err := InstallAgyHooks(AgyHooksProject(d)); return err }},
	} {
		w := t.TempDir()
		out := t.TempDir()
		if err := os.Symlink(out, filepath.Join(w, tc.dot)); err == nil {
			if err := tc.run(w); err == nil {
				t.Fatalf("%s parent symlink in worktree was followed", tc.dot)
			}
		}
	}

	// A hardlinked file in the worktree is replaced by a new inode, leaving
	// the hardlink target untouched.
	wtHard := t.TempDir()
	ocPath := OpenCodePluginProject(wtHard)
	if _, err := InstallOpenCodePlugin(ocPath); err != nil {
		t.Fatal(err)
	}
	hardTarget := filepath.Join(wtHard, "target.js")
	if err := os.WriteFile(hardTarget, []byte(OpenCodeMarker+"\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(ocPath)
	if err := os.Link(hardTarget, ocPath); err == nil {
		if _, err := InstallOpenCodePlugin(ocPath); err != nil {
			t.Fatalf("InstallOpenCodePlugin over hardlink: %v", err)
		}
		if got, _ := os.ReadFile(hardTarget); string(got) != OpenCodeMarker+"\nold\n" {
			t.Fatalf("hardlink target was modified in place: %q", got)
		}
	}

	// A FIFO (named pipe) in the worktree is rejected without blocking.
	wtFifo := t.TempDir()
	ocFifo := OpenCodePluginProject(wtFifo)
	_ = os.MkdirAll(filepath.Dir(ocFifo), 0o755)
	if err := syscall.Mkfifo(ocFifo, 0o600); err == nil {
		if _, err := InstallOpenCodePlugin(ocFifo); err == nil {
			t.Fatal("InstallOpenCodePlugin accepted a FIFO")
		}
	}

	// In the user's home folder, dotfile symlinks (stow/chezmoi) on parent
	// folders are followed.
	dotHome := t.TempDir()
	dotfiles := t.TempDir()
	t.Setenv("HOME", dotHome)
	_ = os.MkdirAll(filepath.Join(dotfiles, "copilot"), 0o755)
	_ = os.MkdirAll(filepath.Join(dotfiles, "opencode"), 0o755)
	_ = os.MkdirAll(filepath.Join(dotfiles, "gemini"), 0o755)
	_ = os.MkdirAll(filepath.Join(dotHome, ".config"), 0o755)
	if err := os.Symlink(filepath.Join(dotfiles, "copilot"), filepath.Join(dotHome, ".copilot")); err == nil {
		if _, err := InstallCopilotPlugin(CopilotPluginHome(dotHome)); err != nil {
			t.Fatalf("home symlinked .copilot failed: %v", err)
		}
	}
	if err := os.Symlink(filepath.Join(dotfiles, "opencode"), filepath.Join(dotHome, ".config", "opencode")); err == nil {
		if _, err := InstallOpenCodePlugin(OpenCodePluginHome(dotHome)); err != nil {
			t.Fatalf("home symlinked .config/opencode failed: %v", err)
		}
	}
	if err := os.Symlink(filepath.Join(dotfiles, "gemini"), filepath.Join(dotHome, ".gemini")); err == nil {
		if _, _, err := InstallAgyHooks(AgyHooksHome(dotHome)); err != nil {
			t.Fatalf("home symlinked .gemini failed: %v", err)
		}
	}

	// File symlink in the home folder (stow) and existing 0600 permissions
	// are preserved.
	dotHome2 := t.TempDir()
	t.Setenv("HOME", dotHome2)
	realHooks := filepath.Join(dotfiles, "my-hooks.json")
	if err := os.WriteFile(realHooks, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Dir(AgyHooksHome(dotHome2)), 0o755)
	if err := os.Symlink(realHooks, AgyHooksHome(dotHome2)); err == nil {
		if _, _, err := InstallAgyHooks(AgyHooksHome(dotHome2)); err != nil {
			t.Fatalf("home file symlink for AgyHooksHome failed: %v", err)
		}
		if st, err := os.Stat(realHooks); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("mode of realHooks = %v, err = %v; want 0600", st.Mode().Perm(), err)
		}
	}

	// A worktree located under ~/.local/share/worktrees, ~/.config, or
	// $XDG_STATE_HOME must still be treated as an untrusted worktree and
	// reject symlinks.
	stateParent := filepath.Join(dotHome2, "state")
	t.Setenv("XDG_STATE_HOME", stateParent)
	for _, wtSub := range []string{
		filepath.Join(dotHome2, ".local", "share", "worktrees", "w"),
		filepath.Join(dotHome2, ".config", "nvim"),
		filepath.Join(stateParent, "worktrees", "w"),
	} {
		ext := t.TempDir()
		_ = os.MkdirAll(wtSub, 0o755)
		_ = os.Symlink(ext, filepath.Join(wtSub, ".opencode"))
		if _, err := InstallOpenCodePluginProject(wtSub); err == nil {
			t.Fatalf("worktree at %s followed .opencode symlink", wtSub)
		}
		_ = os.Symlink(ext, filepath.Join(wtSub, ".copilot"))
		if _, err := InstallCopilotPluginProject(wtSub); err == nil {
			t.Fatalf("worktree at %s followed .copilot symlink", wtSub)
		}
		_ = os.Symlink(ext, filepath.Join(wtSub, ".agents"))
		if _, _, err := InstallAgyHooksProject(wtSub); err == nil {
			t.Fatalf("worktree at %s followed .agents symlink", wtSub)
		}
	}
}
