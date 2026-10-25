package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nerdr/internal/clean"
	"nerdr/internal/config"
	"nerdr/internal/hooks"
	"nerdr/internal/plugin"
	"nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// An agent runs a plugin by asking nerdr (`nerdr plugin run`), which
// runs it as the user, outside any sandbox the agent is in: the user
// allowed what is in it, and perhaps says yes to the run. nerdr keeps
// what it prints, which the command waiting on it reads and prints for
// the agent (see pluginexec.go). F5 opens to show the user, the plugin's
// light on in the list and a line saying who runs it; the plugin says what
// more is shown, in its plugin.toml's show (see plugin.ShowValues). A
// plugin's screen, when it runs, is told the same, to show the run as it
// likes: see pluginEvent. A plugin that watches goes on, and what it
// prints is sent to the agent as messages.

// maxRunLines is the most of a run's output kept to show, and the most of
// one line: a plugin that prints a progress bar or a very long line must
// not take the screen, or the memory, with it.
const (
	maxRunLines = 500
	maxRunLine  = 4 << 10
	maxPartial  = 64 << 10
	// maxPluginRuns is the most an agent, and nerdr, has going at once: a
	// plugin can wait, and a flood of them must not take the machine.
	maxPluginRuns         = 32
	maxPluginRunsPerAgent = 8
	maxFinishedRuns       = 128
)

// orphaned is how long a run's output may go unread by the command that
// waits on it before the run is taken to have lost it, and is stopped: an
// agent stops a command it has waited on too long.
const orphaned = 3 * readWait

// keepRunResult allows a foreground client to drain a completed run.
// Each read renews it; abandoned results are eventually forgotten.
const keepRunResult = time.Minute

// pluginRun is an agent's run of a plugin, in tab t.
type pluginRun struct {
	id      string
	t       *tab
	agent   string // who runs it, "2:codex"
	dir     string // the plugin's folder
	values  map[string]any
	started time.Time
	ended   time.Time // zero while it runs
	exit    int
	// out is what it printed, a line each, and partial the line it has
	// not ended yet; pumped is how much of what it printed they have.
	out     []string
	partial string
	pumped  int

	proc *runProc
	// from is the agent that runs it; readAt when its command last read
	// what it printed.
	from   *tab
	readAt time.Time
	// watch is set on a run that goes on until it is stopped (see
	// plugin.Watch), whose agent gets what it prints as messages.
	watch bool
	// confined is whether the agent asking for it was (see confined):
	// what it prints, sent to the agent, is as if the agent wrote it.
	confined bool
	// stopBy is who stopped it, "" while no one has: "you" for the agent,
	// "the user".
	stopBy string
	// told is the message with what it printed last put in for the agent,
	// while it waits to be sent: more is added to it.
	told string
}

// running reports whether r has not ended yet.
func (r *pluginRun) running() bool { return r != nil && r.ended.IsZero() }

// add takes what the run printed: a carriage return starts its line over,
// as on a terminal, so a progress bar is one line and not hundreds.
func (r *pluginRun) add(text string) {
	lines := strings.Split(r.partial+text, "\n")
	r.partial = lines[len(lines)-1]
	// Only what comes after the run's last carriage return is shown: a
	// progress bar is one line, not every one it drew.
	if i := strings.LastIndexByte(r.partial, '\r'); i >= 0 {
		r.partial = r.partial[i+1:]
	}
	if len(r.partial) > maxPartial {
		r.partial = tailText(r.partial, maxPartial)
	}
	for _, l := range lines[:len(lines)-1] {
		r.out = append(r.out, runLine(l))
	}
	if over := len(r.out) - maxRunLines; over > 0 {
		r.out = slices.Delete(r.out, 0, over)
	}
}

// tail is the end of s, n bytes or so, at the start of a character.
func tailText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "…" + s
}

// lines is what r printed, the line not ended yet last.
func (r *pluginRun) lines() []string {
	if l := runLine(r.partial); l != "" {
		return append(slices.Clip(r.out), l)
	}
	return r.out
}

// runLine is one line a run printed, as the terminal would leave it after
// its last carriage return, without its colours, safe to draw.
func runLine(l string) string {
	l = ansi.Strip(l)
	l = strings.TrimRight(l, "\r")
	if i := strings.LastIndexByte(l, '\r'); i >= 0 {
		l = l[i+1:]
	}
	l = clean.Line(strings.ReplaceAll(l, "\t", "    "))
	if len(l) > maxRunLine {
		l = ansi.Truncate(l, maxRunLine, "…")
	}
	return l
}

// pluginRunOf is the last run of plugin p in t, if an agent ran it.
func (m *Model) pluginRunOf(t *tab, p plugin.Plugin) *pluginRun {
	if t == nil {
		return nil
	}
	return m.plugins.runs[pluginUIKey(t, p)]
}

// runByID is the run called id, running, recently ended or last of its plugin.
func (m *Model) runByID(id string) *pluginRun {
	if r := m.plugins.byID[id]; r != nil {
		return r
	}
	if r := m.plugins.finished[id]; r != nil {
		return r
	}
	for _, r := range m.plugins.runs {
		if r.id == id {
			return r
		}
	}
	return nil
}

// The answers to a run not started; see hooks.PluginRun.
const (
	runNotAllowed = "not allowed"
	runDeclined   = "declined"
	runBusy       = "busy"
	runUnanswered = "unanswered"
)

// onRunPlugin runs the plugin r names for agent from, as r asks, once
// the user has allowed it, and said yes to the run when that is asked.
func (m *Model) onRunPlugin(r hooks.Report, from *tab) tea.Cmd {
	reply := answerer(r)
	deadline := time.Now().Add(hooks.PluginAskWait - 5*time.Second)
	o := from.owner()
	var p plugin.Plugin
	var names []string
	for _, q := range plugin.Find(plugin.UserDir(config.Path())) {
		names = append(names, clean.Line(q.Name))
		if q.Name == r.Name {
			p = q
		}
	}
	switch {
	case p.Dir == "":
		reply(hooks.Answer{Err: fmt.Sprintf("there is no plugin %q; those there are: %s", clean.Line(r.Name), strings.Join(names, ", "))})
		return nil
	case p.Err != "":
		reply(hooks.Answer{Err: clean.Line(p.Name) + " is broken: " + clean.Line(p.Err)})
		return nil
	case len(p.Run) == 0:
		reply(hooks.Answer{Err: clean.Line(p.Name) + " has no run: it is for the user, in nerdr's F5"})
		return nil
	}
	p, key, err := plugin.Fresh(p)
	switch {
	case err != nil:
		reply(hooks.Answer{Err: p.Name + ": " + err.Error()})
		return nil
	case !plugin.AllowedKey(pluginsAllowedFile(), key):
		m.toast(o.Name + ": " + m.agentLabel(o, from) + " would run the plugin " + p.Name +
			", not allowed as it is: " + m.screenKey(5) + " and a show it and allow it")
		reply(hooks.Answer{Text: runNotAllowed})
		return nil
	}
	// The values are the agent's: checked here as the command checked
	// them, what came to the socket being whatever was written to it.
	vals := plugin.Values{}
	for k, v := range r.Values {
		if err := p.Set(vals, k, v); err != nil {
			reply(hooks.Answer{Err: err.Error()})
			return nil
		}
	}
	if missing := p.Complete(vals); len(missing) > 0 {
		reply(hooks.Answer{Err: p.Name + " needs --" + missing[0].Name + ": ask the user for it"})
		return nil
	}
	dir, err := runDir(o, r.Dir)
	if err != nil {
		reply(hooks.Answer{Err: err.Error()})
		return nil
	}
	confined := m.confined(r, from)
	start := func(m *Model) tea.Cmd {
		defer dir.Close()
		if time.Now().After(deadline) {
			// The command that asked may already have been told the
			// answer did not come; it must not start then.
			reply(hooks.Answer{Text: runUnanswered})
			return nil
		}
		// What runs is the plugin as the user allowed it, though it was
		// asked about a while ago.
		if q, now, err := plugin.Fresh(p); err != nil || now != key || q.Err != "" {
			reply(hooks.Answer{Text: runNotAllowed})
			return nil
		}
		if why := m.runLimit(from); why != "" {
			reply(hooks.Answer{Err: why})
			return nil
		}
		run, err := m.launchRun(from, p, vals, dir, confined, key)
		if err != nil {
			reply(hooks.Answer{Err: p.Name + ": " + err.Error()})
			return nil
		}
		reply(hooks.Answer{Text: "started " + run.id})
		return m.showPluginRun(o, p, run)
	}
	if m.cfg.ConfirmPluginRuns || p.Confirm {
		where := r.Dir
		if where == "" {
			where = o.Dir
		}
		m.askPluginRun(from, p, vals, where, confined, func(m *Model, answer string) tea.Cmd {
			if answer == "allowed" {
				return start(m)
			}
			dir.Close()
			reply(hooks.Answer{Text: answer})
			return nil
		})
		return nil
	}
	return start(m)
}

// launchRun starts plugin p with vals in dir for agent a, as the user,
// with the tab's variables, as its screen is.
// runLimit is why agent a may not start another plugin run, or "".
func (m *Model) runLimit(a *tab) string {
	total, mine := 0, 0
	for _, r := range m.plugins.byID {
		if !r.running() {
			continue
		}
		total++
		if r.from == a {
			mine++
		}
	}
	if mine >= maxPluginRunsPerAgent {
		return fmt.Sprintf("you already have %d plugin runs going: wait for one to end", mine)
	}
	if total >= maxPluginRuns {
		return fmt.Sprintf("nerdr already runs %d plugins: wait for one to end", total)
	}
	return ""
}

func (m *Model) launchRun(a *tab, p plugin.Plugin, vals plugin.Values, dir *os.File, confined bool, key string) (*pluginRun, error) {
	o := a.owner()
	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	cmd := p.RunCmd(vals, "")
	cmd.Env = append(append(childEnv(o), "NERDR_SHELL=1"), p.Env(vals)...)
	if err := pluginCommandIn(cmd, dir, p.Dir, key); err != nil {
		return nil, err
	}
	proc, err := startProc(cmd, m.notify)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	run := &pluginRun{id: hex.EncodeToString(idb), t: o, agent: m.agentLabel(o, a), dir: p.Dir,
		values: map[string]any(vals), started: now, proc: proc, from: a, readAt: now,
		watch: p.Watch, confined: confined}
	v := &m.plugins
	if v.runs == nil {
		v.runs, v.byID = map[string]*pluginRun{}, map[string]*pluginRun{}
	}
	uiKey := pluginUIKey(o, p)
	if old := v.runs[uiKey]; old.running() && old.watch && old.stopBy == "" {
		// One watch of a plugin in a tab: the new one takes over.
		old.stopBy = "a new run of it"
		old.proc.stop()
	}
	if p.Shows(plugin.ShowUI) {
		if u := m.pluginUIOf(o, p); !u.running() || u.key != key {
			m.startPluginUI(o, p, false, key)
		}
	}
	v.runs[uiKey], v.byID[run.id] = run, run
	m.pluginEvent(o, p.Dir, run.startEvent())
	return run, nil
}

// onReadRun answers the command waiting on a run with what it printed
// since it last asked.
func (m *Model) onReadRun(r hooks.Report, from *tab) {
	run := m.runByID(r.Run)
	if run == nil || run.from != from {
		answerer(r)(hooks.Answer{Err: "you run no such plugin run"})
		return
	}
	run.readAt = time.Now()
	run.proc.read(r.Offset, answerer(r))
}

// pumpRuns takes what the runs printed since, for F5, their screens and
// the agents watching with them, and ends those that have ended. A run
// whose command no longer reads it, or whose agent is gone, is stopped.
func (m *Model) pumpRuns(now time.Time) {
	agents := m.allAgents()
	for id, run := range m.plugins.finished {
		if !slices.Contains(agents, run.from) ||
			now.Sub(run.ended) > keepRunResult && now.Sub(run.readAt) > keepRunResult {
			delete(m.plugins.finished, id)
		}
	}
	for _, run := range m.plugins.byID {
		text, next, done, exit := run.proc.since(run.pumped)
		run.pumped = next
		if text != "" {
			run.add(text)
			m.pluginEvent(run.t, run.dir, map[string]any{"event": "output", "run": run.id, "text": clean.Text(text)})
			if run.watch {
				m.tellWatcher(run, text, run.confined)
			}
		}
		switch {
		case done:
			m.endRun(run, exit, run.confined)
		case run.stopBy != "":
		case !slices.Contains(agents, run.from):
			run.stopBy = "nerdr, its agent being gone"
			run.proc.stop()
		case !run.watch && now.Sub(run.readAt) > orphaned:
			run.stopBy = "nerdr, the command waiting on it having stopped"
			run.proc.stop()
		}
	}
}

// stopRuns stops all runs, for nerdr quitting.
func (m *Model) stopRuns() {
	var runs []*pluginRun
	for _, run := range m.plugins.byID {
		run.proc.stop()
		runs = append(runs, run)
	}
	if len(runs) == 0 {
		return
	}
	// Wait for the SIGTERM, then the SIGKILL the stop escalates to: nerdr
	// must not exit before it, or a program that ignores SIGTERM outlives
	// it.
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	for _, run := range runs {
		select {
		case <-run.proc.stopped:
		case <-deadline.C:
			return
		}
	}
}

// showPluginRun opens F5 on plugin p, which run has just started, when
// it is the tab looked at and p's open does not say otherwise, without
// taking the keyboard: the user sees it
// run beside the agent. A screen of the panel's they have the keyboard
// in, or a plugin they are at in F5, is not taken from them.
func (m *Model) showPluginRun(t *tab, p plugin.Plugin, run *pluginRun) tea.Cmd {
	name := p.Name
	if !p.Opens() {
		return nil // its light says it runs
	}
	if t != m.cur() {
		m.toast(t.Name + ": " + clean.Line(run.agent) + " runs the plugin " + clean.Line(name))
		return nil
	}
	if m.focus == focusReview {
		if !m.panelPlugins() {
			m.toast(clean.Line(run.agent) + " runs the plugin " + clean.Line(name) + " · " + m.screenKey(5) + " shows it")
		}
		return nil
	}
	var cmd tea.Cmd
	if !m.panelPlugins() {
		m.reviewOpen = true
		cmd = m.reviewKey(tea.KeyMsg{Type: tea.KeyF5})
	}
	m.pluginList(t)
	for i, q := range m.plugins.list {
		if q.Dir == p.Dir {
			m.plugins.pick(i)
			m.plugins.uiFocus = ""
		}
	}
	return tea.Batch(cmd, m.resize())
}

// startEvent is the event telling a plugin's screen that run started.
func (r *pluginRun) startEvent() map[string]any {
	vals := r.values
	if vals == nil {
		vals = map[string]any{}
	}
	return map[string]any{"event": "start", "run": r.id, "agent": r.agent, "values": vals,
		"time": r.started.Format(time.RFC3339)}
}

// pluginEvent tells the screen of the plugin in folder dir, in t, of an
// agent's run of it, when the screen runs: a JSON object a line, in the
// file NERDR_PLUGIN_EVENTS names, which the screen reads as it grows.
func (m *Model) pluginEvent(t *tab, dir string, ev map[string]any) {
	u := m.plugins.uis[t.ID+"\x00"+dir]
	if u == nil || u.events == nil {
		return
	}
	b, err := json.Marshal(ev)
	if err == nil {
		_, _ = u.events.Write(append(b, '\n'))
	}
}

// pluginEventsFile makes the file a plugin's screen is told of its runs
// in, in nerdr's state folder, where no agent writes.
func pluginEventsFile(p plugin.Plugin) (*os.File, error) {
	dir := filepath.Join(filepath.Dir(state.Dir()), "plugin-events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, p.Name+"-*.jsonl")
}

// runState is the light and the words of run, in a list's row: whether it
// runs, who runs it and how it ended.
func (m *Model) runState(run *pluginRun) (string, string) {
	agent := clean.Line(run.agent)
	switch {
	case run.running() && run.stopBy != "":
		return light(stWorking), "stopping · " + agent
	case run.running() && run.watch:
		return light(stWorking), "watches · " + agent + " · " + duration(time.Since(run.started))
	case run.running():
		return light(stWorking), "runs · " + agent + " · " + duration(time.Since(run.started))
	case run.watch && run.stopBy != "":
		return light(stDone), "stopped · " + agent
	case run.exit == 0:
		return light(stDone), "ok · " + agent
	}
	return light(stBlocked), fmt.Sprintf("exit %d · %s", run.exit, agent)
}

// runLines are the lines F5 shows of run, of plugin p, w wide, above
// its output: who runs it and how long, or how it ended; and the values
// it was given, when p shows them.
func (m *Model) runLines(p plugin.Plugin, run *pluginRun, w int) []string {
	lt, _ := m.runState(run)
	var head string
	agent := clean.Line(run.agent)
	took := run.ended.Sub(run.started).Round(100 * time.Millisecond)
	switch {
	case run.running() && run.watch:
		head = agent + " watches with it · " + duration(time.Since(run.started)) +
			" · what it prints goes to " + agent + " · s stops it"
	case run.running():
		head = agent + " runs it · " + duration(time.Since(run.started))
	case run.watch && run.stopBy != "":
		head = fmt.Sprintf("%s watched with it for %s, until %s stopped it %s", agent, duration(took), run.stopBy, ago(time.Since(run.ended)))
	case run.exit == 0:
		head = fmt.Sprintf("%s ran it %s: ok, in %s", agent, ago(time.Since(run.ended)), took)
	default:
		head = fmt.Sprintf("%s ran it %s: it failed, exit %d, in %s", agent, ago(time.Since(run.ended)), run.exit, took)
	}
	var out []string
	for i, l := range wrapRows(head, w-2, 2) {
		out = append(out, onOff(i == 0, " "+lt+" ", "   ")+l)
	}
	if p.Shows(plugin.ShowValues) {
		names := make([]string, 0, len(run.values))
		for k := range run.values {
			names = append(names, k)
		}
		sort.Strings(names)
		var parts []string
		for _, k := range names {
			parts = append(parts, "--"+clean.Line(k)+" "+ansi.Truncate(clean.Line(valueText(run.values[k])), 200, "…"))
		}
		if len(parts) == 0 {
			parts = []string{"no parameters"}
		}
		for _, l := range wrapRows(strings.Join(parts, "  "), w-3, 4) {
			out = append(out, "   "+l)
		}
	}
	return out
}

// valueText is a parameter's value as the agent gave it: a list joined
// with commas.
func valueText(v any) string {
	if l, ok := v.([]string); ok {
		return strings.Join(l, ",")
	}
	if l, ok := v.([]any); ok {
		var s []string
		for _, x := range l {
			s = append(s, fmt.Sprint(x))
		}
		return strings.Join(s, ",")
	}
	return fmt.Sprint(v)
}

// runOutput is what run printed, as F5 shows it: a rule saying it is what
// the agent gets back, and its last lines, n rows in all, w wide.
func (m *Model) runOutput(run *pluginRun, w, n int) []string {
	agent := clean.Line(run.agent)
	label := "─ back to " + agent + " "
	if run.running() {
		label = "─ " + agent + " gets "
	}
	out := []string{dim(ansi.Truncate(label+strings.Repeat("─", max(w-lipgloss.Width(label), 0)), w, "…"))}
	lines := run.lines()
	if len(lines) == 0 {
		return append(out, dim(onOff(run.running(), " nothing printed yet", " nothing: it printed nothing")))
	}
	for _, l := range lines[max(len(lines)-(n-1), 0):] {
		out = append(out, " "+ansi.Truncate(l, w-1, "…"))
	}
	return out
}

// runAskWait is how long the user has to say yes to an agent's run: short
// of how long the agent waits for nerdr's answer.
const runAskWait = hooks.PluginAskWait - 10*time.Second

// runQuestion is the user asked whether an agent may run a plugin.
type runQuestion struct {
	dialog      *confirm
	agent, name string
	until       time.Time
	decide      func(m *Model, answer string) tea.Cmd
}

// answer decides the run with what, and takes the question away.
func (q *runQuestion) answer(m *Model, what string) tea.Cmd {
	if m.dialog == q.dialog {
		m.dialog = nil
	}
	if m.plugins.asking == q {
		m.plugins.asking = nil
	}
	return q.decide(m, what)
}

// askPluginRun asks the user whether agent a may run plugin p with vals,
// in the folder dir, and decides with "allowed", "declined" or
// "unanswered". While they answer another question, or another agent's,
// it is "busy". The question says where it runs, and, for an agent in a
// sandbox, that it runs outside: a plugin that builds or runs what is in
// the folder runs what the agent wrote there.
func (m *Model) askPluginRun(a *tab, p plugin.Plugin, vals map[string]any, dir string, confined bool, decide func(m *Model, answer string) tea.Cmd) {
	o := a.owner()
	agent := m.agentLabel(o, a)
	if m.dialog != nil || m.plugins.asking != nil {
		m.toast(o.Name + ": " + agent + " would run the plugin " + p.Name + ", and asks again soon")
		decide(m, runBusy)
		return
	}
	bold := lipgloss.NewStyle().Bold(true)
	lines := []string{o.Name + ": " + bold.Render(agent) + " would run it, as you, with", ""}
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		lines = append(lines, "  --"+clean.Line(k)+"  "+bold.Render(ansi.Truncate(clean.Line(valueText(vals[k])), 200, "…")))
	}
	if len(names) == 0 {
		lines = append(lines, dim("  no parameters"))
	}
	lines = append(lines, "", "in "+bold.Render(clean.Line(dir)))
	if confined {
		lines = append(lines, warn(agent+" runs in a sandbox: this runs outside it, on what it wrote there"))
	}
	lines = append(lines, "", dim(fmt.Sprintf("y runs it · n does not · unanswered in %s, it does not run", duration(runAskWait))))
	q := &runQuestion{agent: agent, name: p.Name, until: time.Now().Add(runAskWait), decide: decide}
	q.dialog = &confirm{
		title: "Run " + p.Name + "?",
		lines: lines,
		yes:   func(m *Model) tea.Cmd { return q.answer(m, "allowed") },
		no:    func(m *Model) tea.Cmd { return q.answer(m, runDeclined) },
	}
	m.plugins.asking = q
	m.dialog = q.dialog
}

// maxTold is the most of what a watching run printed one message to its
// agent holds, in bytes: the rest of a flood is left out, and said to be.
const maxTold = 8 << 10

// tellWatcher puts what the watching run printed in a message for the
// agent that started it, sent when the agent is idle; added to the one
// before while that waits. sandboxed says the agent is in a sandbox: what
// came from it is not typed into an agent outside one.
func (m *Model) tellWatcher(run *pluginRun, text string, sandboxed bool) {
	a := run.from
	if a == nil || !slices.Contains(m.allAgents(), a) {
		return
	}
	text = clean.Text(text)
	if strings.TrimSpace(text) == "" {
		return
	}
	name := filepath.Base(run.dir)
	head := fmt.Sprintf("The plugin %s, which you started (run %s), printed:\n\n", name, run.id)
	tail := fmt.Sprintf("\n\nIt goes on watching. Do with it what the user asked of you; stop it with nerdr plugin stop %s.", name)
	body := text
	if n := len(a.Messages); n > 0 && a.Messages[n-1].Text == run.told {
		// The message before has not been sent: it takes this too.
		body = strings.TrimSuffix(strings.TrimPrefix(run.told, head), tail) + "\n" + text
		a.Messages = a.Messages[:n-1]
	}
	if len(body) > maxTold {
		body = "[… the start left out: it printed more than one message holds]\n" + tailText(body, maxTold)
	}
	run.told = head + strings.TrimSpace(body) + tail
	m.addMessageFrom(a, run.told, true, sandboxed)
	run.told = a.Messages[len(a.Messages)-1].Text
}

// endRun is run ended with exit; the agent watching with it is told, but
// for when it stopped it itself.
func (m *Model) endRun(run *pluginRun, exit int, sandboxed bool) {
	run.ended, run.exit = time.Now(), exit
	delete(m.plugins.byID, run.id)
	if !run.watch {
		if m.plugins.finished == nil {
			m.plugins.finished = map[string]*pluginRun{}
		}
		if len(m.plugins.finished) >= maxFinishedRuns {
			var oldest *pluginRun
			for _, r := range m.plugins.finished {
				if oldest == nil || r.ended.Before(oldest.ended) {
					oldest = r
				}
			}
			if oldest != nil {
				delete(m.plugins.finished, oldest.id)
			}
		}
		m.plugins.finished[run.id] = run
	}
	m.pluginEvent(run.t, run.dir, map[string]any{"event": "end", "run": run.id, "exit": exit})
	if !run.watch || run.stopBy == "you" || run.from == nil || !slices.Contains(m.allAgents(), run.from) {
		return
	}
	name := filepath.Base(run.dir)
	why := fmt.Sprintf("it ended by itself, exit %d", exit)
	if run.stopBy != "" {
		why = run.stopBy + " stopped it"
	}
	m.addMessageFrom(run.from, fmt.Sprintf("The plugin %s, which you started (run %s), no longer watches: %s. "+
		"Start it again only if the user wants it to go on.", name, run.id, why), true, sandboxed)
}

// watching reports whether agent a watches with a plugin: it is not put
// to sleep, which would leave what it is told unread.
func (m *Model) watching(a *tab) bool {
	for _, run := range m.plugins.byID {
		if run.watch && run.from == a && run.stopBy == "" {
			return true
		}
	}
	return false
}

// stopAgentRuns stops the runs of agent a that r names, by the run's id
// or the plugin's name.
func (m *Model) stopAgentRuns(r hooks.Report, a *tab) {
	var stopped []string
	for _, run := range m.plugins.byID {
		if run.from == a && run.stopBy == "" && (run.id == r.Run || r.Run == "" && filepath.Base(run.dir) == r.Name) {
			run.stopBy = "you"
			run.proc.stop()
			stopped = append(stopped, filepath.Base(run.dir)+" (run "+run.id+")")
		}
	}
	if len(stopped) == 0 {
		answerer(r)(hooks.Answer{Err: "you run no such plugin: nothing to stop"})
		return
	}
	sort.Strings(stopped)
	answerer(r)(hooks.Answer{Text: "Stopping " + strings.Join(stopped, ", ") + ": it ends within a few seconds."})
}

// stopPickedRun is s in F5: the user stops the run of the plugin picked.
func (m *Model) stopPickedRun(t *tab, p plugin.Plugin) {
	run := m.pluginRunOf(t, p)
	if !run.running() || run.stopBy != "" {
		m.toast(clean.Line(p.Name) + " does not run in this tab")
		return
	}
	run.stopBy = "the user"
	if !run.watch {
		// The command waiting on it says why it ended; a watch's agent is
		// told so when it ends (endRun), not among what it printed.
		run.proc.note("the user stopped it")
	}
	run.proc.stop()
	m.toast("stopping " + clean.Line(p.Name) + "; " + clean.Line(run.agent) + " is told")
}
