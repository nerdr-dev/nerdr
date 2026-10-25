package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/plugin"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// F5 lists the user's plugins; a screen starts once allowed, gets the
// keyboard, and F5 goes between it and the list.
func TestPluginScreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	wt := t.TempDir()
	tb.Dir, tb.Worktrees[0].Path = wt, wt
	dir := filepath.Join(cfg, "nerdr", "plugins", "hello")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "description = \"Says hello.\"\nui = [\"sh\", \"-c\", \"echo hello from $NERDR_PLUGIN; cat\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	f5 := tea.KeyMsg{Type: tea.KeyF5}

	m.Update(f5)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "hello") || !strings.Contains(view, "Says hello.") {
		t.Fatalf("F5 does not show the plugin:\n%s", view)
	}
	m.Update(keyMsg("enter"))
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("a screen started without asking: dialog %T", m.dialog)
	}
	m.Update(keyMsg("y"))
	defer m.Shutdown()
	if m.pluginUIPane() == nil {
		t.Fatal("the plugin's screen does not have the keyboard")
	}
	for deadline := time.Now().Add(3 * time.Second); !strings.Contains(ansi.Strip(m.View()), "hello from hello"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the screen does not show what the plugin printed:\n%s", ansi.Strip(m.View()))
		}
	}
	m.Update(f5)
	if m.pluginUIPane() != nil {
		t.Fatal("F5 on the plugin's screen should go back to the list")
	}
	m.Update(f5)
	if m.pluginUIPane() == nil {
		t.Fatal("F5 on the list should go back to the running screen")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if m.pluginUIPane() != nil || m.panelPlugins() {
		t.Fatal("F1 should leave the plugins")
	}
}

// writePlugin puts a plugin called name in dir, with a screen running
// script.
func writePlugin(t *testing.T, dir, name, script string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "description = \"" + name + "\"\nui = [\"./ui.sh\"]\n"
	if err := os.WriteFile(filepath.Join(d, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "ui.sh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A change to a plugin while the user is asked about it is not allowed by
// their yes: they are asked again, about what is there now.
func TestPluginChangedWhileAsked(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	wt := t.TempDir()
	tb.Dir, tb.Worktrees[0].Path = wt, wt
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "seed", "echo shown; cat\n")
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("enter"))
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("dialog %T, want the question", m.dialog)
	}
	writePlugin(t, plugins, "seed", "echo swapped; cat\n")
	m.Update(keyMsg("y"))
	if m.pluginUIPane() != nil {
		t.Fatal("the yes to what was shown started what is there now")
	}
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("dialog %T: the user should be asked again", m.dialog)
	}
	m.Update(keyMsg("y"))
	if m.pluginUIPane() == nil {
		t.Fatal("the yes to the plugin as it is did not start it")
	}
}

// The keyboard stays with the screen of the plugin picked when plugins
// are added or taken away before it, and goes back to the list when the
// plugin picked goes.
func TestPluginPickSurvivesTheListChanging(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	for _, n := range []string{"a", "b", "c"} {
		writePlugin(t, plugins, n, "cat\n")
	}
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("j"))
	m.Update(keyMsg("j"))
	m.Update(keyMsg("enter")) // c, asked about first
	m.Update(keyMsg("y"))
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("k"))
	m.Update(keyMsg("enter")) // b
	m.Update(keyMsg("y"))
	b := m.pluginUIPane()
	if b == nil {
		t.Fatal("b's screen does not have the keyboard")
	}
	reread := func() {
		m.plugins.at = time.Time{}
		m.View()
	}

	writePlugin(t, plugins, "aa", "cat\n")
	reread()
	if m.pluginUIPane() != b {
		t.Fatal("a plugin added before b took the keyboard from it")
	}
	if err := os.RemoveAll(filepath.Join(plugins, "a")); err != nil {
		t.Fatal(err)
	}
	reread()
	if m.pluginUIPane() != b {
		t.Fatal("a plugin taken away before b took the keyboard from it")
	}
	if err := os.RemoveAll(filepath.Join(plugins, "b")); err != nil {
		t.Fatal(err)
	}
	reread()
	if p := m.pluginUIPane(); p != nil {
		t.Fatal("b gone, the keyboard went to another plugin's screen")
	}
}

// No plugin, the user's own among them, starts or runs before the user
// allowed it as it is; a shows what is in it first.
func TestPluginsWaitForTheUser(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "mine", "echo the-script-itself; cat\n")
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	p := m.pluginList(tb)[0]
	q, key, err := plugin.Fresh(p)
	if err != nil {
		t.Fatal(err)
	}

	a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Plugin, What: hooks.PluginCheck, Name: q.Dir, Text: key})
	if a.Text != "not allowed" {
		t.Fatalf("an agent was told %q of a plugin the user has not allowed", a)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	if !strings.Contains(ansi.Strip(m.View()), "not allowed") {
		t.Fatal("F5 does not say the plugin is not allowed")
	}
	m.Update(keyMsg("a"))
	c, ok := m.dialog.(*confirm)
	if !ok || !strings.Contains(ansi.Strip(strings.Join(c.scroll, "\n")), "echo the-script-itself") {
		t.Fatalf("a does not show what the plugin runs: %T", m.dialog)
	}
	m.Update(keyMsg("y"))
	if m.pluginUIPane() != nil {
		t.Fatal("allowing a plugin started its screen")
	}
	a, _ = askNerdr(m, tb, hooks.Report{Method: hooks.Plugin, What: hooks.PluginCheck, Name: q.Dir, Text: key})
	if a.Text != "allowed" {
		t.Fatalf("allowed, an agent was told %q", a)
	}
	writePlugin(t, plugins, "mine", "echo changed; cat\n")
	_, changed, _ := plugin.Fresh(p)
	a, _ = askNerdr(m, tb, hooks.Report{Method: hooks.Plugin, What: hooks.PluginCheck, Name: q.Dir, Text: changed})
	if a.Text != "not allowed" {
		t.Fatalf("changed, an agent was told %q", a)
	}
}

// What is in a plugin's folder, too long for the screen, scrolls in the
// question allowing it, down to its last line.
func TestPluginAskScrolls(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	var script strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&script, "echo line-%d\n", i)
	}
	writePlugin(t, filepath.Join(cfg, "nerdr", "plugins"), "long", script.String())
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("a"))
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("dialog %T, want the question", m.dialog)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "echo line-1 ") || strings.Contains(view, "line-200") {
		t.Fatalf("the question should start at the top:\n%s", view)
	}
	m.Update(keyMsg("end"))
	c := m.dialog.(*confirm)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "line-200") || c.top+c.shown != len(c.scroll) {
		t.Fatalf("end should show the last line:\n%s", view)
	}
	m.Update(keyMsg("home"))
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if c.top != 1 {
		t.Fatalf("the wheel scrolled to %d, want 1", c.top)
	}
	m.Update(keyMsg("y"))
	if m.dialog != nil {
		t.Fatalf("y should answer the question: dialog %T", m.dialog)
	}
}

// Plugin metadata is written by whoever wrote the plugin, an agent among
// them: an escape sequence in it must not reach the terminal.
func TestPluginMetadataCannotEscapeTheTerminal(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	dir := filepath.Join(plugins, "escape")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const seq = `\u001b]52;c;ZXZpbA==\u0007`
	toml := "description = \"d " + seq + "\"\ntitle = \"t " + seq + "\"\nrun = [\"true\"]\n" +
		"[[params]]\nname = \"mode\"\ntype = \"choice\"\noptions = [\"a" + seq + "\"]\ndefault = \"a" + seq + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "x\x1b]52;c;ZXZpbA==\x07.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("content \x1b]52;c;ZXZpbA==\x07"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testModel(t)
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	if view := m.View(); strings.Contains(view, "\x1b]52;") {
		t.Fatalf("the terminal got the plugin's escape sequence:\n%q", view)
	}
	m.Update(keyMsg("a"))
	c, ok := m.dialog.(*confirm)
	if !ok {
		t.Fatalf("a did not ask: dialog %T", m.dialog)
	}
	if lines := strings.Join(c.lines, "\n"); strings.Contains(lines, "\x1b]52;") {
		t.Fatalf("the question got the plugin's escape sequence: %q", lines)
	}
	if scroll := strings.Join(c.scroll, "\n"); strings.Contains(scroll, "\x1b]52;") {
		t.Fatalf("the preview got the plugin's escape sequence: %q", scroll)
	}
}

// The question reads no special file: a FIFO in a folder an agent wrote
// must not hold the screen.
func TestPluginPreviewSkipsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	done := make(chan []string, 1)
	go func() { done <- pluginPreview(dir, 100, 40) }()
	select {
	case lines := <-done:
		if !strings.Contains(strings.Join(lines, "\n"), "not a regular file") {
			t.Fatalf("the FIFO was not named as one: %q", lines)
		}
	case <-time.After(time.Second):
		t.Fatal("pluginPreview waited on a FIFO")
	}
}

// A file too large to draw is named, not read into the question.
func TestPluginPreviewDoesNotReadHugeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), make([]byte, maxPreviewFile+1), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := pluginPreview(dir, 100, 40)
	if !strings.Contains(strings.Join(lines, "\n"), "too large to show") {
		t.Fatalf("a large file was not named: %q", lines)
	}
}

// d takes the allowance away again: future runs and screens wait for a
// new yes.
func TestPluginDisallow(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "mine", "cat\n")
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("a"))
	m.Update(keyMsg("y"))
	p := m.pluginList(tb)[0]
	if !plugin.Allowed(pluginsAllowedFile(), p) {
		t.Fatal("not allowed after a")
	}
	m.Update(keyMsg("d"))
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("d did not ask: dialog %T", m.dialog)
	}
	m.Update(keyMsg("y"))
	p = m.pluginList(tb)[0]
	if plugin.Allowed(pluginsAllowedFile(), p) {
		t.Fatal("still allowed after d")
	}
}

// A screen started for an agent's run elsewhere does not take the
// keyboard from the screen the user is in.
func TestPluginUISecondScreenKeepsTheKeyboard(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	toml := "description = \"x\"\nrun = [\"true\"]\nui = [\"sh\", \"-c\", \"cat\"]\nshow = [\"ui\"]\n"
	allowedPlugin(t, cfg, "a", toml)
	allowedPlugin(t, cfg, "b", toml)

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.pluginList(tb)
	for i, q := range m.plugins.list {
		if q.Name == "a" {
			m.plugins.pick(i)
		}
	}
	m.Update(keyMsg("enter"))
	a := m.pluginUIPane()
	if a == nil {
		t.Fatal("a's screen does not have the keyboard")
	}
	started(t, m, askRun(m, tb, "b", nil))
	if got := m.pluginUIPane(); got != a {
		t.Fatal("b's screen took the keyboard from a's")
	}
}

// A changed plugin is started anew, not shown as the old screen, and its
// old screen stops.
func TestPluginUIChangedContentRestarts(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "mine", "cat\n")
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("y"))
	old := m.pluginUIPane()
	if old == nil {
		t.Fatal("the screen does not have the keyboard")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	writePlugin(t, plugins, "mine", "cat\n# changed\n")
	m.plugins.at = time.Time{}
	m.View()
	m.Update(keyMsg("enter"))
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("the changed plugin was not asked about: dialog %T", m.dialog)
	}
	m.Update(keyMsg("y"))
	if now := m.pluginUIPane(); now == nil || now == old {
		t.Fatal("the changed plugin kept its old screen")
	}
}

// A plugin whose folder is taken away does not leave a screen or run that
// F5 cannot show, or stop.
func TestPluginGoneStopsItsScreenAndRuns(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	p := allowedPlugin(t, cfg, "gone", "description = \"x\"\nrun = [\"sleep\", \"30\"]\nui = [\"sh\", \"-c\", \"cat\"]\nshow = [\"ui\"]\n")

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.pluginList(tb)
	for i, q := range m.plugins.list {
		if q.Name == "gone" {
			m.plugins.pick(i)
		}
	}
	m.Update(keyMsg("enter"))
	if m.pluginUIPane() == nil {
		t.Fatal("the screen did not start")
	}
	first := started(t, m, askRun(m, tb, "gone", nil))
	second := started(t, m, askRun(m, tb, "gone", nil))
	if err := os.RemoveAll(p.Dir); err != nil {
		t.Fatal(err)
	}
	m.plugins.goneAt = time.Time{}
	m.checkPluginsGone(time.Now().Add(10 * time.Second))
	if m.pluginUIOf(tb, p) != nil {
		t.Fatal("the screen of a gone plugin is still there")
	}
	if _, ok := m.plugins.runs[pluginUIKey(tb, p)]; ok {
		t.Fatal("the run of a gone plugin is still in F5")
	}
	for _, run := range []*pluginRun{first, second} {
		if run.stopBy != "the plugin is gone" {
			t.Fatalf("a run was not stopped: %q", run.stopBy)
		}
	}
}

// A plugin.toml written by rename is missing for a moment; its screen is
// not stopped for that.
func TestPluginGoneWaitsForAnEditor(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "mine", "cat\n")
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("y"))
	p := m.pluginList(tb)[0]
	if m.pluginUIOf(tb, p) == nil {
		t.Fatal("the screen did not start")
	}
	toml := filepath.Join(p.Dir, plugin.File)
	tmp := toml + ".new"
	if err := os.Rename(toml, tmp); err != nil {
		t.Fatal(err)
	}
	m.plugins.goneAt = time.Time{}
	m.checkPluginsGone(time.Now())
	if m.pluginUIOf(tb, p) == nil {
		t.Fatal("a plugin.toml being written stopped its screen")
	}
	if err := os.Rename(tmp, toml); err != nil {
		t.Fatal(err)
	}
	m.plugins.goneAt = time.Time{}
	m.checkPluginsGone(time.Now().Add(2 * time.Second))
	if m.pluginUIOf(tb, p) == nil {
		t.Fatal("the screen did not stay")
	}
	for dir := range m.plugins.missing {
		if dir == p.Dir {
			t.Fatal("a restored plugin is still marked missing")
		}
	}
}

// With more plugins than fit, the list scrolls so the picked row is seen.
func TestPluginListScrollsToThePick(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	for i := 0; i < 20; i++ {
		writePlugin(t, plugins, fmt.Sprintf("p%02d", i), "cat\n")
	}
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.pluginList(tb)
	m.plugins.pick(19)
	lines := m.pluginsBody(tb, 80, 12)
	if len(lines) < 4 || !strings.Contains(strings.Join(lines[:4], "\n"), "p19") {
		t.Fatalf("the picked row is not in the list:\n%s", strings.Join(lines, "\n"))
	}
}

// Going to another panel leaves the plugin screen; coming back to F5 does
// not put the keyboard in it by itself.
func TestPluginUIFocusClearedByOtherPanel(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "mine", "cat\n")
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("y"))
	if m.pluginUIPane() == nil {
		t.Fatal("the screen does not have the keyboard")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if m.plugins.uiFocus != "" || m.pluginUIPane() != nil {
		t.Fatal("F1 left the plugin screen with the keyboard")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	if m.pluginUIPane() != nil {
		t.Fatal("F5 took the keyboard back into the screen by itself")
	}
}

// d stops allowing every version of the plugin in the folder, even when
// the version shown now is not the one that was allowed.
func TestPluginDisallowOldVersions(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	plugins := filepath.Join(cfg, "nerdr", "plugins")
	writePlugin(t, plugins, "mine", "cat\n")
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()

	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m.Update(keyMsg("a"))
	m.Update(keyMsg("y"))
	old := m.pluginList(tb)[0]
	if !plugin.Allowed(pluginsAllowedFile(), old) {
		t.Fatal("not allowed after a")
	}
	writePlugin(t, plugins, "mine", "cat\n# changed\n")
	m.plugins.at = time.Time{}
	m.View()
	m.Update(keyMsg("d"))
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("d did not ask: dialog %T", m.dialog)
	}
	m.Update(keyMsg("y"))
	if plugin.Allowed(pluginsAllowedFile(), old) || plugin.AllowedDir(pluginsAllowedFile(), old.Dir) {
		t.Fatal("an old version is still allowed after d")
	}
}
