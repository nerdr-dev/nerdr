package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/proc"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// callsDialog is the prefix and g: how many times nerdr has run git and
// gh for each tab since it started, by the folder they ran in, to see
// what keeps them busy; and, for the row picked with ↑ ↓, by command. It
// is drawn again with every frame, so the counts go up as calls are made.
// Any other key closes it.
type callsDialog struct {
	pick   string            // the row whose commands are shown, by name
	opened map[proc.Call]int // the counts when it opened, for what came since
}

// allCalls is the name of the row that sums the others.
const allCalls = "all"

func (m *Model) newCallsDialog() *callsDialog {
	d := &callsDialog{pick: allCalls}
	d.opened, _ = proc.Counts()
	if t := m.cur(); t != nil {
		d.pick = t.Name
	}
	return d
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
	calls, _ := proc.Counts()
	names := []string{}
	for _, r := range m.callRows(calls) {
		names = append(names, r.name)
	}
	names = append(names, allCalls)
	at := len(names) - 1
	for i, n := range names {
		if n == d.pick {
			at = i
		}
	}
	d.pick = names[max(0, min(len(names)-1, at+by))]
}

// callRow is one tab's calls, or those in no tab's worktree.
type callRow struct {
	name            string
	git, gh, others int
	cmds            map[string]int // by program and what it was asked, "git status"
}

func (r callRow) total() int { return r.git + r.gh + r.others }

// callRows sums calls by the tab whose worktree each ran in.
func (m *Model) callRows(calls map[proc.Call]int) []callRow {
	byTab := map[*tab]*callRow{}
	elsewhere := &callRow{name: "elsewhere", cmds: map[string]int{}}
	for c, n := range calls {
		t := m.tabOfDir(c.Dir)
		r := elsewhere
		if t != nil {
			if byTab[t] == nil {
				byTab[t] = &callRow{name: t.Name, cmds: map[string]int{}}
			}
			r = byTab[t]
		}
		r.cmds[strings.TrimSpace(c.Program+" "+c.Sub)] += n
		switch c.Program {
		case "git":
			r.git += n
		case "gh":
			r.gh += n
		default:
			r.others += n
		}
	}
	var out []callRow
	for _, r := range byTab {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].total() != out[j].total() {
			return out[i].total() > out[j].total()
		}
		return out[i].name < out[j].name
	})
	if elsewhere.total() > 0 {
		out = append(out, *elsewhere)
	}
	return out
}

// tabOfDir is the tab with dir in one of its worktrees, or nil.
func (m *Model) tabOfDir(dir string) *tab {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var best *tab
	bestLen := 0
	for _, t := range m.tabs {
		paths := []string{t.Dir}
		for _, w := range t.Worktrees {
			paths = append(paths, w.Path)
		}
		for _, p := range paths {
			p = filepath.Clean(p)
			if p != "." && (dir == p || strings.HasPrefix(dir, p+string(filepath.Separator))) && len(p) > bestLen {
				best, bestLen = t, len(p)
			}
		}
	}
	return best
}

func (d *callsDialog) view(m *Model, w, h int) string {
	calls, since := proc.Counts()
	rows := m.callRows(calls)
	nameW := len("tab")
	for _, r := range rows {
		nameW = min(max(nameW, len([]rune(r.name))), 30)
	}
	line := func(name string, git, gh, others string) string {
		return padTo(truncName(name, nameW), nameW+2) + fmt.Sprintf("%7s %7s %7s", git, gh, others)
	}
	row := func(r callRow) string {
		s := line(r.name, fmt.Sprint(r.git), fmt.Sprint(r.gh), fmt.Sprint(r.others))
		if r.name == d.pick {
			return "❯ " + lipgloss.NewStyle().Bold(true).Render(s)
		}
		name := r.name
		return "  " + m.onClick(s, func(m *Model) tea.Cmd { d.pick = name; return nil })
	}
	lines := []string{"  " + dim(line("tab", "git", "gh", "other"))}
	all := callRow{name: allCalls, cmds: map[string]int{}}
	picked := &all
	for i := range rows {
		r := rows[i]
		lines = append(lines, row(r))
		all.git, all.gh, all.others = all.git+r.git, all.gh+r.gh, all.others+r.others
		for c, n := range r.cmds {
			all.cmds[c] += n
		}
		if r.name == d.pick {
			picked = &rows[i]
		}
	}
	if len(rows) == 0 {
		lines = append(lines, "  "+dim("(nothing run yet)"))
	}
	if picked == &all {
		d.pick = allCalls // a tab with no calls, or closed
	}
	lines = append(lines, "  "+dim(strings.Repeat("─", nameW+2+24)), row(all))
	// By the wall clock, as the time it is shown with: the monotonic one
	// stands still while a Mac sleeps.
	took := time.Now().Round(0).Sub(since.Round(0))
	at := since.Format("15:04")
	if since.Format("2006-01-02") != time.Now().Format("2006-01-02") {
		at = since.Format("Mon 2 Jan 15:04") // not today: the time alone reads as one to come
	}
	when := fmt.Sprintf("Since nerdr started at %s, %s ago", at, duration(took))
	if took >= time.Minute {
		when += fmt.Sprintf(": %.1f a minute", float64(all.total())/took.Minutes())
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
	for _, r := range m.callRows(recent) {
		if r.name == picked.name || picked.name == allCalls {
			for c, n := range r.cmds {
				sinceOpen[c] += n
			}
		}
	}
	lines = append(lines, commandLines(*picked, sinceOpen)...)
	lines = append(lines, "")
	lines = append(lines, deniedLines(boxInner(w))...)
	lines = append(lines, "",
		dim("elsewhere: in no open tab's worktree — the repositories, all your PRs, closed tabs."),
		dim("other: the agents' CLIs asked for their conversations, notifications."),
		dim("new: since this was opened. The counts go up as calls are made."),
		dim("turned down: refused for lack of permission, or by a sandbox such as cplt or its proxy."),
		"", m.button("↑↓", "pick a row")+"  "+m.button("esc", "close"))
	return box("git and gh calls", lines, w)
}

// deniedShown is how many of the denials the dialog lists.
const deniedShown = 6

// deniedLines lists what nerdr was turned down for, the latest first, in
// w columns: what it tried, and what said no.
func deniedLines(w int) []string {
	ds := denied.List()
	if len(ds) == 0 {
		return []string{dim("Nothing turned down: no call, file or socket of nerdr's was refused.")}
	}
	out := []string{"Turned down, the latest first:"}
	for i, d := range ds {
		if i == deniedShown {
			out = append(out, dim(fmt.Sprintf("  and %d more", len(ds)-i)))
			break
		}
		times := ""
		if d.N > 1 {
			times = fmt.Sprintf(" %d×", d.N)
		}
		out = append(out, "  "+dim(d.Last.Format("15:04")+times)+" "+truncName(d.What, max(w-14, 10)))
		if d.Why != "" {
			out = append(out, "    "+warn(truncName(d.Why, max(w-4, 10))))
		}
	}
	return out
}

// toastDenied says in a toast what nerdr was last turned down for, when
// that is new: what nerdr does behind its screen fails quietly otherwise.
func (m *Model) toastDenied() {
	n := denied.Noted()
	if n == m.deniedSaid {
		return
	}
	m.deniedSaid = n
	if ds := denied.List(); len(ds) > 0 {
		m.toastText = "Turned down: " + ds[0].What + " · " + m.pk("g")
		m.toastUntil = time.Now().Add(inAppFor)
	}
}

// callCmdsShown is how many of a row's commands the dialog lists.
const callCmdsShown = 12

// commandLines lists the commands of row r, the most run first, and how
// many of each were run since the dialog opened, of those in recent.
func commandLines(r callRow, recent map[string]int) []string {
	type cmd struct {
		name string
		n    int
	}
	var cmds []cmd
	w := len("command")
	for c, n := range r.cmds {
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
	out := []string{"By command, " + r.name + ":", dim(padTo("  command", w+4) + fmt.Sprintf("%7s %7s", "calls", "new"))}
	if len(cmds) == 0 {
		out = append(out, dim("  (none)"))
	}
	for i, c := range cmds {
		if i == callCmdsShown {
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
		out = append(out, padTo("  "+truncName(c.name, w), w+4)+fmt.Sprintf("%7d ", c.n)+warn(fmt.Sprintf("%7s", more)))
	}
	return out
}

// truncName cuts name to n columns.
func truncName(name string, n int) string {
	r := []rune(name)
	if len(r) <= n {
		return name
	}
	return string(r[:n-1]) + "…"
}
