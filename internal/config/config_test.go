package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"

	"github.com/BurntSushi/toml"
)

func TestPrefixByte(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   byte
		ok     bool
		key    string
	}{
		{"ctrl+q", 0x11, true, "ctrl+q"},
		{"Ctrl+A", 0x01, true, "ctrl+a"},
		{"ctrl+space", 0x00, true, "ctrl+@"},
		{"ctrl+]", 0x1d, true, "ctrl+]"},
		{`ctrl+\`, 0x1c, true, `ctrl+\`},
		{"ctrl+i", 0, false, ""}, // tab
		{"ctrl+m", 0, false, ""}, // enter
		{"ctrl+j", 0, false, ""}, // newline
		{"ctrl+h", 0, false, ""}, // backspace
		{"alt+q", 0, false, ""},
		{"ctrl+qq", 0, false, ""},
	} {
		c := Config{Prefix: tc.prefix}
		got, err := c.PrefixByte()
		if (err == nil) != tc.ok {
			t.Errorf("%q: err = %v, want ok=%v", tc.prefix, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("%q: byte = %#x, want %#x", tc.prefix, got, tc.want)
		}
		if tc.ok && c.PrefixKey() != tc.key {
			t.Errorf("%q: key = %q, want %q", tc.prefix, c.PrefixKey(), tc.key)
		}
	}
}

func TestNotifyOn(t *testing.T) {
	for notify, want := range map[string][2]bool{
		"":        {true, true}, // defaults to all
		"all":     {true, true},
		"blocked": {true, false},
		"done":    {false, true},
		"off":     {false, false},
		"bogus":   {true, true},
	} {
		c := defaults(Config{Notify: notify})
		if got := [2]bool{c.NotifyOn("blocked"), c.NotifyOn("done")}; got != want {
			t.Errorf("notify=%q: %v, want %v", notify, got, want)
		}
	}
}

// TestSetupKeepsTheRestOfTheFile covers the first start writing its
// answers into a config file the user already has, with [agents] tables
// after which a plain key would land in the wrong place.
func TestSetupKeepsTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = os.MkdirAll(filepath.Join(dir, "nerdr"), 0o755)
	_ = os.WriteFile(Path(), []byte("prefix = \"ctrl+b\"\n\n[agents.claude]\nargs = [\"-x\"]\n"), 0o644)

	c, err := Load()
	if err != nil || c.Install != "" || c.DataDirSet() {
		t.Fatalf("before: %+v %v", c, err)
	}
	notes := ".nerdr"
	if err := SetKeys(Edit{"", "install", Quote(InstallWorktree)}, Edit{"", "data_dir", Quote(notes)}); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Install != InstallWorktree || c.DataDir != ".nerdr" || !c.DataDirSet() ||
		c.Prefix != "ctrl+b" || len(c.Agents["claude"].Args) != 1 {
		t.Fatalf("after: %+v", c)
	}
}

func TestWorktreesDirIsInHomeUnlessSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load()
	if err != nil || c.WorktreesDirSet() || c.WorktreesDir != filepath.Join(home, "worktrees-nerdr") {
		t.Fatalf("default: %q %v", c.WorktreesDir, err)
	}
	if err := SetKey("", "worktrees_dir", Quote("~/wt")); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || !c.WorktreesDirSet() || c.WorktreesDir != filepath.Join(home, "wt") {
		t.Fatalf("set: %q %v", c.WorktreesDir, err)
	}
	if err := SetKey("", "worktrees_dir", Quote("worktrees")); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(); c.WorktreesDir != "worktrees" {
		t.Fatalf("relative: %q", c.WorktreesDir)
	}
}

func TestSetReviewRounds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	write := func(s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(Path(), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(Path())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, tc := range []struct{ name, before, after string }{
		{"no file", "", "\n[review]\nrounds = 3\n"},
		{"no table", "prefix = \"ctrl+q\"\n", "prefix = \"ctrl+q\"\n\n[review]\nrounds = 3\n"},
		{"table without rounds", "[review]\nauto = true\n\n[agents.claude]\nargs = []\n",
			"[review]\nrounds = 3\nauto = true\n\n[agents.claude]\nargs = []\n"},
		{"rounds there", "[review]\nauto = true\nrounds = 1 # once\n[x]\nrounds = 7\n",
			"[review]\nauto = true\nrounds = 3\n[x]\nrounds = 7\n"},
		{"table last, no newline", "[review]", "[review]\nrounds = 3\n"},
	} {
		os.Remove(Path())
		if tc.before != "" {
			write(tc.before)
		}
		if err := SetKey("review", "rounds", "3"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := read(); got != tc.after {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.after)
		}
		c, err := Load()
		if err != nil || c.Review.Rounds != 3 {
			t.Fatalf("%s: loads rounds %d (%v)", tc.name, c.Review.Rounds, err)
		}
	}
}

// TestSetKey covers the settings dialog writing one key: in place, at the
// top of its table, in a new table, over several lines, and removed.
func TestSetKey(t *testing.T) {
	text := "# mine\nnotify = \"done\"\n\n[agents.claude]\nargs = [\n  \"--verbose\",\n]\n# keep\n\n[review]\nauto = true\n"
	for _, tc := range []struct{ table, key, value, want string }{
		{"", "notify", `"off"`, "# mine\nnotify = \"off\"\n\n[agents.claude]"},
		{"", "notify", "", "# mine\n\n[agents.claude]"},
		{"", "mouse", "false", "mouse = false\n# mine\nnotify"},
		{"agents.claude", "args", `["--a"]`, "[agents.claude]\nargs = [\"--a\"]\n# keep\n"},
		{"agents.claude", "model", `"opus"`, "[agents.claude]\nmodel = \"opus\"\nargs = [\n"},
		{"agents.claude", "args", "", "[agents.claude]\n# keep\n"},
		{"agents.codex", "env", `{ A = "1" }`, "auto = true\n\n[agents.codex]\nenv = { A = \"1\" }\n"},
		{"agents.codex", "env", "", text},
		{"review", "auto", "false", "[review]\nauto = false\n"},
	} {
		got := setKey(text, tc.table, tc.key, tc.value)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s.%s = %s:\n%s", tc.table, tc.key, tc.value, got)
		}
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SetKey("agents.opencode", "config_model", Quote("anthropic/claude-opus-5-5")); err != nil {
		t.Fatal(err)
	}
	if err := SetKey("agents.claude", "env", Table(map[string]string{"MAX_THINKING_TOKENS": "1", "A.B": "x"})); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.Agents["opencode"].ConfigModel != "anthropic/claude-opus-5-5" || c.Agents["claude"].Env["A.B"] != "x" {
		t.Fatalf("%+v %v", c.Agents, err)
	}
	if err := SetKey("", "max_tabs", `"many"`); err == nil {
		t.Error("a value that does not read was written")
	}
}

// TestSetKeyFollowsValues: a value over several lines, or with brackets
// in its strings and comments, is replaced whole, and a line starting
// with [ inside an array is no table.
func TestSetKeyFollowsValues(t *testing.T) {
	for _, tc := range []struct{ text, table, key, value string }{
		{"[agents.claude]\nargs = [\n  \"--a\",\n  \"[b]\",\n]\nsandboxed = true\n", "agents.claude", "args", `["--z"]`},
		{"[agents.claude]\nargs = [\"--p\", \"use [x] here\", \"--y\"] # a ] here\nsandboxed = true\n", "agents.claude", "args", `["--z"]`},
		{"repo_dirs = [\n  \"a\",\n  [\"b\"],\n]\nlayout = \"small\"\n[review]\nauto = true\n", "", "repo_dirs", `["c"]`},
		{"[agents.claude]\nenv = { A = \"}\", B = \"2\" }\nsandboxed = true\n", "agents.claude", "env", `{ C = "3" }`},
	} {
		got := setKey(tc.text, tc.table, tc.key, tc.value)
		var c map[string]any
		if _, err := toml.Decode(got, &c); err != nil {
			t.Errorf("%q:\n%s\n%v", tc.text, got, err)
			continue
		}
		if !strings.Contains(got, tc.key+" = "+tc.value+"\n") || strings.Count(got, tc.key+" =") != 1 {
			t.Errorf("%q: not set once:\n%s", tc.text, got)
		}
		if strings.Contains(tc.text, "sandboxed") && !strings.Contains(got, "sandboxed = true") {
			t.Errorf("%q: the key after it went:\n%s", tc.text, got)
		}
	}
	// Multi-line strings end at their last run of quotes, and a quote
	// escaped in one does not end it: the key after is kept.
	for _, text := range []string{
		`worktrees_dir = """/tmp/quoted""""` + "\n" + `prefix = "ctrl+a"` + "\n",
		`worktrees_dir = """a\"""b` + "\n" + `"""` + "\n" + `prefix = "ctrl+a"` + "\n",
		`worktrees_dir = '''it'''''` + "\n" + `prefix = "ctrl+a"` + "\n",
		`worktrees_dir = "a \" b" # """` + "\n" + `prefix = "ctrl+a"` + "\n",
	} {
		var c map[string]any
		if _, err := toml.Decode(text, &c); err != nil {
			t.Fatalf("%q is not TOML: %v", text, err)
		}
		got := setKeys(text, []Edit{{"", "worktrees_dir", `"/tmp/new"`}})
		if got != "worktrees_dir = \"/tmp/new\"\nprefix = \"ctrl+a\"\n" {
			t.Errorf("%q:\n%s", text, got)
		}
	}

	got := setKeys("repo_dirs = [\n  \"a\",\n]\nlayout = \"small\"\n", []Edit{{"", "repo_dirs", `["c"]`}})
	if got != "repo_dirs = [\"c\"]\nlayout = \"small\"\n" {
		t.Errorf("root settings:\n%s", got)
	}
}

func TestFPS(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, DefaultFPS}, {-5, DefaultFPS}, {30, 30}, {120, 120}, {500, 120}} {
		if got := defaults(Config{FPS: c.in}).FPS; got != c.want {
			t.Errorf("fps = %d: got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestReviewWhen(t *testing.T) {
	for _, c := range []struct{ in, want string }{{"", WhenReady}, {"ready", WhenReady}, {"turn", WhenTurn}, {"sometimes", WhenReady}} {
		if got := defaults(Config{Review: Review{When: c.in}}).Review.When; got != c.want {
			t.Errorf("when = %q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReviewModelNames(t *testing.T) {
	for _, c := range []struct{ in, want string }{{"", NamesByNerdr}, {"nerdr", NamesByNerdr}, {"agent", NamesByAgent}, {"me", NamesByNerdr}} {
		if got := defaults(Config{Review: Review{ModelNames: c.in}}).Review.ModelNames; got != c.want {
			t.Errorf("model_names = %q: got %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWrappers covers a wrapper script kept in the config: an agent's
// command and check_command that start with its name run it, with the
// rest of the command after it, and other commands are left alone.
func TestWrappers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = os.MkdirAll(filepath.Join(dir, "nerdr"), 0o755)
	write := func(s string) {
		if err := os.WriteFile(Path(), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`
[wrappers.box]
script = '''
exec "$@"
'''

[wrappers.zbox]
shell = "/bin/sh"
script = 'exec "$@"'

[agents.copilot]
command = ["box", "nav-pilot", "--"]
check_command = ["zbox", "cplt", "exec", "--"]

[agents.claude]
command = ["my-sandbox", "box", "claude"]
`)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cp := c.Agents["copilot"]
	if want := []string{"bash", "-c", "exec \"$@\"\n", "box", "nav-pilot", "--"}; !slices.Equal(cp.Command, want) {
		t.Fatalf("command %q, want %q", cp.Command, want)
	}
	if want := []string{"/bin/sh", "-c", `exec "$@"`, "zbox", "cplt", "exec", "--"}; !slices.Equal(cp.CheckCommand, want) {
		t.Fatalf("check_command %q, want %q", cp.CheckCommand, want)
	}
	if cl := c.Agents["claude"]; !slices.Equal(cl.Command, []string{"my-sandbox", "box", "claude"}) || cl.CheckCommand != nil {
		t.Fatalf("claude %+v", cl)
	}

	for _, bad := range []string{
		"[wrappers.box]\nscript = \"  \"\n",
		"[wrappers.\"a b\"]\nscript = \"x\"\n",
		"[wrappers.box]\nscript = \"x\"\nshell = \"sh -e\"\n",
	} {
		write(bad)
		if _, err := Load(); err == nil {
			t.Errorf("no error for %q", bad)
		}
	}
}

// TestUnwrapped covers telling a wrapper's words from the rest of a
// command: only words WrapSpec put there count as a wrapper's.
func TestUnwrapped(t *testing.T) {
	c := Config{Wrappers: map[string]Wrapper{"box": {Script: "x"}}}
	s := c.WrapSpec(agents.Spec{Command: []string{"box", "--", "codex"}})
	if pre, rest := s.Unwrapped(); !slices.Equal(pre, []string{"bash", "-c", "x", "box"}) || !slices.Equal(rest, []string{"--", "codex"}) {
		t.Fatalf("%q %q", pre, rest)
	}
	plain := c.WrapSpec(agents.Spec{Command: []string{"bash", "-c", "x", "box", "codex"}})
	if pre, rest := plain.Unwrapped(); pre != nil || len(rest) != 5 {
		t.Errorf("a command that only looks wrapped: %q %q", pre, rest)
	}
}

// TestResumeThroughAWrapper covers an agent nerdr does not know carrying
// on a conversation its integration reported, with a wrapper from the
// config in its command: the agent's program counts, and the wrapper's
// shell is no program of the agent's.
func TestResumeThroughAWrapper(t *testing.T) {
	c := Config{Wrappers: map[string]Wrapper{"box": {Script: `exec "$@"`}}}
	specs := map[agents.Kind]agents.Spec{"my-agent": c.WrapSpec(agents.Spec{Command: []string{"box", "my-agent"}})}
	l, ok := agents.ResumeReported(specs, "my-agent", []string{"my-agent", "--resume", "x"})
	if want := []string{"bash", "-c", `exec "$@"`, "box", "my-agent", "--resume", "x"}; !ok || !slices.Equal(l.Argv, want) {
		t.Fatalf("the agent's own: %v %q", ok, l.Argv)
	}
	for _, other := range [][]string{{"bash", "--resume", "x"}, {"box", "--resume", "x"}} {
		if _, ok := agents.ResumeReported(specs, "my-agent", other); ok {
			t.Errorf("%q taken as the agent's", other)
		}
	}
}

// TestAutoDefaults: every [auto] event is off unless set to ask or auto,
// and max_tries is 3 unless set, within bounds.
func TestAutoDefaults(t *testing.T) {
	var c Config
	if _, err := toml.Decode("[auto]\nci_failed = \"auto\"\nreview_comments = \"bogus\"\nmax_tries = 50\n", &c); err != nil {
		t.Fatal(err)
	}
	c = defaults(c)
	if c.Auto.CIFailed != AutoSend || c.Auto.ReviewComments != AutoOff || c.Auto.Conflicts != AutoOff || c.Auto.MaxTries != 20 || c.Auto.Wake {
		t.Fatalf("auto = %+v", c.Auto)
	}
	if d := defaults(Config{}).Auto; d.Mode(EventCIFailed) != AutoOff || d.MaxTries != DefaultAutoTries {
		t.Fatalf("default auto = %+v", d)
	}
}

func TestPortDefaults(t *testing.T) {
	c := defaults(Config{})
	if c.PortBase != DefaultPortBase || *c.PortsPerTab != DefaultPortsPerTab {
		t.Fatalf("port_base %d, ports_per_tab %d", c.PortBase, *c.PortsPerTab)
	}
	off, many := 0, 5000
	if c := defaults(Config{PortBase: 80, PortsPerTab: &off}); c.PortBase != 1024 || *c.PortsPerTab != 0 {
		t.Fatalf("port_base %d, ports_per_tab %d", c.PortBase, *c.PortsPerTab)
	}
	if c := defaults(Config{PortsPerTab: &many}); *c.PortsPerTab != 1000 {
		t.Fatalf("ports_per_tab %d", *c.PortsPerTab)
	}
}
