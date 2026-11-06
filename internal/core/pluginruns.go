package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/plugin"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// maxRunLines is the most of a run's output kept to show, and the most of
// one line: a plugin that prints a progress bar or a very long line must
// not take the screen, or the memory, with it.
const (
	MaxRunLines = 500
	MaxRunLine  = 4 << 10
	MaxPartial  = 64 << 10
	// MaxPluginRuns is the most an agent, and nerdr, has going at once: a
	// plugin can wait, and a flood of them must not take the machine.
	MaxPluginRuns         = 32
	MaxPluginRunsPerAgent = 8
	MaxFinishedRuns       = 128
)

// Orphaned is how long a run's output may go unread by the command that
// waits on it before the run is taken to have lost it, and is stopped: an
// agent stops a command it has waited on too long.
const Orphaned = 3 * ReadWait

// KeepRunResult allows a foreground client to drain a completed run.
// Each read renews it; abandoned results are eventually forgotten.
const KeepRunResult = time.Minute

// tail is the end of s, n bytes or so, at the start of a character.
func TailText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "…" + s
}

// The answers to a run not started; see hooks.PluginRun.
const (
	RunNotAllowed = "not allowed"
	RunDeclined   = "declined"
	RunBusy       = "busy"
	RunUnanswered = "unanswered"
)

// PluginEventsFile makes the file a plugin's screen is told of its runs
// in, in nerdr's state folder, where no agent writes.
func PluginEventsFile(p plugin.Plugin) (*os.File, error) {
	dir := filepath.Join(filepath.Dir(state.Dir()), "plugin-events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, p.Name+"-*.jsonl")
}

// ValueText is a parameter's value as the agent gave it: a list joined
// with commas.
func ValueText(v any) string {
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

// RunAskWait is how long the user has to say yes to an agent's run: short
// of how long the agent waits for nerdr's answer.
const RunAskWait = hooks.PluginAskWait - 10*time.Second

// MaxTold is the most of what a watching run printed one message to its
// agent holds, in bytes: the rest of a flood is left out, and said to be.
const MaxTold = 8 << 10

// pluginRun is an agent's run of a plugin, in tab t.
type PluginRun struct {
	Id      string
	T       *Tab
	Agent   string // who runs it, "2:codex"
	Dir     string // the plugin's folder
	Values  map[string]any
	Started time.Time
	Ended   time.Time // zero while it runs
	Exit    int
	// out is what it printed, a line each, and partial the line it has
	// not ended yet; pumped is how much of what it printed they have.
	Out     []string
	Partial string
	Pumped  int

	Proc *RunProc
	// from is the agent that runs it; readAt when its command last read
	// what it printed.
	From   *Tab
	ReadAt time.Time
	// watch is set on a run that goes on until it is stopped (see
	// plugin.Watch), whose agent gets what it prints as messages.
	Watch bool
	// confined is whether the agent asking for it was (see confined):
	// what it prints, sent to the agent, is as if the agent wrote it.
	Confined bool
	// stopBy is who stopped it, "" while no one has: "you" for the agent,
	// "the user".
	StopBy string
	// told is the message with what it printed last put in for the agent,
	// while it waits to be sent: more is added to it.
	Told string
}

// pluginRuns is the runs of plugins by agents.
type PluginRuns struct {
	// runs are the last run of each plugin agents ran, by pluginUIKey,
	// and byID those running, by their id: see pluginruns.go.
	Runs, ByID map[string]*PluginRun
	// finished holds ended foreground runs until their clients have had
	// time to read them, even when a newer run occupies runs.
	Finished map[string]*PluginRun
}

// running reports whether r has not ended yet.
func (r *PluginRun) Running() bool { return r != nil && r.Ended.IsZero() }

// add takes what the run printed: a carriage return starts its line over,
// as on a terminal, so a progress bar is one line and not hundreds.
func (r *PluginRun) Add(text string) {
	lines := strings.Split(r.Partial+text, "\n")
	r.Partial = lines[len(lines)-1]
	// Only what comes after the run's last carriage return is shown: a
	// progress bar is one line, not every one it drew.
	if i := strings.LastIndexByte(r.Partial, '\r'); i >= 0 {
		r.Partial = r.Partial[i+1:]
	}
	if len(r.Partial) > MaxPartial {
		r.Partial = TailText(r.Partial, MaxPartial)
	}
	for _, l := range lines[:len(lines)-1] {
		r.Out = append(r.Out, RunLine(l))
	}
	if over := len(r.Out) - MaxRunLines; over > 0 {
		r.Out = slices.Delete(r.Out, 0, over)
	}
}

// lines is what r printed, the line not ended yet last.
func (r *PluginRun) Lines() []string {
	if l := RunLine(r.Partial); l != "" {
		return append(slices.Clip(r.Out), l)
	}
	return r.Out
}

// runLine is one line a run printed, as the terminal would leave it after
// its last carriage return, without its colours, safe to draw.
func RunLine(l string) string {
	l = ansi.Strip(l)
	l = strings.TrimRight(l, "\r")
	if i := strings.LastIndexByte(l, '\r'); i >= 0 {
		l = l[i+1:]
	}
	l = clean.Line(strings.ReplaceAll(l, "\t", "    "))
	if len(l) > MaxRunLine {
		l = ansi.Truncate(l, MaxRunLine, "…")
	}
	return l
}

// pluginRunOf is the last run of plugin p in t, if an agent ran it.
func (m *Core) PluginRunOf(t *Tab, p plugin.Plugin) *PluginRun {
	if t == nil {
		return nil
	}
	return m.PluginRuns.Runs[PluginUIKey(t, p)]
}

// runByID is the run called id, running, recently ended or last of its plugin.
func (m *Core) RunByID(id string) *PluginRun {
	if r := m.PluginRuns.ByID[id]; r != nil {
		return r
	}
	if r := m.PluginRuns.Finished[id]; r != nil {
		return r
	}
	for _, r := range m.PluginRuns.Runs {
		if r.Id == id {
			return r
		}
	}
	return nil
}

// onRunPlugin runs the plugin r names for agent from, as r asks, once
// the user has allowed it, and said yes to the run when that is asked.
func (m *Core) OnRunPlugin(r hooks.Report, from *Tab) Cmd {
	reply := Answerer(r)
	deadline := time.Now().Add(hooks.PluginAskWait - 5*time.Second)
	o := from.Owner()
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
	case !plugin.AllowedKey(PluginsAllowedFile(), key):
		m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " would run the plugin " + p.Name +
			", not allowed as it is: " + ScreenRef(5) + " and a show it and allow it")
		reply(hooks.Answer{Text: RunNotAllowed})
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
	dir, err := RunDir(o, r.Dir)
	if err != nil {
		reply(hooks.Answer{Err: err.Error()})
		return nil
	}
	confined := m.Confined(r, from)
	start := func() Cmd {
		defer dir.Close()
		if time.Now().After(deadline) {
			// The command that asked may already have been told the
			// answer did not come; it must not start then.
			reply(hooks.Answer{Text: RunUnanswered})
			return nil
		}
		// What runs is the plugin as the user allowed it, though it was
		// asked about a while ago.
		if q, now, err := plugin.Fresh(p); err != nil || now != key || q.Err != "" {
			reply(hooks.Answer{Text: RunNotAllowed})
			return nil
		}
		if why := m.RunLimit(from); why != "" {
			reply(hooks.Answer{Err: why})
			return nil
		}
		run, err := m.LaunchRun(from, p, vals, dir, confined, key)
		if err != nil {
			reply(hooks.Answer{Err: p.Name + ": " + err.Error()})
			return nil
		}
		reply(hooks.Answer{Text: "started " + run.Id})
		return m.Tell(PluginRunMsg{T: m.Ref(o), P: p, Run: run.Id})
	}
	if m.Cfg.ConfirmPluginRuns || p.Confirm || confined {
		where := r.Dir
		if where == "" {
			where = o.Dir
		}
		decide := func(answer string) Cmd {
			if answer == AnswerAllowed {
				return start()
			}
			dir.Close()
			reply(hooks.Answer{Text: answer})
			return nil
		}
		if m.Asked(AskPluginRun) || m.HasClient() && !m.Client.DialogClosed() {
			// Asked again once the user is free to answer.
			m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " would run the plugin " + p.Name + ", and asks again soon")
			return decide(RunBusy)
		}
		// With no client the question waits for one as long as the agent
		// waits for its answer: only a user can say yes.
		q := &Question{Kind: AskPluginRun, A: m.Ref(from), P: p, Vals: vals, Dir: where, Confined: confined, Until: time.Now().Add(RunAskWait)}
		return m.Ask(q, decide)
	}
	return start()
}

// launchRun starts plugin p with vals in dir for agent a, as the user,
// with the tab's variables, as its screen is.
// runLimit is why agent a may not start another plugin run, or "".
func (m *Core) RunLimit(a *Tab) string {
	total, mine := 0, 0
	for _, r := range m.PluginRuns.ByID {
		if !r.Running() {
			continue
		}
		total++
		if r.From == a {
			mine++
		}
	}
	if mine >= MaxPluginRunsPerAgent {
		return fmt.Sprintf("you already have %d plugin runs going: wait for one to end", mine)
	}
	if total >= MaxPluginRuns {
		return fmt.Sprintf("nerdr already runs %d plugins: wait for one to end", total)
	}
	return ""
}

func (m *Core) LaunchRun(a *Tab, p plugin.Plugin, vals plugin.Values, dir *os.File, confined bool, key string) (*PluginRun, error) {
	o := a.Owner()
	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	cmd := p.RunCmd(vals, "")
	cmd.Env = append(append(ChildEnv(o), "NERDR_SHELL=1"), p.Env(vals)...)
	spec, err := PluginSpec(cmd, dir, p.Dir, key)
	if err != nil {
		return nil, err
	}
	m.Hold(&spec, Label{Kind: HeldRun, Tab: o.ID, Plugin: p.Dir, Key: key, Run: hex.EncodeToString(idb),
		From: a.ID, Values: map[string]any(vals), Watch: p.Watch, Confined: confined}, confined, "")
	proc, err := StartProc(m.Programs(), spec, m.Notify)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	run := &PluginRun{Id: hex.EncodeToString(idb), T: o, Agent: m.AgentLabel(o, a), Dir: p.Dir,
		Values: map[string]any(vals), Started: now, Proc: proc, From: a, ReadAt: now,
		Watch: p.Watch, Confined: confined}
	v := &m.PluginRuns
	if v.Runs == nil {
		v.Runs, v.ByID = map[string]*PluginRun{}, map[string]*PluginRun{}
	}
	uiKey := PluginUIKey(o, p)
	if old := v.Runs[uiKey]; old.Running() && old.Watch && old.StopBy == "" {
		// One watch of a plugin in a tab: the new one takes over.
		old.StopBy = "a new run of it"
		old.Proc.Stop()
	}
	if p.Shows(plugin.ShowUI) {
		m.EnsurePluginUI(o, p, key)
	}
	v.Runs[uiKey], v.ByID[run.Id] = run, run
	m.PluginEvent(o, p.Dir, run.StartEvent())
	return run, nil
}

// onReadRun answers the command waiting on a run with what it printed
// since it last asked.
func (m *Core) OnReadRun(r hooks.Report, from *Tab) {
	run := m.RunByID(r.Run)
	if run == nil || run.From != from {
		Answerer(r)(hooks.Answer{Err: "you run no such plugin run"})
		return
	}
	run.ReadAt = time.Now()
	run.Proc.Read(r.Offset, Answerer(r))
}

// pumpRuns takes what the runs printed since, for F5, their screens and
// the agents watching with them, and ends those that have ended. A run
// whose command no longer reads it, or whose agent is gone, is stopped.
func (m *Core) PumpRuns(now time.Time) {
	agents := m.AllAgents()
	for id, run := range m.PluginRuns.Finished {
		if !slices.Contains(agents, run.From) ||
			now.Sub(run.Ended) > KeepRunResult && now.Sub(run.ReadAt) > KeepRunResult {
			delete(m.PluginRuns.Finished, id)
		}
	}
	for _, run := range m.PluginRuns.ByID {
		text, next, done, exit := run.Proc.Since(run.Pumped)
		// Not back: a run taken up after a crash is pumped from where its
		// output had got to, before it is all read again.
		run.Pumped = max(run.Pumped, next)
		if text != "" {
			run.Add(text)
			m.PluginEvent(run.T, run.Dir, map[string]any{"event": "output", "run": run.Id, "text": clean.Text(text)})
			if run.Watch {
				m.TellWatcher(run, text, run.Confined)
			}
		}
		switch {
		case done:
			m.EndRun(run, exit, run.Confined)
		case run.StopBy != "":
		case !slices.Contains(agents, run.From):
			run.StopBy = "nerdr, its agent being gone"
			run.Proc.Stop()
		case !run.Watch && now.Sub(run.ReadAt) > Orphaned:
			run.StopBy = "nerdr, the command waiting on it having stopped"
			run.Proc.Stop()
		}
	}
}

// stopRuns stops all runs, for nerdr quitting.
func (m *Core) StopRuns() {
	var runs []*PluginRun
	for _, run := range m.PluginRuns.ByID {
		run.Proc.Stop()
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
		case <-run.Proc.Stopped:
		case <-deadline.C:
			return
		}
	}
}

// startEvent is the event telling a plugin's screen that run started.
func (r *PluginRun) StartEvent() map[string]any {
	vals := r.Values
	if vals == nil {
		vals = map[string]any{}
	}
	return map[string]any{"event": "start", "run": r.Id, "agent": r.Agent, "values": vals,
		"time": r.Started.Format(time.RFC3339)}
}

// tellWatcher puts what the watching run printed in a message for the
// agent that started it, sent when the agent is idle; added to the one
// before while that waits. sandboxed says the agent is in a sandbox: what
// came from it is not typed into an agent outside one.
func (m *Core) TellWatcher(run *PluginRun, text string, sandboxed bool) {
	a := run.From
	if a == nil || !slices.Contains(m.AllAgents(), a) {
		return
	}
	text = clean.Text(text)
	if strings.TrimSpace(text) == "" {
		return
	}
	name := filepath.Base(run.Dir)
	head := fmt.Sprintf("The plugin %s, which you started (run %s), printed:\n\n", name, run.Id)
	tail := fmt.Sprintf("\n\nIt goes on watching. Do with it what the user asked of you; stop it with nerdr plugin stop %s.", name)
	body := text
	if n := len(a.Messages); n > 0 && a.Messages[n-1].Text == run.Told {
		// The message before has not been sent: it takes this too.
		body = strings.TrimSuffix(strings.TrimPrefix(run.Told, head), tail) + "\n" + text
		a.Messages = a.Messages[:n-1]
	}
	if len(body) > MaxTold {
		body = "[… the start left out: it printed more than one message holds]\n" + TailText(body, MaxTold)
	}
	run.Told = head + strings.TrimSpace(body) + tail
	m.AddMessageFrom(a, run.Told, true, sandboxed)
	run.Told = a.Messages[len(a.Messages)-1].Text
}

// endRun is run ended with exit; the agent watching with it is told, but
// for when it stopped it itself.
func (m *Core) EndRun(run *PluginRun, exit int, sandboxed bool) {
	run.Ended, run.Exit = time.Now(), exit
	delete(m.PluginRuns.ByID, run.Id)
	if !run.Watch {
		if m.PluginRuns.Finished == nil {
			m.PluginRuns.Finished = map[string]*PluginRun{}
		}
		if len(m.PluginRuns.Finished) >= MaxFinishedRuns {
			var oldest *PluginRun
			for _, r := range m.PluginRuns.Finished {
				if oldest == nil || r.Ended.Before(oldest.Ended) {
					oldest = r
				}
			}
			if oldest != nil {
				delete(m.PluginRuns.Finished, oldest.Id)
			}
		}
		m.PluginRuns.Finished[run.Id] = run
	}
	m.PluginEvent(run.T, run.Dir, map[string]any{"event": "end", "run": run.Id, "exit": exit})
	if !run.Watch || run.StopBy == "you" || run.From == nil || !slices.Contains(m.AllAgents(), run.From) {
		return
	}
	name := filepath.Base(run.Dir)
	why := fmt.Sprintf("it ended by itself, exit %d", exit)
	if run.StopBy != "" {
		why = run.StopBy + " stopped it"
	}
	m.AddMessageFrom(run.From, fmt.Sprintf("The plugin %s, which you started (run %s), no longer watches: %s. "+
		"Start it again only if the user wants it to go on.", name, run.Id, why), true, sandboxed)
}

// watching reports whether agent a watches with a plugin: it is not put
// to sleep, which would leave what it is told unread.
func (m *Core) Watching(a *Tab) bool {
	for _, run := range m.PluginRuns.ByID {
		if run.Watch && run.From == a && run.StopBy == "" {
			return true
		}
	}
	return false
}

// stopAgentRuns stops the runs of agent a that r names, by the run's id
// or the plugin's name.
func (m *Core) StopAgentRuns(r hooks.Report, a *Tab) {
	var stopped []string
	for _, run := range m.PluginRuns.ByID {
		if run.From == a && run.StopBy == "" && (run.Id == r.Run || r.Run == "" && filepath.Base(run.Dir) == r.Name) {
			run.StopBy = "you"
			run.Proc.Stop()
			stopped = append(stopped, filepath.Base(run.Dir)+" (run "+run.Id+")")
		}
	}
	if len(stopped) == 0 {
		Answerer(r)(hooks.Answer{Err: "you run no such plugin: nothing to stop"})
		return
	}
	sort.Strings(stopped)
	Answerer(r)(hooks.Answer{Text: "Stopping " + strings.Join(stopped, ", ") + ": it ends within a few seconds."})
}

// stopPickedRun is s in F5: the user stops the run of the plugin picked.
func (m *Core) StopPickedRun(t *Tab, p plugin.Plugin) {
	run := m.PluginRunOf(t, p)
	if !run.Running() || run.StopBy != "" {
		m.Toast(clean.Line(p.Name) + " does not run in this tab")
		return
	}
	run.StopBy = "the user"
	if !run.Watch {
		// The command waiting on it says why it ended; a watch's agent is
		// told so when it ends (endRun), not among what it printed.
		run.Proc.Note("the user stopped it")
	}
	run.Proc.Stop()
	m.Toast("stopping " + clean.Line(p.Name) + "; " + clean.Line(run.Agent) + " is told")
}

// pluginUIKey is the key of plugin p's screen in tab t.
func PluginUIKey(t *Tab, p plugin.Plugin) string { return t.ID + "\x00" + p.Dir }

// pluginsAllowedFile is where the plugins the user allowed are kept.
func PluginsAllowedFile() string { return plugin.AllowedFile(state.Dir()) }

// onPlugin answers an agent's `nerdr plugin`: whether a plugin is
// allowed (list), running one (run), what a run printed (read), and a
// stop.
func (m *Core) OnPlugin(r hooks.Report) Cmd {
	from := m.AgentByHook(r.Pane)
	if from == nil {
		Answerer(r)(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	switch r.What {
	case hooks.PluginRun:
		return m.OnRunPlugin(r, from)
	case hooks.PluginRead:
		m.OnReadRun(r, from)
	case hooks.PluginStop:
		m.StopAgentRuns(r, from)
	default:
		Answerer(r)(hooks.Answer{Text: OnOff(plugin.AllowedKey(PluginsAllowedFile(), r.Text), "allowed", RunNotAllowed)})
	}
	return nil
}

func OnOff(on bool, yes, no string) string {
	if on {
		return yes
	}
	return no
}
