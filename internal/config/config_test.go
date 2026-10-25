package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// TestSaveSetupKeepsTheRestOfTheFile covers the first start writing its
// answers into a config file the user already has, with [agents] tables
// after which a plain key would land in the wrong place.
func TestSaveSetupKeepsTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = os.MkdirAll(filepath.Join(dir, "nerdr"), 0o755)
	_ = os.WriteFile(Path(), []byte("prefix = \"ctrl+b\"\n\n[agents.claude]\nargs = [\"-x\"]\n"), 0o644)

	c, err := Load()
	if err != nil || c.Install != "" || c.DataDirSet() {
		t.Fatalf("before: %+v %v", c, err)
	}
	notes := ".nerdr"
	if err := SaveSetup(InstallWorktree, &notes); err != nil {
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
	if err := SaveWorktreesDir("~/wt"); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || !c.WorktreesDirSet() || c.WorktreesDir != filepath.Join(home, "wt") {
		t.Fatalf("set: %q %v", c.WorktreesDir, err)
	}
	if err := SaveWorktreesDir("worktrees"); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(); c.WorktreesDir != "worktrees" {
		t.Fatalf("relative: %q", c.WorktreesDir)
	}
}

func TestSaveReviewRounds(t *testing.T) {
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
		if err := SaveReviewRounds(3); err != nil {
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
		got := rootSettings(text, map[string]string{"worktrees_dir": `"/tmp/new"`})
		if got != "worktrees_dir = \"/tmp/new\"\nprefix = \"ctrl+a\"\n" {
			t.Errorf("%q:\n%s", text, got)
		}
	}

	got := rootSettings("repo_dirs = [\n  \"a\",\n]\nlayout = \"small\"\n", map[string]string{"repo_dirs": `["c"]`})
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
