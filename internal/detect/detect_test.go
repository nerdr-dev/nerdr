package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeTitleSpinnerIsWorking(t *testing.T) {
	r := For("claude").Classify(Input{Title: "◑ Fixing the tests", Screen: "❯ \n"})
	if r.State != Working {
		t.Fatalf("got %v (%s), want working", r.State, r.Rule)
	}
}

func TestClaudePromptBoxIsIdle(t *testing.T) {
	screen := "● Done.\n\n" +
		"────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle)\n"
	r := For("claude").Classify(Input{Title: "✳ Fixing the tests", Screen: screen})
	if r.State != Idle {
		t.Fatalf("got %v (%s), want idle", r.State, r.Rule)
	}
}

// TestClaudeWithABackgroundShell: claude 2.1.312, a command left running
// with run_in_background. Its turn is over, as copilot's is with a shell
// left running: a dev server runs for ever. When the shell ends, claude
// takes its turn up again by itself, and the title's spinner says so.
func TestClaudeWithABackgroundShell(t *testing.T) {
	m := For("claude")
	screen := "● started\n\n" +
		"✻ Cooked for 5s · done 9:08 PM · 1 shell still running\n\n" +
		"────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on · 1 shell · ← 1 agent\n"
	if r := m.Classify(Input{Title: "✳ Background sleep command", Screen: screen}); r.State != Idle {
		t.Errorf("waiting on the shell: got %v (%s), want idle", r.State, r.Rule)
	}
	if r := m.Classify(Input{Title: "◐ Background sleep command", Screen: screen}); r.State != Working || !r.Visible {
		t.Errorf("woken by the shell's end: got %v (%s, visible %v), want working", r.State, r.Rule, r.Visible)
	}
}

func TestClaudePermissionPromptIsBlocked(t *testing.T) {
	screen := "Bash command\n  rm -rf build\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\nEsc to cancel · Tab to amend\n"
	r := For("claude").Classify(Input{Screen: screen})
	if r.State != Blocked {
		t.Fatalf("got %v (%s), want blocked", r.State, r.Rule)
	}
}

// TestVisibleMarksTheStatesOwnUI covers Result.Visible: a dialog or the
// prompt box is the state's own UI; text that merely tends to go with a
// question is not.
func TestVisibleMarksTheStatesOwnUI(t *testing.T) {
	m := For("claude")
	if r := m.Classify(Input{Screen: "Bash command\n  rm -rf build\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\nEsc to cancel · Tab to amend\n"}); r.State != Blocked || !r.Visible {
		t.Errorf("permission prompt: %+v", r)
	}
	if r := m.Classify(Input{Screen: "Do you want to keep the old name? yes, I think so\n"}); r.State != Blocked || r.Visible {
		t.Errorf("a question in the agent's text: %+v", r)
	}
	if r := m.Classify(Input{Screen: "nothing known\n"}); r.Rule != FallbackRule || r.Visible {
		t.Errorf("fallback: %+v", r)
	}
}

func TestAllBundledManifestsCompile(t *testing.T) {
	files, err := bundled.ReadDir("manifests")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("only %d manifests bundled", len(files))
	}
	for _, f := range files {
		data, err := bundled.ReadFile("manifests/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		m, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", f.Name(), err)
		}
		// For looks a kind up by its file name.
		if m.ID+".toml" != f.Name() {
			t.Errorf("%s has id %q", f.Name(), m.ID)
		}
	}
}

// The codex screens below were captured from codex-cli 0.160.0 running in
// nerdr's own terminal.

const codexTrust = `
  Folder access
  /home/me/src/app

  Trust this folder? Codex can read, edit, and run files here, subject to your permission
  settings. Folder settings can run code automatically, even without a model request. Continue
  only if you trust these files. Your trust decision will be saved.

› 1. Trust and continue
  2. Quit

  enter continue · esc quit
`

const codexWorking = `  >_ OpenAI Codex (v0.160.0)
     /home/me/src/app

› Run the shell command sleep 10 then reply done

• I’ll run sleep 10, then reply.

• Working (9s • esc to interrupt) · 1 background terminal running · /ps to view · /stop to close

› Ask Codex to do anything

  GPT-6.1-Sol default · /home/me/src/app
  ← for agents · ? for shortcuts
`

const codexDone = `  >_ OpenAI Codex (v0.160.0)
     /home/me/src/app

› Reply with just the word hi

• hi

  Worked for 2s • 8:33 PM

› Ask Codex to do anything

  GPT-6.1-Sol default · /home/me/src/app
  ← for agents · ? for shortcuts
`

func TestCodexStates(t *testing.T) {
	m := For("codex")
	for _, c := range []struct {
		name string
		in   Input
		want State
	}{
		{"trust dialog", Input{Screen: codexTrust}, Blocked},
		{"spinner in the title", Input{Title: "⠏ Run sleep 10 command | app", Screen: codexWorking}, Working},
		{"working, no title", Input{Screen: codexWorking}, Working},
		{"done", Input{Title: "Reply with hi | app", Screen: codexDone}, Idle},
		{"done, no title", Input{Screen: codexDone}, Idle},
		{"action required", Input{Title: "Action Required | app", Screen: codexDone}, Blocked},
	} {
		if r := m.Classify(c.in); r.State != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, r.State, r.Rule, c.want)
		}
	}
}

// The agy screens below were captured from Antigravity CLI 1.2.16 running
// in nerdr's own terminal.

const agyTrust = `Accessing workspace:
/home/me/src/app

Do you trust the contents of this project?
Antigravity CLI requires permission to read, edit, and execute files here.

> Yes, I trust this folder
  No, exit

  ↑/↓ Navigate · enter Confirm
                                                                                 Gemini 3.8 Flash · high
`

const agyPermission = `  Antigravity CLI 1.2.16
  /home/me/src/app
────────────────────────────────────────────────────────────
> Run the shell command: touch hello.txt

● Bash(touch hello.txt) (ctrl+o to expand)
Command
────────────────────────────────────────────────────────────────────────────────────────────────────────
Requesting permission for:
   touch hello.txt

Run this command?
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with 'touch'
  3. Yes, and always allow for commands that start with 'touch' (Persist to settings.json)
  4. No, cancel

  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel                                                                    Gemini 3.8 Flash · high
`

const agyIdle = `  Antigravity CLI 1.2.16
  /home/me/src/app
────────────────────────────────────────────────────────────────────────────────────────────────────────
>
────────────────────────────────────────────────────────────────────────────────────────────────────────
? for shortcuts                                                                  Gemini 3.8 Flash · high
`

func TestAgyStates(t *testing.T) {
	m := For("agy")
	for _, c := range []struct {
		name   string
		screen string
		want   State
	}{
		{"trust dialog", agyTrust, Blocked},
		{"permission prompt", agyPermission, Blocked},
		{"prompt box", agyIdle, Idle},
	} {
		if r := m.Classify(Input{Screen: c.screen}); r.State != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, r.State, r.Rule, c.want)
		}
	}
}

// The opencode screens below were captured from opencode v2.0.20 running
// in nerdr's own terminal.

func TestOpenCodeStates(t *testing.T) {
	m := For("opencode")
	for _, c := range []struct {
		name string
		in   Input
		want State
	}{
		{"working footer", Input{Screen: "  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n  ⬝⬝⬝⬝⬝⬝⬝⬝ esc interrupt  ↓ 1 shell · $0.00  ctrl+p commands  /tmp/opencode\n"}, Working},
		{"permission", Input{Screen: "  △ Permission required\n  bash: ls\n  ⇆ select  enter confirm  esc reject\n"}, Blocked},
		// A conversation that quotes the working hint or the dialog's title
		// above the footer must not read as working or asking.
		{"transcript quotes the hint", Input{Screen: "  - The footer says esc interrupt while working.\n  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n  /tmp/opencode  ctrl+p commands\n"}, Idle},
		{"transcript quotes the title", Input{Screen: "  - The permission dialog shows `△ Permission required`.\n"}, Idle},
	} {
		if r := m.Classify(c.in); r.State != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, r.State, r.Rule, c.want)
		}
	}
}

// TestCopilotWaitingForBackgroundAgents: copilot 1.0.98, captured in
// nerdr's own terminal, waiting for a subagent it started in the
// background; the dot before the line changes.
func TestCopilotWaitingForBackgroundAgents(t *testing.T) {
	m := For("copilot")
	for _, dot := range []string{"●", "◎"} {
		screen := " ● started\n" +
			" /tmp/cp                                Session: 0.39 AIC used\n" +
			"╻▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n┃\n╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n" +
			" " + dot + " Waiting for background agents · 207 B esc stop agents                    Auto → gpt-6-luna\n"
		if r := m.Classify(Input{Screen: screen}); r.State != Working || !r.Visible {
			t.Errorf("%s: got %v (%s, visible %v), want working", dot, r.State, r.Rule, r.Visible)
		}
	}
}

// TestCopilotWaitingForBackgroundShells: copilot 1.0.98, captured in
// nerdr's own terminal, with a dev server left running in an async shell.
// Its turn is over: waiting on the shell is not work. The same footer
// during a turn is.
func TestCopilotWaitingForBackgroundShells(t *testing.T) {
	m := For("copilot")
	screen := func(footer string) string {
		return " ● Storybook is running at http://localhost:6006/.\n" +
			" /tmp/cp                                Session: 0.13 AIC used\n" +
			"╻▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n┃\n╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n" +
			" " + footer + "                    Auto → gpt-6-luna\n"
	}
	if r := m.Classify(Input{Screen: screen("● Waiting for background shells · 609 B esc esc interrupt")}); r.State != Idle {
		t.Errorf("waiting for shells: got %v (%s), want idle", r.State, r.Rule)
	}
	if r := m.Classify(Input{Screen: screen("◉ Working · 147 B esc esc interrupt")}); r.State != Working {
		t.Errorf("working: got %v (%s), want working", r.State, r.Rule)
	}
}

func TestPromptMarkerRegions(t *testing.T) {
	for _, c := range []struct{ screen, spec, want string }{
		{"before\n› input\nafter", "after_last_prompt_marker", "after"},
		{"before\n› input\nafter", "before_current_prompt_marker", "before"},
		{"before\n› input\nafter", "whole_recent_without_current_prompt_marker", ""},
		{"no marker", "whole_recent_without_current_prompt_marker", "no marker"},
		{"• old\n■ latest\n› input", "current_prompt_block_marker", "■ latest"},
		{"• old\n■ latest\n› input", "after_current_prompt_block_marker", "■ latest\n› input"},
		// A response after the prompt makes it an old one, not the current.
		{"› old\n• new", "current_prompt_block_marker", ""},
		{"› old\n• new", "before_current_prompt_marker", "› old\n• new"},
	} {
		if got := region(Input{Screen: c.screen}, c.spec); got != c.want {
			t.Errorf("%s of %q: %q, want %q", c.spec, c.screen, got, c.want)
		}
	}
}

func TestManifestForNewerEngineIsRefused(t *testing.T) {
	if _, err := Parse([]byte("id = \"x\"\nmin_engine_version = 99\n")); err == nil {
		t.Fatal("a manifest for a newer engine was taken")
	}
}

// TestOlderHerdrCacheLosesToBundled covers herdr's cached manifest being
// left behind by the bundled one: herdr refreshes it only while it runs.
func TestOlderHerdrCacheLosesToBundled(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(state, "herdr", "agent-detection", "remote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(version string) {
		data := "id = \"codex\"\nversion = \"" + version + "\"\nmin_engine_version = 3\n"
		if err := os.WriteFile(filepath.Join(dir, "codex.toml"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("2000.01.01.1")
	if m := load("codex"); m.Source != "bundled" {
		t.Errorf("an older cache was used: %s %s", m.Source, m.Version)
	}
	write("9999.01.01.1")
	if m := load("codex"); m.Source == "bundled" {
		t.Error("a newer cache was passed over")
	}
}

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2026.09.23.1", "2026.10.01.1", true},
		{"2026.10.01.1", "2026.10.01.1", false},
		{"2026.10.01.2", "2026.10.01.1", false},
		{"2026.9.30.1", "2026.10.01.1", true},
		{"", "2026.10.01.1", true},
	} {
		if got := olderVersion(c.a, c.b); got != c.want {
			t.Errorf("olderVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestKnowsIdle(t *testing.T) {
	for id, want := range map[string]bool{"claude": true, "codex": true, "opencode": false, "copilot": false} {
		if got := For(id).KnowsIdle(); got != want {
			t.Errorf("%s: %v, want %v", id, got, want)
		}
	}
}

func TestGoRegexTakesHerdrsRustSyntax(t *testing.T) {
	for in, want := range map[string]string{
		`^\s*[\u2800-\u28FF]+\s+\p{Alphabetic}+\w*ing\b`: `^\s*[\x{2800}-\x{28FF}]+\s+\p{L}+\w*ing\b`,
		`\u{2800}`:     `\x{2800}`,
		`a\\u2800`:     `a\\u2800`,
		`(?i)thinking`: `(?i)thinking`,
	} {
		if got := goRegex(in); got != want {
			t.Errorf("goRegex(%q) = %q, want %q", in, got, want)
		}
	}
	r := For("cursor").Classify(Input{Screen: "  ⠋ Generating\n"})
	if r.State != Working {
		t.Errorf("cursor's spinner: %+v", r)
	}
}
