package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/plugin"

	"github.com/charmbracelet/x/ansi"
)

// askRun asks nerdr to run plugin name for tb's agent with vals, as
// nerdr plugin run does; the answer comes on the channel.
func askRun(m *Model, tb *tab, name string, vals map[string]any) chan hooks.Answer {
	reply := make(chan hooks.Answer, 1)
	m.Update(hooks.Report{Method: hooks.Plugin, Pane: tb.hookKey(), Reply: reply, What: hooks.PluginRun,
		Name: name, Values: vals, Dir: tb.Dir})
	return reply
}

// answered is the answer on c, "" when none has come.
func answered(c chan hooks.Answer) string {
	select {
	case a := <-c:
		return a.Text + a.Err
	default:
		return ""
	}
}

// started is the run nerdr started, as its answer on c says.
func started(t *testing.T, m *Model, c chan hooks.Answer) *pluginRun {
	t.Helper()
	a := answered(c)
	id, ok := strings.CutPrefix(a, "started ")
	if !ok || m.runByID(id) == nil {
		t.Fatalf("nerdr did not start the run: %q", a)
	}
	return m.runByID(id)
}

// readRun reads what run printed until it ends, as nerdr plugin run does,
// and its exit status.
func readRun(t *testing.T, m *Model, tb *tab, run *pluginRun) (string, int) {
	t.Helper()
	var out strings.Builder
	offset := 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		reply := make(chan hooks.Answer, 1)
		m.Update(hooks.Report{Method: hooks.Plugin, Pane: tb.hookKey(), Reply: reply, What: hooks.PluginRead, Run: run.id, Offset: offset})
		var got hooks.RunOutput
		select {
		case a := <-reply:
			if err := json.Unmarshal([]byte(a.Text), &got); err != nil {
				t.Fatalf("read answered %q %q", a.Text, a.Err)
			}
		case <-time.After(7 * time.Second):
			t.Fatal("a read was not answered")
		}
		out.WriteString(got.Text)
		offset = got.Offset
		if got.Ended && got.Text == "" {
			return out.String(), got.Exit
		}
	}
	t.Fatal("the run did not end")
	return "", 0
}

// pumped pumps the runs until run has ended.
func pumped(t *testing.T, m *Model, run *pluginRun) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); run.running(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the run did not end")
		}
		m.pumpRuns(time.Now())
	}
}

// allowedPlugin puts plugin name, with plugin.toml toml, in the user's
// folder under cfg, allowed as it is.
func allowedPlugin(t *testing.T, cfg, name, toml string) plugin.Plugin {
	t.Helper()
	d := filepath.Join(cfg, "nerdr", "plugins", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, key, err := plugin.Fresh(plugin.Load(d))
	if err != nil || p.Err != "" {
		t.Fatal(err, p.Err)
	}
	if err := plugin.Allow(pluginsAllowedFile(), p, key); err != nil {
		t.Fatal(err)
	}
	return p
}

// An agent's run is nerdr's, as the user, in the agent's folder: F5
// opens on it without the keyboard, its light on, who runs it, and what
// the plugin shows of it — the parameters, and what the agent gets back,
// which the command waiting on it reads.
func TestPluginRunShown(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	// As pwd prints it: on macOS the temporary folder is behind a symlink.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tb.Dir = dir
	defer m.Shutdown()
	allowedPlugin(t, cfg, "aaa", "description = \"Another.\"\nrun = [\"true\"]\n")
	p := allowedPlugin(t, cfg, "deploy", "description = \"Deploys.\"\nrun = [\"./run.sh\"]\nshow = [\"values\", \"output\"]\n"+
		"[[params]]\nname = \"repos\"\ntype = \"multi\"\n")
	if err := os.WriteFile(filepath.Join(p.Dir, "run.sh"), []byte("#!/bin/sh\necho building in $(pwd)\n"+
		"printf 'step 1\\rstep 2\\n'\nprintf '\\033[31mred\\033[0m\\n'\necho repos=$NERDR_PARAM_REPOS >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p = allowedPlugin(t, cfg, "deploy", "description = \"Deploys.\"\nrun = [\"./run.sh\"]\nshow = [\"values\", \"output\"]\n"+
		"[[params]]\nname = \"repos\"\ntype = \"multi\"\n")

	run := started(t, m, askRun(m, tb, "deploy", map[string]any{"repos": []any{"api", "web"}}))
	if !m.panelPlugins() || m.focus == focusReview {
		t.Fatalf("a run should open F5 and leave the keyboard where it was: plugins %v, focus %v", m.panelPlugins(), m.focus)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "runs · ") {
		t.Fatalf("F5 does not say it runs:\n%s", view)
	}
	out, exit := readRun(t, m, tb, run)
	if exit != 3 || !strings.Contains(out, "building in "+tb.Dir) || !strings.Contains(out, "repos=api,web") {
		t.Fatalf("the command reads %q, exit %d", out, exit)
	}
	pumped(t, m, run)
	view := ansi.Strip(m.View())
	for _, want := range []string{"--repos api,web", "building", "step 2", "red", "exit 3", "back to"} {
		if !strings.Contains(view, want) {
			t.Fatalf("F5 does not show %q of the run:\n%s", want, view)
		}
	}
	if strings.Contains(view, "step 1") || strings.Contains(view, "[31m") {
		t.Fatalf("a carriage return should start the line over, and colours go:\n%s", view)
	}

	// Values not the plugin's are refused; one changed since it was
	// allowed does not run.
	if a := answered(askRun(m, tb, "deploy", map[string]any{"nope": "x"})); !strings.Contains(a, "no parameter") {
		t.Fatalf("a parameter it does not take was answered %q", a)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "run.sh"), []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if a := answered(askRun(m, tb, "deploy", nil)); a != runNotAllowed {
		t.Fatalf("a plugin changed since it was allowed was answered %q", a)
	}
}

// A plugin that shows its screen gets it started for an agent's run, and
// the screen is told of the run in NERDR_PLUGIN_EVENTS.
func TestPluginRunTellsTheScreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	m.router = input.New(nil, 0x11)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	p := allowedPlugin(t, cfg, "board", "description = \"Shows.\"\nrun = [\"sh\", \"-c\", \"sleep 0.3; echo done-it\"]\nui = [\"sh\", \"-c\", "+
		"\"tail -f \\\"$NERDR_PLUGIN_EVENTS\\\" | grep --line-buffered -o 'event...[a-z]*\\\\|clean..true\\\\|done-it\\\\|exit..0'\"]\nshow = [\"ui\"]\n"+
		"[[params]]\nname = \"clean\"\ntype = \"bool\"\n")
	run := started(t, m, askRun(m, tb, "board", map[string]any{"clean": true}))
	if !m.pluginUIOf(tb, p).running() {
		t.Fatal("the plugin's screen was not started for the run")
	}
	if m.pluginUIPane() != nil {
		t.Fatal("a screen started for a run should not take the keyboard")
	}
	pumped(t, m, run)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		view := ansi.Strip(m.View())
		if strings.Count(view, `event":"start`) == 1 && strings.Contains(view, `clean":true`) &&
			strings.Contains(view, "done-it") && strings.Contains(view, `exit":0`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the screen was not told of the run:\n%s", view)
		}
	}
	events := m.pluginUIOf(tb, p).events.Name()
	m.stopPluginUI(tb, p)
	if _, err := os.Stat(events); !os.IsNotExist(err) {
		t.Fatal("the events file outlives its screen")
	}
}

// show is values, output or ui, and ui only with a ui.
func TestPluginShowChecked(t *testing.T) {
	d := t.TempDir()
	for toml, want := range map[string]string{
		"description = \"x\"\nrun = [\"true\"]\nshow = [\"everything\"]\n": "not values, output or ui",
		"description = \"x\"\nrun = [\"true\"]\nshow = [\"ui\"]\n":         "no ui to show",
		"description = \"x\"\nrun = [\"true\"]\nshow = [\"output\"]\n":     "",
	} {
		dir := filepath.Join(d, "p")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o644)
		if got := plugin.Load(dir).Err; want == "" && got != "" || !strings.Contains(got, want) {
			t.Fatalf("%q: error %q, want %q", toml, got, want)
		}
	}
}

// A plugin whose open is false runs without F5 opening on it: its light
// says it runs.
func TestPluginRunOpenOff(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	p := allowedPlugin(t, cfg, "quiet", "description = \"Quiet.\"\nrun = [\"true\"]\nopen = false\n")
	started(t, m, askRun(m, tb, "quiet", nil))
	if m.panelPlugins() {
		t.Fatal("F5 opened for a plugin whose open is false")
	}
	if m.pluginRunOf(tb, p) == nil {
		t.Fatal("the run was not kept, for its light")
	}
}

// With confirm_plugin_runs, or a plugin's own confirm, the user says yes
// or no to each run, seeing its values; unanswered, it does not run.
func TestPluginRunConfirmed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	allowedPlugin(t, cfg, "free", "description = \"Free.\"\nrun = [\"true\"]\n[[params]]\nname = \"target\"\n")
	allowedPlugin(t, cfg, "careful", "description = \"Careful.\"\nrun = [\"true\"]\nconfirm = true\n[[params]]\nname = \"target\"\n")
	vals := map[string]any{"target": "prod"}

	if a := answered(askRun(m, tb, "free", vals)); !strings.HasPrefix(a, "started ") {
		t.Fatalf("a plugin not asking for it was answered %q", a)
	}
	c := askRun(m, tb, "careful", vals)
	d, ok := m.dialog.(*confirm)
	if !ok || !strings.Contains(ansi.Strip(strings.Join(d.lines, "\n")), "--target  prod") {
		t.Fatalf("confirm = true did not ask, with the values: %T", m.dialog)
	}
	if text := ansi.Strip(strings.Join(d.lines, "\n")); !strings.Contains(text, "in "+tb.Dir) || strings.Contains(text, "sandbox") {
		t.Fatalf("the question does not say where it runs, or says a sandbox:\n%s", text)
	}
	if a := answered(askRun(m, tb, "free", vals)); !strings.HasPrefix(a, "started ") {
		t.Fatalf("a run not to be asked about, while another is, was answered %q", a)
	}
	m.cfg.ConfirmPluginRuns = true
	if a := answered(askRun(m, tb, "free", vals)); a != runBusy {
		t.Fatalf("a second question was answered %q", a)
	}
	m.Update(keyMsg("n"))
	if a := answered(c); a != runDeclined {
		t.Fatalf("no was answered %q", a)
	}
	c = askRun(m, tb, "free", vals)
	m.Update(keyMsg("y"))
	if a := answered(c); !strings.HasPrefix(a, "started ") || m.dialog != nil {
		t.Fatalf("yes was answered %q, dialog %T", a, m.dialog)
	}
	c = askRun(m, tb, "free", vals)
	m.plugins.asking.until = time.Now().Add(-time.Second)
	m.checkPluginUIs()
	if a := answered(c); a != runUnanswered || m.dialog != nil {
		t.Fatalf("a question not answered in time was answered %q, dialog %T", a, m.dialog)
	}
}

// A watching run sends what it prints to the agent that started it, a
// message added to while it waits; it goes on until the agent or the user
// stops it, and the agent is told when it ends but by its own stop. A
// run not watching whose command no longer reads it is stopped.
func TestPluginWatch(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	allowedPlugin(t, cfg, "logwatch", "description = \"Watches the log.\"\nwatch = true\n"+
		"run = [\"sh\", \"-c\", \"echo ERROR one; sleep 0.2; echo ERROR two; exec sleep 30\"]\n")
	allowedPlugin(t, cfg, "slow", "description = \"Slow.\"\nrun = [\"sleep\", \"30\"]\n")
	tb.status = stWorking // what it is told waits

	run := started(t, m, askRun(m, tb, "logwatch", nil))
	for deadline := time.Now().Add(5 * time.Second); len(tb.Messages) == 0 || !strings.Contains(tb.Messages[0].Text, "ERROR two"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("what it printed did not come to the agent: %+v", tb.Messages)
		}
		m.pumpRuns(time.Now())
	}
	if n := len(tb.Messages); n != 1 || !strings.Contains(tb.Messages[0].Text, "ERROR one\nERROR two") ||
		!strings.Contains(tb.Messages[0].Text, "nerdr plugin stop logwatch") || !tb.Messages[0].Auto {
		t.Fatalf("what it printed should wait for the agent in one message: %+v", tb.Messages)
	}
	if !m.watching(tb) {
		t.Fatal("the agent should be watching: it would be put to sleep")
	}
	// The command that started it has long returned: it goes on.
	m.pumpRuns(time.Now().Add(time.Hour))
	if !run.running() || run.stopBy != "" {
		t.Fatal("a watch stopped for want of a command reading it")
	}
	reply := make(chan hooks.Answer, 1)
	m.Update(hooks.Report{Method: hooks.Plugin, Pane: tb.hookKey(), Reply: reply, What: hooks.PluginStop, Name: "logwatch"})
	if a := answered(reply); !strings.Contains(a, "Stopping logwatch") {
		t.Fatalf("stop answered %q", a)
	}
	pumped(t, m, run)
	if len(tb.Messages) != 1 {
		t.Fatalf("the agent that stopped it is told it ended: %+v", tb.Messages)
	}

	// The user stops the next with s in F5; the agent is told.
	tb.Messages = nil
	run = started(t, m, askRun(m, tb, "logwatch", nil))
	m.focus = focusReview
	m.pluginList(tb)
	for i, q := range m.plugins.list {
		if q.Name == "logwatch" {
			m.plugins.pick(i)
		}
	}
	m.pluginsKey(keyMsg("s"))
	pumped(t, m, run)
	if last := tb.Messages[len(tb.Messages)-1].Text; !strings.Contains(last, "the user stopped it") {
		t.Fatalf("the agent is not told the user stopped it: %q", last)
	}
	for _, msg := range tb.Messages {
		if strings.Contains(msg.Text, "[nerdr:") {
			t.Fatalf("nerdr's note for a waiting command came to the agent as what the watch printed: %q", msg.Text)
		}
	}

	// A run its command no longer reads is stopped.
	slow := started(t, m, askRun(m, tb, "slow", nil))
	m.pumpRuns(time.Now().Add(time.Minute))
	pumped(t, m, slow)
	if !strings.Contains(slow.stopBy, "command") {
		t.Fatalf("a run left unread was not stopped: %q", slow.stopBy)
	}
}

// A newer run must not hide the result of an older one that its client
// has not drained yet. Idle results expire, but reads renew their lease.
func TestPluginOverlappingRunResults(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	p := allowedPlugin(t, cfg, "overlap", "description = \"Test.\"\nrun = [\"sh\", \"-c\", \"echo result-$NERDR_PARAM_NAME; exit 3\"]\nopen = false\n[[params]]\nname = \"name\"\n")
	first := started(t, m, askRun(m, tb, p.Name, map[string]any{"name": "first"}))
	second := started(t, m, askRun(m, tb, p.Name, map[string]any{"name": "second"}))
	pumped(t, m, first)
	pumped(t, m, second)
	if got := m.pluginRunOf(tb, p); got != second {
		t.Fatal("F5 should show the latest run")
	}
	if m.runByID(first.id) != first {
		t.Fatal("older run disappeared before its result was read")
	}
	// Knowing its ID must not let another agent read the output.
	reply := make(chan hooks.Answer, 1)
	m.onReadRun(hooks.Report{Run: first.id, Reply: reply}, &tab{})
	if a := <-reply; a.Err == "" {
		t.Fatal("another agent could read the result")
	}
	for _, r := range []*pluginRun{first, second} {
		out, exit := readRun(t, m, tb, r)
		if out != "result-"+r.values["name"].(string)+"\n" || exit != 3 {
			t.Fatalf("wrong result: %q, exit %d", out, exit)
		}
	}
	// A repeated pump must not end the same run again.
	ended := first.ended
	m.pumpRuns(time.Now())
	if !first.ended.Equal(ended) {
		t.Fatal("completed run was ended twice")
	}
	future := time.Now().Add(2 * keepRunResult)
	first.readAt = future
	m.pumpRuns(future)
	if m.runByID(first.id) != first {
		t.Fatal("a result being read was expired")
	}
	m.pumpRuns(future.Add(keepRunResult + time.Second))
	if len(m.plugins.finished) != 0 || m.runByID(first.id) != nil {
		t.Fatal("abandoned results were not expired")
	}
	if m.pluginRunOf(tb, p) != second {
		t.Fatal("expiry removed the latest F5 result")
	}
}

// The program and files a plugin's command names in its folder are used
// from there, not from the folder the agent runs in: a run.sh in the tab
// cannot take the place of the one the user allowed.
func TestPluginRunUsesThePluginsScript(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()

	d := filepath.Join(cfg, "nerdr", "plugins", "script")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "plugin.toml"),
		[]byte("description = \"Says which script.\"\nrun = [\"sh\", \"run.sh\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "run.sh"), []byte("echo from-plugin\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tb.Dir, "run.sh"), []byte("echo from-agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, key, err := plugin.Fresh(plugin.Load(d))
	if err != nil || p.Err != "" {
		t.Fatal(err, p.Err)
	}
	if err := plugin.Allow(pluginsAllowedFile(), p, key); err != nil {
		t.Fatal(err)
	}

	run := started(t, m, askRun(m, tb, "script", nil))
	out, exit := readRun(t, m, tb, run)
	if out != "from-plugin\n" || exit != 0 {
		t.Fatalf("the run printed %q, exit %d; it ran the agent's script", out, exit)
	}
}

// A line, or the line not ended yet, is bounded: a progress bar or a
// very long line must not grow the run's memory or its drawing for ever.
func TestPluginRunOutputIsCapped(t *testing.T) {
	r := &pluginRun{}
	r.add("a" + strings.Repeat("x", 4*maxPartial) + "\n")
	if len(r.out) != 1 || len(r.out[0]) > maxRunLine+4 {
		t.Fatalf("a long line was kept as %d bytes", len(r.out[0]))
	}
	r.add("b" + strings.Repeat("y", 4*maxPartial))
	if len(r.partial) > maxPartial+4 {
		t.Fatalf("the line not ended was kept as %d bytes", len(r.partial))
	}
	progress := &pluginRun{}
	for i := 0; i < 5; i++ {
		progress.add("\r" + strings.Repeat("z", 4*maxPartial))
	}
	if len(progress.partial) > maxPartial+4 {
		t.Fatalf("a progress bar grew to %d bytes", len(progress.partial))
	}
	for _, l := range progress.lines() {
		if len(l) > maxRunLine+4 {
			t.Fatalf("a drawn line is %d bytes", len(l))
		}
	}
}

// A flood of plugin runs is refused: one agent has a few, nerdr has a
// bounded number in all.
func TestPluginRunLimit(t *testing.T) {
	m := testModel(t)
	defer m.Shutdown()
	a := m.cur()
	m.plugins.byID = map[string]*pluginRun{}
	for i := 0; i < maxPluginRunsPerAgent; i++ {
		id := fmt.Sprintf("%016x", i)
		m.plugins.byID[id] = &pluginRun{id: id, t: a, from: a}
	}
	if why := m.runLimit(a); why == "" || !strings.Contains(why, "you already have") {
		t.Fatalf("an agent's limit was not reached: %q", why)
	}
	m.plugins.byID = map[string]*pluginRun{}

	for i := 0; i < maxPluginRuns; i++ {
		id := fmt.Sprintf("%016x", i)
		m.plugins.byID[id] = &pluginRun{id: id, t: a, from: &tab{}}
	}
	if why := m.runLimit(a); why == "" || !strings.Contains(why, "nerdr already runs") {
		t.Fatalf("nerdr's limit was not reached: %q", why)
	}
	m.plugins.byID = nil
}

// A watch that prints only blank lines does not send its agent a message.
func TestPluginWatchSkipsBlankLines(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	allowedPlugin(t, cfg, "blank", "description = \"Blank.\"\nwatch = true\n"+
		"run = [\"sh\", \"-c\", \"printf '\\\\n\\\\n'; sleep 30\"]\n")
	run := started(t, m, askRun(m, tb, "blank", nil))
	time.Sleep(100 * time.Millisecond)
	m.pumpRuns(time.Now())
	if len(tb.Messages) != 0 {
		t.Fatalf("blank output became messages: %+v", tb.Messages)
	}
	run.stopBy = "you"
	run.proc.stop()
	pumped(t, m, run)
}

// Asked for by an agent in a sandbox, a plugin run always asks the user,
// even when neither confirm_plugin_runs nor the plugin's own confirm is
// set, and the question says that the plugin runs outside the sandbox.
func TestPluginRunQuestionSaysTheSandbox(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	tb.sandboxed = true
	defer m.Shutdown()
	allowedPlugin(t, cfg, "careful", "description = \"Careful.\"\nrun = [\"true\"]\n")
	askRun(m, tb, "careful", nil)
	d, ok := m.dialog.(*confirm)
	if !ok {
		t.Fatalf("not asked: %T", m.dialog)
	}
	if text := ansi.Strip(strings.Join(d.lines, "\n")); !strings.Contains(text, "runs in a sandbox: this runs outside it") {
		t.Errorf("the question does not say the sandbox:\n%s", text)
	}
}
