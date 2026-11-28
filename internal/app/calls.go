package app

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/proc"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// callsDialog is the prefix and g: how many times nerdr has run git and
// gh for each tab since it started, by the folder they ran in, to see
// what keeps them busy; and, for the row picked with ↑ ↓, by command. The
// calls are those of the core of the tab's machine, where nearly all are
// made: it is asked for them when the dialog opens, and again every
// callsEvery while it is open, so the counts go up as calls are made.
// Any other key closes it.
type callsDialog struct {
	mc     *fleet.Machine    // whose core's calls are shown
	pick   string            // the row whose commands are shown, by name
	job    string            // of the ReadCalls asked last
	calls  map[proc.Call]int // as core last said, nil until it has
	since  time.Time         // when core's process started
	opened map[proc.Call]int // the counts core first said, for what came since
}

// callsEvery is how often the calls dialog asks core for the counts again.
const callsEvery = time.Second

// callsAgainMsg asks core for the calls of the dialog of job again.
type callsAgainMsg struct{ job string }

func (m *Model) newCallsDialog() (*callsDialog, tea.Cmd) {
	d := &callsDialog{pick: core.AllCalls, mc: m.tabOn()}
	if t := m.Cur(); t != nil {
		d.pick = t.Name
	}
	return d, m.readCalls(d)
}

// readCalls has the core of d's machine say what it has run.
func (m *Model) readCalls(d *callsDialog) tea.Cmd {
	m.callsJobs++
	d.job = "calls-" + strconv.Itoa(m.callsJobs)
	return m.doOn(d.mc, core.ReadCalls{Job: d.job})
}

// onCalls takes what core said for the calls dialog's read asked last,
// and asks again in a while, while the dialog is open.
func (m *Model) onCalls(ev core.CallsMsg) tea.Cmd {
	d, ok := m.dialog.(*callsDialog)
	if !ok || ev.Job != d.job {
		return nil
	}
	d.calls, d.since = core.CallsOf(ev.Calls), ev.Since
	if d.opened == nil {
		d.opened = d.calls
	}
	job := d.job
	return tea.Tick(callsEvery, func(time.Time) tea.Msg { return callsAgainMsg{job: job} })
}

// callsAgain asks again for the calls of the dialog still open of job.
func (m *Model) callsAgain(msg callsAgainMsg) tea.Cmd {
	d, ok := m.dialog.(*callsDialog)
	if !ok || d.job != msg.job || m.fleet.Named(d.mc.Name) != d.mc {
		return nil
	}
	return m.readCalls(d)
}

func (d *callsDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		d.move(m, -1)
	case "down", "j":
		d.move(m, 1)
	default:
		m.dialog = nil
	}
	return nil
}

// move picks the row by above or below the one picked.
func (d *callsDialog) move(m *Model, by int) {
	names := []string{}
	for _, r := range d.mc.Core.CallRows(d.calls) {
		names = append(names, r.Name)
	}
	names = append(names, core.AllCalls)
	at := len(names) - 1
	for i, n := range names {
		if n == d.pick {
			at = i
		}
	}
	d.pick = names[max(0, min(len(names)-1, at+by))]
}

func (d *callsDialog) view(m *Model, w, h int) string {
	calls, since := d.calls, d.since
	rows := d.mc.Core.CallRows(calls)
	nameW := len("tab")
	for _, r := range rows {
		nameW = min(max(nameW, len([]rune(r.Name))), 30)
	}
	line := func(name string, git, gh, others string) string {
		return padTo(core.TruncName(name, nameW), nameW+2) + fmt.Sprintf("%7s %7s %7s", git, gh, others)
	}
	row := func(r core.CallRow) string {
		s := line(r.Name, fmt.Sprint(r.Git), fmt.Sprint(r.Gh), fmt.Sprint(r.Others))
		if r.Name == d.pick {
			return "❯ " + lipgloss.NewStyle().Bold(true).Render(s)
		}
		name := r.Name
		return "  " + m.onClick(s, func(m *Model) tea.Cmd { d.pick = name; return nil })
	}
	lines := []string{"  " + dim(line("tab", "git", "gh/glab", "other"))}
	all := core.CallRow{Name: core.AllCalls, Cmds: map[string]int{}}
	picked := &all
	for i := range rows {
		r := rows[i]
		lines = append(lines, row(r))
		all.Git, all.Gh, all.Others = all.Git+r.Git, all.Gh+r.Gh, all.Others+r.Others
		for c, n := range r.Cmds {
			all.Cmds[c] += n
		}
		if r.Name == d.pick {
			picked = &rows[i]
		}
	}
	switch {
	case calls == nil:
		lines = append(lines, "  "+dim("(asking "+machineLabel(d.mc)+"'s core…)"))
	case len(rows) == 0:
		lines = append(lines, "  "+dim("(nothing run yet)"))
	}
	if picked == &all {
		d.pick = core.AllCalls // a tab with no calls, or closed
	}
	lines = append(lines, "  "+dim(strings.Repeat("─", nameW+2+24)), row(all))
	// By the wall clock, as the time it is shown with: the monotonic one
	// stands still while a Mac sleeps.
	took := time.Now().Round(0).Sub(since.Round(0))
	at := since.Format("15:04")
	if since.Format("2006-01-02") != time.Now().Format("2006-01-02") {
		at = since.Format("Mon 2 Jan 15:04") // not today: the time alone reads as one to come
	}
	when := fmt.Sprintf("Since nerdr started at %s, %s ago", at, core.Duration(took))
	if d.mc.Name != "" {
		when = fmt.Sprintf("On %s, since nerdr's core started there at %s, %s ago", d.mc.Name, at, core.Duration(took))
	}
	if took >= time.Minute {
		when += fmt.Sprintf(": %.1f a minute", float64(all.Total())/took.Minutes())
	}
	if calls == nil {
		when = "Asking " + machineLabel(d.mc) + "'s core what it has run"
	}
	lines = append(lines, "", dim(when+"."), "")
	// What was run since the dialog opened: "since" in the commands.
	recent := map[proc.Call]int{}
	for c, n := range calls {
		if n > d.opened[c] {
			recent[c] = n - d.opened[c]
		}
	}
	sinceOpen := map[string]int{}
	for _, r := range d.mc.Core.CallRows(recent) {
		if r.Name == picked.Name || picked.Name == core.AllCalls {
			for c, n := range r.Cmds {
				sinceOpen[c] += n
			}
		}
	}
	lines = append(lines, commandLines(*picked, sinceOpen)...)
	lines = append(lines, "")
	lines = append(lines, deniedLines(m.deniedOf(d.mc), boxInner(w))...)
	lines = append(lines, "",
		dim("elsewhere: in no open tab's worktree — the repositories, all your PRs, closed tabs."),
		dim("other: the agents' CLIs asked for their conversations, notifications."),
		dim("new: since this was opened. The counts go up as calls are made."),
		dim("turned down: refused for lack of permission, or by a sandbox such as cplt or its proxy."),
		"", m.button("↑↓", "pick a row")+"  "+m.button("esc", "close"))
	return box("git, gh and glab calls", lines, w)
}

// deniedLines lists ds, what nerdr was turned down for, the latest first,
// in w columns: what it tried, and what said no.
func deniedLines(ds []denied.Denial, w int) []string {
	if len(ds) == 0 {
		return []string{dim("Nothing turned down: no call, file or socket of nerdr's was refused.")}
	}
	out := []string{"Turned down, the latest first:"}
	for i, d := range ds {
		if i == core.DeniedShown {
			out = append(out, dim(fmt.Sprintf("  and %d more", len(ds)-i)))
			break
		}
		times := ""
		if d.N > 1 {
			times = fmt.Sprintf(" %d×", d.N)
		}
		out = append(out, "  "+dim(d.Last.Format("15:04")+times)+" "+core.TruncName(d.What, max(w-14, 10)))
		if d.Why != "" {
			out = append(out, "    "+warn(core.TruncName(d.Why, max(w-4, 10))))
		}
	}
	return out
}

// deniedOf is what nerdr was turned down for on mc, the latest first: by
// its core, as the copy has it, and, for the machine the TUI runs on, by
// the TUI in a process of its own.
func (m *Model) deniedOf(mc *fleet.Machine) []denied.Denial {
	ds := slices.Clone(mc.Core.Denied)
	if mc == m.home() && m.ownProcess() {
		ds = append(ds, denied.List()...)
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].Last.After(ds[j].Last) })
	}
	return ds
}

// ownProcess reports whether the TUI runs in a process of its own, not
// its core's: what it is turned down for itself is not its core's.
func (m *Model) ownProcess() bool { return m.home().Conn != nil }

// toastDenied says in a toast what nerdr was last turned down for, when
// that is new, on any machine: what nerdr does behind its screen fails
// quietly otherwise. Each core says how many denials it has kept
// (Core.DeniedNoted), and the TUI's own process too, when it is not its
// core's.
func (m *Model) toastDenied() {
	if m.deniedSaid == nil {
		m.deniedSaid = map[*fleet.Machine]int{}
	}
	var latest *denied.Denial
	said := func(n int, ds []denied.Denial, was int) {
		if n != was && len(ds) > 0 && (latest == nil || ds[0].Last.After(latest.Last)) {
			latest = &ds[0]
		}
	}
	live := map[*fleet.Machine]bool{}
	for _, mc := range m.fleet.Machines() {
		live[mc] = true
		n := mc.Core.DeniedNoted
		said(n, mc.Core.Denied, m.deniedSaid[mc])
		m.deniedSaid[mc] = n
	}
	for mc := range m.deniedSaid {
		if !live[mc] && mc != nil {
			delete(m.deniedSaid, mc)
		}
	}
	if m.ownProcess() {
		n := denied.Noted()
		said(n, denied.List(), m.deniedSaid[nil])
		m.deniedSaid[nil] = n // the TUI's own, by no machine
	}
	if latest != nil {
		m.say(catApprovals, "Turned down: "+latest.What+" · "+m.pk("g l"), core.InAppFor)
	}
}

// commandLines lists the commands of row r, the most run first, and how
// many of each were run since the dialog opened, of those in recent.
func commandLines(r core.CallRow, recent map[string]int) []string {
	type cmd struct {
		name string
		n    int
	}
	var cmds []cmd
	w := len("command")
	for c, n := range r.Cmds {
		cmds = append(cmds, cmd{c, n})
		w = max(w, len(c))
	}
	sort.Slice(cmds, func(i, j int) bool {
		if cmds[i].n != cmds[j].n {
			return cmds[i].n > cmds[j].n
		}
		return cmds[i].name < cmds[j].name
	})
	w = min(w, 30)
	out := []string{"By command, " + r.Name + ":", dim(padTo("  command", w+4) + fmt.Sprintf("%7s %7s", "calls", "new"))}
	if len(cmds) == 0 {
		out = append(out, dim("  (none)"))
	}
	for i, c := range cmds {
		if i == core.CallCmdsShown {
			rest := 0
			for _, c := range cmds[i:] {
				rest += c.n
			}
			out = append(out, dim(fmt.Sprintf("  and %d more, %d calls", len(cmds)-i, rest)))
			break
		}
		more := ""
		if n := recent[c.name]; n > 0 {
			more = "+" + fmt.Sprint(n)
		}
		out = append(out, padTo("  "+core.TruncName(c.name, w), w+4)+fmt.Sprintf("%7d ", c.n)+warn(fmt.Sprintf("%7s", more)))
	}
	return out
}
