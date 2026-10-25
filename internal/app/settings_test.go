package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// pickSetting moves the dialog's cursor to the setting key of table.
func pickSetting(t *testing.T, d *settings, table, key string) {
	t.Helper()
	for i, r := range d.rows {
		if r.table == table && r.key == key {
			d.cursor = i
			return
		}
	}
	t.Fatalf("no setting %s.%s", table, key)
}

func typeText(m *Model, s string) {
	for _, r := range s {
		m.dialog.key(m, keyMsg(string(r)))
	}
}

// TestSettingsDialog covers changing settings in the prefix + p dialog:
// each is written to the config file at once, the rest of it kept, and
// holds in nerdr from then on, but for those read only at its start.
func TestSettingsDialog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(config.Path()[:len(config.Path())-len("/config.toml")], 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("# my settings\nnotify = \"done\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := testModel(t)
	m.run("s", keyMsg("s"))
	d, ok := m.dialog.(*settings)
	if !ok {
		t.Fatalf("p opens %T", m.dialog)
	}

	// claude's model is one of its args.
	pickSetting(t, d, "agents.claude", "args")
	m.dialog.key(m, keyMsg("enter"))
	typeText(m, "--model claude-opus-5-5")
	m.dialog.key(m, keyMsg("enter"))
	l := agents.NewSession(m.cfg.Agents, agents.Claude)
	if !strings.Contains(strings.Join(l.Argv, " "), "claude --model claude-opus-5-5 --session-id") {
		t.Fatalf("claude starts as %q (%s)", l.Argv, d.err)
	}
	for _, r := range d.rows {
		if r.table == "agents.claude" && r.key == "model" {
			t.Error("claude has a model row")
		}
	}

	// opencode's model goes in its config.
	pickSetting(t, d, "agents.opencode", "config_model")
	m.dialog.key(m, keyMsg("enter"))
	typeText(m, "anthropic/claude-opus-5-5")
	m.dialog.key(m, keyMsg("enter"))
	if got := m.cfg.Agents[agents.OpenCode].ConfigModel; got != "anthropic/claude-opus-5-5" {
		t.Fatalf("config_model %q", got)
	}

	// variables.
	pickSetting(t, d, "agents.claude", "env")
	m.dialog.key(m, keyMsg("enter"))
	typeText(m, `A=1 B="two words"`)
	m.dialog.key(m, keyMsg("enter"))
	if env := m.cfg.Agents[agents.Claude].Env; env["A"] != "1" || env["B"] != "two words" {
		t.Fatalf("env %v (%s)", env, d.err)
	}
	if d.text(d.rows[d.cursor]) != `A=1 "B=two words"` {
		t.Fatalf("env reads back as %q", d.text(d.rows[d.cursor]))
	}

	// a choice, and back to the default.
	pickSetting(t, d, "", "notify")
	m.dialog.key(m, keyMsg("enter"))
	if m.cfg.Notify != "off" {
		t.Fatalf("notify %q", m.cfg.Notify)
	}
	m.dialog.key(m, keyMsg("d"))
	if m.cfg.Notify != "all" {
		t.Fatalf("notify by default %q", m.cfg.Notify)
	}

	// a number out of range is refused.
	pickSetting(t, d, "", "max_tabs")
	m.dialog.key(m, keyMsg("enter"))
	typeText(m, "x")
	m.dialog.key(m, keyMsg("enter"))
	if d.err == "" {
		t.Fatal("max_tabs = x was taken")
	}

	// the prefix waits for the next start.
	pickSetting(t, d, "", "prefix")
	m.dialog.key(m, keyMsg("enter"))
	for range 10 {
		m.dialog.key(m, keyMsg("backspace"))
	}
	typeText(m, "ctrl+a")
	m.dialog.key(m, keyMsg("enter"))
	if m.cfg.Prefix != "ctrl+q" {
		t.Fatalf("prefix now %q", m.cfg.Prefix)
	}

	data, _ := os.ReadFile(config.Path())
	for _, want := range []string{"# my settings", `prefix = "ctrl+a"`, "[agents.claude]", `args = ["--model", "claude-opus-5-5"]`, `config_model = "anthropic/claude-opus-5-5"`, `env = { A = "1", B = "two words" }`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the file lacks %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "notify") {
		t.Errorf("notify is back to its default, but in the file:\n%s", data)
	}
	v := ansi.Strip(d.view(m, 120, 40))
	if !strings.Contains(v, "Settings") || strings.Contains(v, "from nerdr's next start") {
		t.Errorf("the description is shown before ?:\n%s", v)
	}
	width := lipgloss.Width(v)
	m.dialog.key(m, keyMsg("?"))
	v = ansi.Strip(d.view(m, 120, 40))
	about := strings.Index(v, "from nerdr's next start")
	if about < 0 || about > strings.Index(v, "▸ prefix") {
		t.Errorf("? does not show the description over the line:\n%s", v)
	}
	m.dialog.key(m, keyMsg("down"))
	v = ansi.Strip(d.view(m, 120, 40))
	if strings.Contains(v, "the key before every nerdr command") {
		t.Errorf("the description stays on another line:\n%s", v)
	}
	pickSetting(t, d, "review", "send_findings")
	d.about = true
	if v := ansi.Strip(d.view(m, 120, 40)); !strings.Contains(v, `Example, under [review]: send_findings = "auto"`) ||
		!strings.Contains(v, "\n│     auto: it goes straight to the agent") {
		t.Errorf("? does not show each choice on its line and an example:\n%s", v)
	}
	d.about = false
	pickSetting(t, d, "", "layout")
	m.dialog.key(m, keyMsg("enter"))
	if v := ansi.Strip(d.view(m, 120, 40)); !strings.Contains(v, "wide  agent 75% | F-menu") {
		t.Errorf("layout's choice is not explained:\n%s", v)
	}
	if lipgloss.Width(v) != width {
		t.Errorf("the dialog is %d wide, then %d", width, lipgloss.Width(v))
	}
	m.dialog.key(m, keyMsg("esc"))
	if m.dialog != nil {
		t.Error("esc leaves it open")
	}
}

func TestSplitWords(t *testing.T) {
	got, err := splitWords(`--a "b c" 'd "e"' f=`)
	if err != nil || strings.Join(got, "|") != `--a|b c|d "e"|f=` {
		t.Errorf("%q %v", got, err)
	}
	if _, err := splitWords(`"open`); err == nil {
		t.Error("an open quote was taken")
	}
	for _, w := range got {
		if back, _ := splitWords(quoteWord(w)); len(back) != 1 || back[0] != w {
			t.Errorf("%q reads back as %q", w, back)
		}
	}
}

// TestConfigFileEditedByHand covers the file changed outside nerdr: what
// can change at once does, and what waits for a restart is told once.
func TestConfigFileEditedByHand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	write := func(s string, at time.Time) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(config.Path()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.Path(), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(config.Path(), at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("prefix = \"ctrl+q\"\nlayout = \"agent\"\ntab_details = false\npanel_width = 40\n", now)
	m.configCheck()
	m.toastText = ""

	write("prefix = \"ctrl+b\"\nlayout = \"agent\"\ntab_details = false\npanel_width = 40\nnotify = \"done\"\n", now.Add(time.Second))
	m.configCheck()
	if m.cfg.Notify != "done" || m.cfg.Prefix != "ctrl+q" {
		t.Fatalf("notify %q, prefix %q", m.cfg.Notify, m.cfg.Prefix)
	}
	if m.toastText != "prefix changed: restart nerdr to use it" {
		t.Fatalf("toast = %q", m.toastText)
	}
	m.toastText = ""
	write("prefix = \"ctrl+b\"\nlayout = \"agent\"\ntab_details = false\npanel_width = 40\nnotify = \"off\"\n", now.Add(2*time.Second))
	m.configCheck()
	if m.cfg.Notify != "off" || m.toastText != "" {
		t.Fatalf("notify %q, toast %q", m.cfg.Notify, m.toastText)
	}
	// fps is only read at start: it keeps its value, and says so.
	before := m.cfg.FPS
	write("prefix = \"ctrl+b\"\nlayout = \"agent\"\ntab_details = false\npanel_width = 40\nnotify = \"off\"\nfps = 30\n", now.Add(3*time.Second))
	m.configCheck()
	if m.cfg.FPS != before || m.toastText != "fps changed: restart nerdr to use it" {
		t.Fatalf("fps %d, toast %q", m.cfg.FPS, m.toastText)
	}
}
