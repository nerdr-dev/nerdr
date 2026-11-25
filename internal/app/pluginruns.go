package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/plugin"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// An agent runs a plugin by asking nerdr (`nerdr plugin run`), which
// runs it as the user, outside any sandbox the agent is in: the user
// allowed what is in it, and perhaps says yes to the run. nerdr keeps
// what it prints, which the command waiting on it reads and prints for
// the agent (see pluginexec.go). The plugins pane opens to show the user, the plugin's
// light on in the list and a line saying who runs it; the plugin says what
// more is shown, in its plugin.toml's show (see plugin.ShowValues). A
// plugin's screen, when it runs, is told the same, to show the run as it
// likes: see pluginEvent. A plugin that watches goes on, and what it
// prints is sent to the agent as messages.

// showPluginRun opens the tab's plugins pane on plugin p, which run has
// just started, when it is the tab looked at and p's open does not say
// otherwise, without taking the keyboard: the user sees it run beside
// the agent. A screen of the panel's they have the keyboard in, or a
// plugins pane they are in, is not taken from them.
func (m *Model) showPluginRun(t *core.Tab, p plugin.Plugin, run *core.PluginRun) tea.Cmd {
	name := p.Name
	if !p.Opens() {
		return nil // its light says it runs
	}
	if p.IsGlobal() {
		// No tab's plugins pane has it: the global plugins' dialog, which
		// opens on it with open_global_plugins, but is not taken from the
		// user, nor put over another dialog or a panel they are in.
		mc := m.machineOf(t)
		switch {
		case m.global.shown:
			if m.plugins.uiFocus == "" && m.globalOn() == mc {
				m.pickPlugin(nil, p.Dir)
			}
			return nil
		case m.openGlobalPlugins() && m.dialog == nil && !m.panelDlg && m.focus != core.FocusReview && !m.inViewPane():
			m.global.on = mc
			cmd := m.showGlobal()
			m.pickPlugin(nil, p.Dir)
			return cmd
		}
		m.toast(t.Name + ": " + clean.Line(run.Agent) + " runs the global plugin " + clean.Line(name) + " · " + m.pk("v") + " shows it")
		return nil
	}
	if t != m.Cur() {
		m.toast(t.Name + ": " + clean.Line(run.Agent) + " runs the plugin " + clean.Line(name))
		return nil
	}
	if m.focus == core.FocusReview {
		if !m.panelPlugins() {
			m.toast(clean.Line(run.Agent) + " runs the plugin " + clean.Line(name) + " · " + m.pk("t p") + " shows it")
		}
		return nil
	}
	pv := m.viewOfScreen(t, core.ViewPlugins)
	var cmd tea.Cmd
	if pv == nil {
		cmd = m.do(core.NewView{T: m.fleet.Ref(t), Screen: core.ViewPlugins, Quiet: true})
		if pv = m.viewOfScreen(t, core.ViewPlugins); pv == nil {
			// Opened on another machine's core: picked once it is here.
			if m.viewPick == nil {
				m.viewPick = map[*core.Tab]string{}
			}
			m.viewPick[t] = p.Dir
			return cmd
		}
	} else if m.viewFocused(t, pv) {
		return nil
	}
	m.pl(pv).small = false
	m.viewOf(t, pv)
	pick := m.inView(t, pv, func() tea.Cmd {
		m.pickPlugin(t, p.Dir)
		return nil
	})
	return tea.Batch(cmd, pick, m.resize())
}

// inViewPane reports whether the user is in a pane of the panel's, the
// code changes or the plugins, of the active tab: one with the keyboard,
// whose keys the global plugins' dialog must not take.
func (m *Model) inViewPane() bool {
	t := m.Cur()
	return m.viewIn != nil || t != nil && m.focus == core.FocusAgent && m.focused(t).Kind == core.PaneView
}

// openGlobalPlugins reports whether the global plugins' dialog opens by
// itself on a global plugin an agent runs (open_global_plugins).
func (m *Model) openGlobalPlugins() bool {
	b := m.cfg().OpenGlobalPlugins
	return b == nil || *b
}

// pickPlugin picks the plugin in dir in the panel's list, its screen
// without the keyboard.
func (m *Model) pickPlugin(t *core.Tab, dir string) {
	m.pluginList(t)
	for i, q := range m.plugins.list {
		if q.Dir == dir {
			m.plugins.pick(i)
			m.plugins.uiFocus = ""
		}
	}
}

// runState is the light and the words of run, in a list's row: whether it
// runs, who runs it and how it ended.
func (m *Model) runState(run *core.PluginRun) (string, string) {
	agent := clean.Line(run.Agent)
	switch {
	case run.Running() && run.StopBy != "":
		return light(core.StWorking), "stopping · " + agent
	case run.Running() && run.Watch:
		return light(core.StWorking), "watches · " + agent + " · " + core.Duration(time.Since(run.Started))
	case run.Running():
		return light(core.StWorking), "runs · " + agent + " · " + core.Duration(time.Since(run.Started))
	case run.Watch && run.StopBy != "":
		return light(core.StDone), "stopped · " + agent
	case run.Exit == 0:
		return light(core.StDone), "ok · " + agent
	}
	return light(core.StBlocked), fmt.Sprintf("exit %d · %s", run.Exit, agent)
}

// runLines are the lines the plugins pane shows of run, of plugin p, w wide, above
// its output: who runs it and how long, or how it ended; and the values
// it was given, when p shows them.
func (m *Model) runLines(p plugin.Plugin, run *core.PluginRun, w int) []string {
	lt, _ := m.runState(run)
	var head string
	agent := clean.Line(run.Agent)
	took := run.Ended.Sub(run.Started).Round(100 * time.Millisecond)
	switch {
	case run.Running() && run.Watch:
		head = agent + " watches with it · " + core.Duration(time.Since(run.Started)) +
			" · what it prints goes to " + agent + " · s stops it"
	case run.Running():
		head = agent + " runs it · " + core.Duration(time.Since(run.Started))
	case run.Watch && run.StopBy != "":
		head = fmt.Sprintf("%s watched with it for %s, until %s stopped it %s", agent, core.Duration(took), run.StopBy, core.Ago(time.Since(run.Ended)))
	case run.Exit == 0:
		head = fmt.Sprintf("%s ran it %s: ok, in %s", agent, core.Ago(time.Since(run.Ended)), took)
	default:
		head = fmt.Sprintf("%s ran it %s: it failed, exit %d, in %s", agent, core.Ago(time.Since(run.Ended)), run.Exit, took)
	}
	var out []string
	for i, l := range wrapRows(head, w-2, 2) {
		out = append(out, core.OnOff(i == 0, " "+lt+" ", "   ")+l)
	}
	out = append(out, m.flowLines(p, run, w)...)
	if p.Shows(plugin.ShowValues) {
		names := make([]string, 0, len(run.Values))
		for k := range run.Values {
			names = append(names, k)
		}
		sort.Strings(names)
		var parts []string
		for _, k := range names {
			parts = append(parts, "--"+clean.Line(k)+" "+ansi.Truncate(clean.Line(core.ValueText(run.Values[k])), 200, "…"))
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

// flowPulse is how long a way between an agent and a plugin stays lit
// after something went along it.
const flowPulse = 2 * time.Second

var (
	flowLit  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	flowDone = lipgloss.NewStyle().Foreground(lightColours[core.StDone])
	flowBad  = lipgloss.NewStyle().Foreground(lightColours[core.StBlocked])
)

// flowIn and flowOut say whether something went from run's agent to its
// plugin, or back, within flowPulse of now: the values when it started,
// what it printed, and how it ended.
func flowIn(run *core.PluginRun, now time.Time) bool { return now.Sub(run.Started) < flowPulse }
func flowOut(run *core.PluginRun, now time.Time) bool {
	return !run.OutAt.IsZero() && now.Sub(run.OutAt) < flowPulse ||
		!run.Running() && now.Sub(run.Ended) < flowPulse
}

// flowMark is what a plugin's row in the list shows of run's flow: an
// arrow lit for a while after something went in or came back.
func flowMark(run *core.PluginRun) string {
	now := time.Now()
	var s string
	if flowIn(run, now) {
		s += flowLit.Render(" ▶")
	}
	if flowOut(run, now) {
		s += flowLit.Render(" ◀")
	}
	return s
}

// flowLines are two lines drawing what went between run's agent and
// plugin p, w wide: the values the agent gave it, and what it gave back,
// each way lit for a while after something went along it.
func (m *Model) flowLines(p plugin.Plugin, run *core.PluginRun, w int) []string {
	now := time.Now()
	agent, name := clean.Line(run.Agent), clean.Line(p.Name)
	at := func(t time.Time) string { return dim(t.Format("15:04:05")) }
	line := func(arrow, what, when string) string {
		left := "   " + agent + " " + arrow + " " + name + "  "
		room := w - lipgloss.Width(left) - lipgloss.Width(when) - 2
		return left + ansi.Truncate(what, max(room, 1), "…") + "  " + when
	}

	// In: the values, given as it starts.
	in := flowDone.Render("━━▶")
	if flowIn(run, now) {
		in = flowLit.Render("━━▶")
	}
	what := core.Plural(len(run.Values), "value")
	if len(run.Values) == 0 {
		what = "no values"
	} else if p.Shows(plugin.ShowValues) {
		what += ", below" // the values, whole, as runLines shows them
	}
	out := []string{line(in, "got "+what, at(run.Started))}

	// Back: what it printed, and how it ended.
	n := len(run.Lines())
	back := core.Plural(n, "line") + fmt.Sprintf(", %d bytes", run.Pumped)
	var arrow, when string
	switch {
	case n == 0 && run.Pumped == 0 && run.Running():
		arrow, back, when = dim("◀┄┄"), "nothing back yet", ""
	case run.Running():
		arrow, back, when = flowDone.Render("◀━━"), "gives back "+back, at(run.OutAt)
	case run.Watch && run.StopBy != "":
		arrow, back, when = flowDone.Render("◀━━"), "gave back "+back+" · stopped", at(run.Ended)
	case run.Exit == 0:
		arrow, back, when = flowDone.Render("◀━━"), "gave back "+back+" · ended ok", at(run.Ended)
	default:
		arrow, back, when = flowBad.Render("◀━━"), "gave back "+back+fmt.Sprintf(" · failed, exit %d", run.Exit), at(run.Ended)
	}
	if flowOut(run, now) {
		arrow = flowLit.Render("◀━━")
	}
	return append(out, line(arrow, back, when))
}

// runOutput is what run printed, as the plugins pane shows it: a rule saying it is what
// the agent gets back, and its last lines, n rows in all, w wide.
func (m *Model) runOutput(run *core.PluginRun, w, n int) []string {
	agent := clean.Line(run.Agent)
	label := "─ back to " + agent + " "
	if run.Running() {
		label = "─ " + agent + " gets "
	}
	out := []string{dim(ansi.Truncate(label+strings.Repeat("─", max(w-lipgloss.Width(label), 0)), w, "…"))}
	lines := run.Lines()
	if len(lines) == 0 {
		return append(out, dim(core.OnOff(run.Running(), " nothing printed yet", " nothing: it printed nothing")))
	}
	for _, l := range lines[max(len(lines)-(n-1), 0):] {
		out = append(out, " "+ansi.Truncate(l, w-1, "…"))
	}
	return out
}

// runQuestion is the user asked whether an agent may run a plugin: core's
// question, and the dialog that shows it.
type runQuestion struct {
	dialog *confirm
	q      core.Question
	// mc is the machine whose core asked it.
	mc *fleet.Machine
}

// answer answers the question with what; core tells of it being answered
// with AnsweredMsg, which takes it away.
func (q *runQuestion) answer(m *Model, what string) tea.Cmd {
	return m.doOn(q.mc, core.Answer{ID: q.q.ID, Answer: what})
}

// answered takes the question away once core has its answer, from the
// user or from the time running out.
func (q *runQuestion) answered(m *Model) {
	if m.dialog == q.dialog {
		m.dialog = nil
	}
	if m.pluginAsking == q {
		m.pluginAsking = nil
	}
}

// askPluginRun asks the user whether an agent may run a plugin, as core's
// question q says: with what values, in which folder, and, for an agent in
// a sandbox, that it runs outside: a plugin that builds or runs what is in
// the folder runs what the agent wrote there.
func (m *Model) askPluginRun(q core.Question) {
	mc := m.tellingOn()
	a, p, vals, dir := m.fleet.ByRef(q.A), q.P, q.Vals, q.Dir
	if a == nil {
		m.doOn(mc, core.Answer{ID: q.ID, Answer: core.RunBusy}) // gone already
		return
	}
	o := a.Owner()
	agent := m.machineOf(o).Core.AgentLabel(o, a)
	bold := lipgloss.NewStyle().Bold(true)
	lines := []string{o.Name + ": " + bold.Render(agent) + " would run it, as you, with"}
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	// Each value is shown whole, scrolled if long, and with its control
	// and invisible characters drawn as marked escapes, as askRepoSetup
	// shows a setup: the user allows what they read, and the plugin is
	// given the value as it is.
	hidden := 0
	shown := func(s string) string {
		t, n := clean.Marked(s, warn)
		hidden += n
		return t
	}
	var scroll []string
	for _, k := range names {
		scroll = append(scroll, "  --"+shown(k)+"  "+bold.Render(shown(core.ValueText(vals[k]))))
	}
	if len(names) == 0 {
		scroll = append(scroll, dim("  no parameters"))
	}
	if hidden > 0 {
		lines = append(lines, warn(fmt.Sprintf("They hold %d character%s that would not show, drawn here as escapes.", hidden, pluralS(hidden))))
	}
	lines = append(lines, "")
	after := []string{"", "in " + bold.Render(clean.Line(dir))}
	if q.Confined {
		after = append(after, warn(agent+" runs in a sandbox: this runs outside it, on what it wrote there"))
	}
	after = append(after, "", dim(fmt.Sprintf("y runs it · n does not · unanswered in %s, it does not run", core.Duration(core.RunAskWait))))
	rq := &runQuestion{q: q, mc: mc}
	rq.dialog = &confirm{
		title:  "Run " + clean.Line(p.Name) + "?",
		lines:  lines,
		scroll: scroll,
		after:  after,
		toEnd:  true,
		yes:    func(m *Model) tea.Cmd { return rq.answer(m, core.AnswerAllowed) },
		no:     func(m *Model) tea.Cmd { return rq.answer(m, core.RunDeclined) },
	}
	m.pluginAsking = rq
	m.dialog = rq.dialog
}
