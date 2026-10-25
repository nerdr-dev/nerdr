package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCodexLaunches(t *testing.T) {
	specs := map[Kind]Spec{Codex: {Command: []string{"sandbox", "--", "codex"}, Args: []string{"--search"}}}
	if l := NewSession(specs, Codex); !slices.Equal(l.Argv, []string{"sandbox", "--", "codex", "--search"}) || l.SessionID != "" {
		t.Errorf("new: %+v", l)
	}
	if l := Resume(specs, Codex, "abc", "/w", nil); !slices.Equal(l.Argv, []string{"sandbox", "--", "codex", "--search", "resume", "abc"}) || l.SessionID != "abc" {
		t.Errorf("resume by id: %+v", l)
	}
	if l := Resume(specs, Codex, "", "/w", nil); !slices.Equal(l.Argv, []string{"sandbox", "--", "codex", "--search", "resume", "--last"}) {
		t.Errorf("resume the latest: %+v", l)
	}
}

// A kind only the config names runs its command, with its resume_args on a
// resume, and is read as the agent its detect names.
func TestCustomKind(t *testing.T) {
	amp := Kind("amp")
	specs := map[Kind]Spec{amp: {Command: []string{"sh"}, Args: []string{"-x"}, ResumeArgs: []string{"--continue"}}}
	if l := NewSession(specs, amp); !slices.Equal(l.Argv, []string{"sh", "-x"}) {
		t.Errorf("new: %+v", l)
	}
	if l := Resume(specs, amp, "", "/w", nil); !slices.Equal(l.Argv, []string{"sh", "-x", "--continue"}) || l.SessionID != "" {
		t.Errorf("resume: %+v", l)
	}
	if !slices.Contains(Available(specs), amp) {
		t.Error("amp's command is on the PATH, but it is not offered")
	}
	if Known(amp) || len(DefaultSpec(amp).Command) != 0 {
		t.Error("amp is not a kind nerdr knows")
	}
	if got := DetectID(specs, amp); got != "amp" {
		t.Errorf("detect: %q", got)
	}
	specs[amp] = Spec{Command: []string{"sh"}, Detect: "claude"}
	if got := DetectID(specs, amp); got != "claude" {
		t.Errorf("detect: %q", got)
	}
}

func TestCursorResumesItsNewestChatHere(t *testing.T) {
	cfg, dir := t.TempDir(), t.TempDir()
	t.Setenv("CURSOR_CONFIG_DIR", cfg)
	if l := NewSession(nil, Cursor); !slices.Equal(l.Argv, []string{"cursor-agent"}) {
		t.Errorf("new: %+v", l)
	}
	if l := Resume(nil, Cursor, "", dir, nil); !slices.Equal(l.Argv, []string{"cursor-agent", "--continue"}) || l.SessionID != "" {
		t.Errorf("nothing to resume: %+v", l)
	}
	writeFile(t, filepath.Join(cursorChats(dir), "old", "store.db"), "")
	writeFile(t, filepath.Join(cursorChats(dir), "new", "store.db"), "")
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(cursorChats(dir), "old", "store.db"), past, past)
	writeFile(t, filepath.Join(cursorChats(t.TempDir()), "other", "store.db"), "")
	if l := Resume(nil, Cursor, "", dir, nil); !slices.Equal(l.Argv, []string{"cursor-agent", "--resume", "new"}) || l.SessionID != "new" {
		t.Errorf("newest here: %+v", l)
	}
}

func TestGeminiLaunches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := NewSession(nil, Gemini)
	if len(l.Argv) != 3 || l.Argv[1] != "--session-id" || l.Argv[2] != l.SessionID || l.SessionID == "" {
		t.Errorf("new: %+v", l)
	}
	if l := Resume(nil, Gemini, "abc", "/w", nil); !slices.Equal(l.Argv, []string{"gemini", "--resume", "abc"}) {
		t.Errorf("resume by id: %+v", l)
	}
	if l := Resume(nil, Gemini, "", "/w", nil); !slices.Equal(l.Argv, []string{"gemini", "--resume", "latest"}) || l.SessionID != "" {
		t.Errorf("resume the latest: %+v", l)
	}
}

func TestAntigravityResumesTheWorktreesConversation(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if l := Resume(nil, Antigravity, "", dir, nil); !slices.Equal(l.Argv, []string{"agy", "--continue"}) {
		t.Errorf("nothing to resume: %+v", l)
	}
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl"), strings.Join([]string{
		`{"display":"a","timestamp":1,"workspace":"` + dir + `","conversationId":"mine-old"}`,
		`{"display":"b","timestamp":2,"workspace":"` + dir + `","conversationId":"mine"}`,
		`{"display":"c","timestamp":3,"workspace":"/elsewhere","conversationId":"sibling"}`,
	}, "\n"))
	if l := Resume(nil, Antigravity, "", dir, nil); !slices.Equal(l.Argv, []string{"agy", "--conversation", "mine"}) || l.SessionID != "mine" {
		t.Errorf("newest here: %+v", l)
	}
}

// A tab's conversation id is reported by the agent, so it is never put on
// the command line where the CLI would read it as a flag.
func TestResumeRefusesAFlagForAnID(t *testing.T) {
	for _, id := range []string{"--dangerously-skip-permissions", "-x", "a b", "a\nb", "--yolo"} {
		l := Resume(nil, Claude, id, t.TempDir(), nil)
		if !slices.Equal(l.Argv, []string{"claude", "--continue"}) || l.SessionID != "" {
			t.Errorf("Resume(%q, nil) = %v", id, l.Argv)
		}
	}
	for _, id := range []string{"0b0a3f7e-1c2d-4e5f-8a9b-0c1d2e3f4a5b", "ses_4f2a91", "q-1"} {
		if !ValidSessionID(id) {
			t.Errorf("%q refused", id)
		}
	}
}

// A custom agent carries on with the resume command its integration
// reported, inside the wrapper of its command, and only when the command
// is the agent's: never a program of the agent's choosing.
func TestResumeReported(t *testing.T) {
	specs := map[Kind]Spec{"my-agent": {Command: []string{"sandbox", "--", "/opt/bin/my-agent"},
		Args: []string{"--quiet"}, ResumeArgs: []string{"--continue"}}}
	l, ok := ResumeReported(specs, "my-agent", []string{"my-agent", "--resume", "s-1"})
	if want := []string{"sandbox", "--", "/opt/bin/my-agent", "--quiet", "--resume", "s-1"}; !ok || !slices.Equal(l.Argv, want) {
		t.Errorf("reported: %v %v", ok, l.Argv)
	}
	for _, argv := range [][]string{nil, {"sh", "-c", "x"}, {"/opt/bin/my-agent", "--resume", "s-1"}} {
		if _, ok := ResumeReported(specs, "my-agent", argv); ok {
			t.Errorf("%q taken", argv)
		}
	}
	if _, ok := ResumeReported(nil, Claude, []string{"claude", "--resume", "x"}); ok {
		t.Error("taken for claude, which nerdr resumes itself")
	}
}

// TestCodexNoDaemon: codex is run with --no-daemon when it has the flag,
// so its commands get the tab's environment, and without it when not.
func TestCodexNoDaemon(t *testing.T) {
	dir := t.TempDir()
	write := func(name, help string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+help+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	newer := write("codex-new", "      --no-daemon   Run without the shared background server")
	older := write("codex-old", "      --last")

	specs := map[Kind]Spec{Codex: {Command: []string{newer}}}
	if l := NewSession(specs, Codex); !slices.Equal(l.Argv, []string{newer, "--no-daemon"}) {
		t.Fatalf("new: %q", l.Argv)
	}
	if l := Resume(specs, Codex, "abc", "/w", nil); !slices.Equal(l.Argv, []string{newer, "resume", "--no-daemon", "abc"}) {
		t.Fatalf("resume: %q", l.Argv)
	}
	specs = map[Kind]Spec{Codex: {Command: []string{older}}}
	if l := NewSession(specs, Codex); !slices.Equal(l.Argv, []string{older}) {
		t.Fatalf("an older codex: %q", l.Argv)
	}
}

// A section that only adds arguments or variables keeps the agent's own
// command; the arguments go before nerdr's.
func TestSectionWithoutCommand(t *testing.T) {
	specs := map[Kind]Spec{Claude: {Args: []string{"--verbose", "--model", "opus"}, Env: map[string]string{"B": "2", "A": "1"}}}
	l := NewSession(specs, Claude)
	if !slices.Equal(l.Argv[:4], []string{"claude", "--verbose", "--model", "opus"}) || l.Argv[4] != "--session-id" {
		t.Errorf("new: %+v", l)
	}
	if !slices.Equal(l.Env, []string{"A=1", "B=2"}) {
		t.Errorf("env: %q", l.Env)
	}
	if l := Resume(specs, Claude, "abc", "/w", nil); !slices.Equal(l.Argv, []string{"claude", "--verbose", "--model", "opus", "--resume", "abc"}) || len(l.Env) != 2 {
		t.Errorf("resume: %+v", l)
	}
	// ConfigModel is opencode's alone.
	specs = map[Kind]Spec{Codex: {ConfigModel: "gpt-5"}}
	if l := NewSession(specs, Codex); slices.Contains(l.Argv, "gpt-5") || len(l.Env) != 0 {
		t.Errorf("codex: %+v", l)
	}
}

// An opencode that can run its own server always does, with no model
// too: only there does nerdr's plugin get the tab's environment.
func TestOpenCodeRunsItsOwnServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode")
	writeFile(t, path, "#!/bin/sh\necho '  --standalone            Run with a private server instead of the background service'\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	specs := map[Kind]Spec{OpenCode: {Command: []string{path}}}
	for _, l := range []Launch{NewSession(specs, OpenCode), Resume(specs, OpenCode, "ses_abc", "/w", nil)} {
		if !slices.Contains(l.Argv, "--standalone") || len(l.Env) != 0 {
			t.Errorf("opencode without a model: %q %q", l.Argv, l.Env)
		}
	}
}

// opencode 2 dropped --model, so nerdr passes its model through the
// OPENCODE_CONFIG_CONTENT variable, and runs its own server (--standalone),
// since only that server reads the variable.
func TestOpenCodeModelGoesInTheEnvironment(t *testing.T) {
	specs := map[Kind]Spec{OpenCode: {ConfigModel: "anthropic/claude-sonnet-4-5", Env: map[string]string{"B": "2"}}}
	for _, l := range []Launch{NewSession(specs, OpenCode), Resume(specs, OpenCode, "abc", "/w", nil)} {
		if !slices.Contains(l.Argv, "--standalone") {
			t.Errorf("opencode with a model runs its own server: %q", l.Argv)
		}
		if slices.Contains(l.Argv, "--model") {
			t.Errorf("opencode has no --model: %q", l.Argv)
		}
		want := `OPENCODE_CONFIG_CONTENT={"model":"anthropic/claude-sonnet-4-5"}`
		if !slices.Contains(l.Env, want) {
			t.Errorf("model is not in the environment: %q", l.Env)
		}
	}
	// With no model configured, nothing is added to the environment, and
	// --standalone only for an opencode that has it.
	bin := t.TempDir()
	fake := func(name, help string) []string {
		path := filepath.Join(bin, name)
		writeFile(t, path, "#!/bin/sh\necho '"+help+"'\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return []string{path}
	}
	older := fake("opencode-1", "--model  model to use")
	if l := NewSession(map[Kind]Spec{OpenCode: {Command: older, Env: map[string]string{"B": "2"}}}, OpenCode); !slices.Equal(l.Env, []string{"B=2"}) || slices.Contains(l.Argv, "--standalone") {
		t.Errorf("env/argv without a model: %q %q", l.Env, l.Argv)
	}
	// The model is merged over an OPENCODE_CONFIG_CONTENT the config already
	// has, so its other settings are kept.
	specs = map[Kind]Spec{OpenCode: {ConfigModel: "anthropic/claude-sonnet-4-5", Env: map[string]string{"OPENCODE_CONFIG_CONTENT": `{"share":"manual"}`}}}
	if l := NewSession(specs, OpenCode); !slices.Contains(l.Env, `OPENCODE_CONFIG_CONTENT={"model":"anthropic/claude-sonnet-4-5","share":"manual"}`) {
		t.Errorf("model was not merged into the configured value: %q", l.Env)
	}
	// And over one inherited from nerdr's own environment.
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"share":"auto"}`)
	if l := NewSession(map[Kind]Spec{OpenCode: {ConfigModel: "anthropic/claude-sonnet-4-5"}}, OpenCode); !slices.Contains(l.Env, `OPENCODE_CONFIG_CONTENT={"model":"anthropic/claude-sonnet-4-5","share":"auto"}`) {
		t.Errorf("model was not merged into the inherited value: %q", l.Env)
	}
}

// A reviewer's conversation is had in the worktree beside the tab's own,
// and newer: the tab must not carry it on as its own, nor fall back to
// claude's --continue, which would pick it up.
func TestClaudePassesOverReviewersConversations(t *testing.T) {
	cfg, dir := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	review := map[string]bool{"review": true}
	writeFile(t, filepath.Join(claudeProject(dir), "review.jsonl"), `{"type":"user","message":{"content":"nerdr: you (claude) are reviewing"}}`)
	if HasConversation(nil, Claude, dir, review) {
		t.Error("only a reviewer's conversation, but one to carry on")
	}
	if l := Resume(nil, Claude, "", dir, review); !l.New || slices.Contains(l.Argv, "--continue") || slices.Contains(l.Argv, "review") {
		t.Errorf("a reviewer's conversation is all there is: %+v", l)
	}
	writeFile(t, filepath.Join(claudeProject(dir), "own.jsonl"), `{"type":"user","message":{"content":"hi"}}`)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(claudeProject(dir), "own.jsonl"), past, past); err != nil {
		t.Fatal(err)
	}
	if l := Resume(nil, Claude, "", dir, review); l.New || l.SessionID != "own" {
		t.Errorf("the older conversation is the tab's: %+v", l)
	}
	// Without reviewers, the newest is carried on, as before.
	if l := Resume(nil, Claude, "", dir, nil); l.SessionID != "review" {
		t.Errorf("no reviewers: %+v", l)
	}
}

// Copilot keeps a session for every start: one where nothing was said is
// passed over for the one before it, even when the tab remembers it.
func TestCopilotResumesTheSessionWithSomethingSaid(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if HasConversation(nil, Copilot, dir, nil) {
		t.Error("no sessions, but a conversation to carry on")
	}
	session := func(id, events string, at time.Time) {
		root := filepath.Join(home, ".copilot", "session-state", id)
		writeFile(t, filepath.Join(root, "workspace.yaml"), "id: "+id+"\ncwd: "+dir+"\n")
		writeFile(t, filepath.Join(root, "events.jsonl"), events)
		_ = os.Chtimes(filepath.Join(root, "workspace.yaml"), at, at)
	}
	session("used", `{"type":"user.message","data":{"content":"fix it"}}`, time.Now().Add(-time.Hour))
	session("empty", `{"type":"session.start","data":{}}`, time.Now())
	if !HasConversation(nil, Copilot, dir, nil) {
		t.Error("no conversation to carry on")
	}
	base := startArgs(specFor(nil, Copilot), Copilot)
	for _, id := range []string{"", "empty"} {
		if l := Resume(nil, Copilot, id, dir, nil); !slices.Equal(l.Argv, append(slices.Clone(base), "--resume=used")) || l.SessionID != "used" {
			t.Errorf("resume %q: %+v", id, l)
		}
	}
}

// Copilot is given the new conversation's id, as claude is, when its help
// knows --session-id: the tab, or the reviewer, then knows which is its
// own. An older copilot is started as before.
func TestCopilotTakesTheSessionID(t *testing.T) {
	bin := t.TempDir()
	fake := func(name, help string) Spec {
		path := filepath.Join(bin, name)
		writeFile(t, path, "#!/bin/sh\necho '"+help+"'\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return Spec{Command: []string{path}}
	}
	l := NewSession(map[Kind]Spec{Copilot: fake("new", "--session-id <id>  set the UUID")}, Copilot)
	if len(l.Argv) != 3 || l.Argv[1] != "--session-id" || l.Argv[2] != l.SessionID || !ValidSessionID(l.SessionID) {
		t.Errorf("new copilot: %+v", l)
	}
	if l := NewSession(map[Kind]Spec{Copilot: fake("old", "--resume")}, Copilot); len(l.Argv) != 1 || l.SessionID != "" {
		t.Errorf("old copilot: %+v", l)
	}
}

// Copilot's --continue takes the newest session of any folder, so with
// none of the worktree's own to carry on a new one is started instead.
func TestCopilotNeverContinuesAnotherFolders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := Resume(nil, Copilot, "", t.TempDir(), nil)
	if !l.New || slices.Contains(l.Argv, "--continue") {
		t.Errorf("nothing to carry on: %+v", l)
	}
}

// Claude's conversation in the worktree is found by the folder named after
// it, and carried on when the tab's own was never saved.
func TestClaudeCarriesOnTheWorktreesConversation(t *testing.T) {
	cfg, dir := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if HasConversation(nil, Claude, dir, nil) {
		t.Error("no conversations, but one to carry on")
	}
	if l := Resume(nil, Claude, "", dir, nil); !slices.Contains(l.Argv, "--continue") {
		t.Errorf("nothing to resume: %+v", l)
	}
	writeFile(t, filepath.Join(claudeProject(dir), "old.jsonl"), `{"type":"user","message":{"content":"hi"}}`)
	writeFile(t, filepath.Join(claudeProject(dir), "summary.jsonl"), `{"type":"summary"}`)
	if !HasConversation(nil, Claude, dir, nil) {
		t.Error("no conversation to carry on")
	}
	for _, id := range []string{"", "unsaved"} {
		if l := Resume(nil, Claude, id, dir, nil); !slices.Contains(l.Argv, "old") || l.SessionID != "old" {
			t.Errorf("resume %q: %+v", id, l)
		}
	}
	writeFile(t, filepath.Join(claudeProject(dir), "kept.jsonl"), `{"type":"user"}`)
	if l := Resume(nil, Claude, "kept", dir, nil); !slices.Contains(l.Argv, "kept") {
		t.Errorf("its own: %+v", l)
	}
}

// TestStartPrompt: copilot and codex are started with their first
// message, as nerdr cannot tell when their screens are ready to be typed
// into; the others are typed it.
func TestStartPrompt(t *testing.T) {
	if got := StartPrompt(Copilot, "review this"); !slices.Equal(got, []string{"-i", "review this"}) {
		t.Fatalf("copilot: %q", got)
	}
	if got := StartPrompt(Codex, "review"); !slices.Equal(got, []string{"--", "review"}) {
		t.Fatalf("codex: %q", got)
	}
	if got := StartPrompt(OpenCode, "review this"); !slices.Equal(got, []string{"--prompt", "review this"}) {
		t.Fatalf("opencode: %v", got)
	}
	if got := StartPrompt(Claude, "review this"); got != nil {
		t.Fatalf("claude: %q", got)
	}
	if got := StartPrompt(Codex, strings.Repeat("x", 128<<10)); got != nil {
		t.Fatalf("a prompt too long for the command line: %d arguments", len(got))
	}
}

// TestClaimCodex: the codex conversation claimed for an agent is its own
// among those started in its worktree since it started: the one with what
// it was told in it, none while two could be, never another agent's.
func TestClaimCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "10", "17")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, cwd, prompt string, at time.Time) {
		f := filepath.Join(day, "rollout-2026-10-17T10-00-00-"+id+".jsonl")
		lines := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}` + "\n" +
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}}` + "\n"
		if err := os.WriteFile(f, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(f, at, at)
	}
	now := time.Now()
	old := "11111111-1111-1111-1111-111111111111"
	mine := "22222222-2222-2222-2222-222222222222"
	other := "33333333-3333-3333-3333-333333333333"
	elsewhere := "44444444-4444-4444-4444-444444444444"
	write(old, dir, "old work", now.Add(-time.Hour))
	write(mine, dir, "fix the parser", now.Add(-time.Minute))
	write(other, dir, "write the docs", now)
	write(elsewhere, t.TempDir(), "fix the parser", now.Add(time.Second))
	since := now.Add(-10 * time.Minute)

	if got := Claim(nil, Codex, dir, nil, since, []string{"fix  the\nparser"}); got != mine {
		t.Fatalf("by what it was told: claimed %q, want %q", got, mine)
	}
	if got := Claim(nil, Codex, dir, nil, since, nil); got != "" {
		t.Fatalf("two started since, nothing to tell them apart: claimed %q", got)
	}
	if got := Claim(nil, Codex, dir, map[string]bool{other: true}, since, nil); got != mine {
		t.Fatalf("the other agent's passed over: claimed %q, want %q", got, mine)
	}
	write(other, dir, "fix the parser", now)
	if got := Claim(nil, Codex, dir, nil, since, []string{"fix the parser"}); got != "" {
		t.Fatalf("both were told the same: claimed %q", got)
	}
	if got := Claim(nil, Codex, dir, map[string]bool{other: true, mine: true}, since, nil); got != "" {
		t.Fatalf("one from before the agent started is not its own: %q", got)
	}
}

// TestOpenCodeID: the ids nerdr gives opencode have its form.
func TestOpenCodeID(t *testing.T) {
	id := openCodeID()
	if len(id) != 30 || !strings.HasPrefix(id, "ses_") || !ValidSessionID(id) || id == openCodeID() {
		t.Fatalf("id %q", id)
	}
}

// TestWithModel: a model given replaces the config's model flag, in the
// agent's own spelling, and leaves the other arguments; opencode's goes in
// its config instead.
func TestWithModel(t *testing.T) {
	specs := map[Kind]Spec{Claude: {Command: []string{"claude"}, Args: []string{"--model", "opus", "--permission-mode", "acceptEdits"}}}
	got := WithModel(specs, Claude, "haiku")[Claude].Args
	if strings.Join(got, " ") != "--permission-mode acceptEdits --model haiku" {
		t.Fatalf("claude args %q", got)
	}
	if specs[Claude].Args[1] != "opus" {
		t.Fatal("the config's specs are left as they are")
	}
	got = WithModel(nil, Codex, "gpt-5.5")[Codex].Args
	if strings.Join(got, " ") != "--model gpt-5.5" {
		t.Fatalf("codex args %q", got)
	}
	got = WithModel(map[Kind]Spec{Codex: {Args: []string{"-m", "o3", "--model=x"}}}, Codex, "gpt-5.5")[Codex].Args
	if strings.Join(got, " ") != "--model gpt-5.5" {
		t.Fatalf("codex args with -m and --model=: %q", got)
	}
	if s := WithModel(nil, OpenCode, "anthropic/claude-sonnet-5-5")[OpenCode]; s.ConfigModel != "anthropic/claude-sonnet-5-5" || len(s.Args) != 0 {
		t.Fatalf("opencode %+v", s)
	}
	if out := WithModel(specs, Claude, ""); out[Claude].Args[1] != "opus" {
		t.Fatal("no model, the config's")
	}
	if TakesModel(Kind("other")) || !TakesModel(OpenCode) || !TakesModel(Antigravity) {
		t.Fatal("TakesModel")
	}
}
