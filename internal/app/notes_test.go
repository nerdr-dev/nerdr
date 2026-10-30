package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// codex is told what claude is, as developer instructions after those it
// would have had, written so that codex reads them back as they were.
func TestCodexIsToldTheNotes(t *testing.T) {
	m := testModel(t)
	m.cfg.Install = config.InstallHome
	tb := m.cur()
	tb.Agent = agents.Codex
	tb.Dir = t.TempDir()

	var own string
	var asked struct {
		argv, env, config []string
		dir               string
	}
	codexInstructions = func(argv, env, config []string, dir string) (string, error) {
		asked.argv, asked.env, asked.config, asked.dir = argv, env, config, dir
		return own, nil
	}
	t.Cleanup(func() { codexInstructions = hooks.CodexDeveloperInstructions })

	read := func(args []string) string {
		t.Helper()
		if len(args) != 2 || args[0] != "-c" {
			t.Fatalf("args: %q", args)
		}
		var c struct {
			DeveloperInstructions string `toml:"developer_instructions"`
		}
		if _, err := toml.Decode(args[1], &c); err != nil {
			t.Fatalf("codex could not read %q: %v", args[1], err)
		}
		return c.DeveloperInstructions
	}
	if got := read(m.codexNote(tb, []string{"codex"}, nil)); got != worktreeNote {
		t.Fatalf("instructions %q", got)
	}

	own = "Answer in \"Nynorsk\".\nKeep it short\\brief.\x7f\x01"
	m.cfg.Review = config.Review{Auto: true, When: config.WhenReady}
	argv := []string{"codex", "--config=model=\"o3\"", "-cdeveloper_instructions=\"mine\"", "--no-daemon", "--", "-c not=this"}
	if got, want := read(m.codexNote(tb, argv, []string{"CODEX_HOME=/x"})), own+"\n\n"+worktreeNote+"\n\n"+readyNote; got != want {
		t.Fatalf("instructions %q, want %q", got, want)
	}
	if !slices.Equal(asked.argv, []string{"codex"}) || !slices.Equal(asked.env, []string{"CODEX_HOME=/x"}) || asked.dir != tb.Dir ||
		!slices.Equal(asked.config, []string{`model="o3"`, `developer_instructions="mine"`}) {
		t.Fatalf("codex asked as %+v", asked)
	}

	for _, p := range [][]string{{"-p", "work"}, {"--profile=work"}, {"-pwork"}} {
		if args := m.codexNote(tb, append([]string{"codex"}, p...), nil); args != nil {
			t.Fatalf("%q: a profile's own are not read, so none should be added: %q", p, args)
		}
	}
	codexInstructions = func(argv, env, config []string, dir string) (string, error) {
		return "", errors.New("no codex")
	}
	if args := m.codexNote(tb, []string{"codex"}, nil); args != nil {
		t.Fatalf("not knowing the user's, none should be added: %q", args)
	}
	m.cfg.EnabledSkills = []string{"nerdr-explain"}
	if args := m.codexNote(tb, []string{"codex"}, nil); args != nil {
		t.Fatalf("no skill to point to: %q", args)
	}
}

// copilot finds the notes in a folder of nerdr's, named beside the
// user's own instruction folders.
func TestCopilotIsToldTheNotes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(copilotInstructionsVar, "/inherited")
	m := testModel(t)
	m.cfg.Install = config.InstallHome
	tb := m.cur()

	dirOf := func(env []string, want string) string {
		t.Helper()
		var vals []string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, copilotInstructionsVar+"="); ok {
				vals = append(vals, v)
			}
		}
		if len(vals) != 1 || !strings.HasPrefix(vals[0], want+",") {
			t.Fatalf("%s: %q, want it to start with %q", copilotInstructionsVar, vals, want)
		}
		return strings.TrimPrefix(vals[0], want+",")
	}
	dir := dirOf(m.copilotNote(tb, []string{"FOO=1"}), "/inherited")
	b, err := os.ReadFile(filepath.Join(dir, ".github", "instructions", "nerdr.instructions.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "---\napplyTo: \"**\"\n---\n") || !strings.Contains(string(b), worktreeNote) {
		t.Fatalf("instructions file:\n%s", b)
	}

	env := m.copilotNote(tb, []string{"FOO=1", copilotInstructionsVar + "=/mine"})
	if dirOf(env, "/mine") != dir || !slices.Contains(env, "FOO=1") {
		t.Fatalf("env %q", env)
	}

	m.cfg.Review = config.Review{Auto: true, When: config.WhenReady}
	if other := dirOf(m.copilotNote(tb, nil), "/inherited"); other == dir {
		t.Fatal("another note should have a folder of its own")
	}

	m.cfg.EnabledSkills = []string{"nerdr-explain"}
	if env := m.copilotNote(tb, []string{"FOO=1"}); !slices.Equal(env, []string{"FOO=1"}) {
		t.Fatalf("no skill to point to: %q", env)
	}
}

// A codex nerdr starts has the notes on its command line, before the
// prompt.
func TestCodexStartsWithTheNotes(t *testing.T) {
	codexInstructions = func(argv, env, config []string, dir string) (string, error) { return "", nil }
	t.Cleanup(func() { codexInstructions = hooks.CodexDeveloperInstructions })
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	cmd := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + started + "'\nexec cat\n"
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := paneModel(t)
	m.cfg.Install = config.InstallHome
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{cmd}}}
	m.newAgent(m.cur(), agents.Codex)
	var args string
	for i := 0; i < 100 && args == ""; i++ {
		b, _ := os.ReadFile(started)
		args = string(b)
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(args, "-c\ndeveloper_instructions=") || !strings.Contains(args, "nerdr-spinoff") {
		t.Fatalf("codex started with %q", args)
	}
}

// opencode is given the notes in its environment, for nerdr's plugin;
// other agents are not.
func TestOpenCodeIsToldTheNotes(t *testing.T) {
	m := testModel(t)
	m.cfg.Install = config.InstallHome
	tb := m.cur()
	tb.Agent = agents.OpenCode
	if !slices.Contains(m.agentEnv(tb), "NERDR_OPENCODE_NOTE="+worktreeNote) {
		t.Fatalf("env %q", m.agentEnv(tb))
	}
	tb.Agent = agents.Claude
	for _, kv := range m.agentEnv(tb) {
		if strings.HasPrefix(kv, "NERDR_OPENCODE_NOTE=") {
			t.Fatalf("claude told %q", kv)
		}
	}
	tb.Agent = agents.OpenCode
	m.cfg.EnabledSkills = []string{"nerdr-explain"}
	for _, kv := range m.agentEnv(tb) {
		if strings.HasPrefix(kv, "NERDR_OPENCODE_NOTE=") {
			t.Fatalf("no skill to point to: %q", kv)
		}
	}
}

// Antigravity reads the notes from a folder of nerdr's added to its
// workspace, one for each worktree, put right at each start.
func TestAgyIsToldTheNotes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel(t)
	m.cfg.Install = config.InstallHome
	tb := m.cur()
	tb.Dir = t.TempDir()

	args := m.agyNote(tb)
	if len(args) != 2 || args[0] != "--add-dir" || strings.HasPrefix(args[1], tb.Dir) {
		t.Fatalf("args %q", args)
	}
	file := filepath.Join(args[1], "AGENTS.md")
	if b, err := os.ReadFile(file); err != nil || string(b) != worktreeNote+"\n" {
		t.Fatalf("AGENTS.md %q, %v", b, err)
	}
	if err := os.WriteFile(file, []byte("changed by an agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := m.agyNote(tb); !slices.Equal(again, args) {
		t.Fatalf("the same tab moved: %q", again)
	}
	if b, _ := os.ReadFile(file); string(b) != worktreeNote+"\n" {
		t.Fatalf("not put right: %q", b)
	}

	other := &tab{Tab: tb.Tab}
	other.Dir = t.TempDir()
	if o := m.agyNote(other); slices.Equal(o, args) {
		t.Fatal("another worktree should have a folder of its own")
	}

	m.cfg.EnabledSkills = []string{"nerdr-explain"}
	if args := m.agyNote(tb); args != nil {
		t.Fatalf("no skill to point to: %q", args)
	}
}

// An agent that may write in its note's folder cannot have nerdr write
// elsewhere through it: links put in place of the note, or of the names
// an older nerdr wrote it through, are not followed.
func TestNoteFollowsNoLinks(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "AGENTS.md.tmp"} {
		if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeNote(base, filepath.Join("n", "AGENTS.md"), []byte("the note\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep me" {
		t.Fatalf("written through a link: %q", b)
	}
	info, err := os.Lstat(filepath.Join(dir, "AGENTS.md"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the note should be a file of its own: %v, %v", info, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); string(b) != "the note\n" {
		t.Fatalf("note %q", b)
	}

	// The folder itself turned into a link: no note rather than one elsewhere.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), dir); err != nil {
		t.Fatal(err)
	}
	if err := writeNote(base, filepath.Join("n", "AGENTS.md"), []byte("the note\n")); err == nil {
		t.Fatal("wrote through a linked folder")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(outside), "AGENTS.md")); err == nil {
		t.Fatal("the note landed outside")
	}
}
