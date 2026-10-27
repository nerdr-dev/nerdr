package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A tab's repositories can be added to and taken away after it is made
// (the prefix and i r). A new one gets a worktree on the tab's branch, in the
// tab's folder under its name, as a tab made over several repositories
// has them. A tab with one repository has its worktree as its folder: it
// is moved into the folder under its name first, which stops what runs
// in it, and its agents are started again on their conversations. The
// folder keeps its path, and with it its name, which may no longer say
// what is in it: the agents find their conversations by it.
//
// Taking one away deletes its worktree and keeps its branch. The last one
// stays: a tab has a repository as long as it is open. With one left, the
// tab goes in that repository's group, its layout as it is.

// repoDialog is the tab's repositories, ticked, among those there are to
// pick from.
type repoDialog struct {
	t      *tab
	repos  []workspace.Repo
	picked map[string]bool // by repository path
	cursor int
	// change is what was picked, once git has been asked about it; the
	// dialog then asks whether to go ahead.
	change *repoChange
	busy   string
	err    string
}

// repoChange is the tab's repositories to add and worktrees to delete.
type repoChange struct {
	add             workspace.Plan
	remove          []workspace.Worktree
	dirty, unpushed []string
	// nest is set when the tab's one worktree is its folder, to be moved
	// into it before another goes beside it.
	nest bool
}

// repoCheckedMsg is what git said about the change d asked for.
type repoCheckedMsg struct {
	d      *repoDialog
	change *repoChange
	err    error
}

// reposChangedMsg is the tab's worktrees once the change was made, as far
// as it went: err says where it stopped.
type reposChangedMsg struct {
	t        *tab
	wts      []workspace.Worktree
	added    []workspace.Worktree
	removed  []workspace.Worktree
	nested   *workspace.Worktree
	restart  []*tab
	warnings []string
	err      error
}

// openRepos opens the dialog for the active tab's repositories.
func (m *Model) openRepos() tea.Cmd {
	t := m.cur()
	if why := m.reposOff(t); why != "" {
		m.toast(why)
		return nil
	}
	repos, err := workspace.ScanAll(m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs)
	d := &repoDialog{t: t, picked: map[string]bool{}}
	if err != nil {
		d.err = err.Error()
	}
	have := map[string]bool{}
	for _, r := range repos {
		have[filepath.Clean(r.Path)] = true
	}
	for _, w := range t.Worktrees {
		d.picked[filepath.Clean(w.Repo)] = true
		if !have[filepath.Clean(w.Repo)] {
			// From a folder no longer scanned: still the tab's.
			repos = append(repos, workspace.Repo{Name: filepath.Base(w.Repo), Path: filepath.Clean(w.Repo)})
		}
	}
	for i := range repos {
		repos[i].Path = filepath.Clean(repos[i].Path)
	}
	d.repos = repos
	m.dialog = d
	return nil
}

// reposOff says why t's repositories cannot be changed now, "" when they
// can.
func (m *Model) reposOff(t *tab) string {
	if t != nil {
		t = t.owner()
	}
	switch {
	case t == nil:
		return "no tab open"
	case len(t.Worktrees) == 0:
		return "the tab is in a folder, not in worktrees nerdr made"
	case t.reposBusy:
		return "the tab's repositories are being changed"
	case t.Worktrees[0].Branch == "":
		return "the tab's worktree has no branch checked out to put the new ones on"
	}
	for _, a := range t.agentsIn() {
		if a.review != nil {
			return "not while a code review runs in the tab"
		}
	}
	return ""
}

// reposHold reports, and says so, whether t's tab is having its
// repositories changed: what would start in its worktrees, close them or
// stop its agents waits until that is done.
func (m *Model) reposHold(t *tab) bool {
	if t == nil || !t.owner().reposBusy {
		return false
	}
	m.toast(t.owner().Name + ": wait until its repositories are changed")
	return true
}

// pickedRepos is the repositories ticked, in the list's order.
func (d *repoDialog) pickedRepos() []workspace.Repo {
	var out []workspace.Repo
	for _, r := range d.repos {
		if d.picked[r.Path] {
			out = append(out, r)
		}
	}
	return out
}

// toggle ticks or unticks the repository under the cursor; the last one
// ticked stays.
func (d *repoDialog) toggle() {
	if d.cursor < 0 || d.cursor >= len(d.repos) {
		return
	}
	p := d.repos[d.cursor].Path
	if d.picked[p] && len(d.pickedRepos()) == 1 {
		d.err = "a tab has at least one repository: close the tab to let go of the last"
		return
	}
	d.picked[p] = !d.picked[p]
	d.err = ""
}

func (d *repoDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if d.busy != "" {
		if k == "esc" || k == "ctrl+c" {
			m.dialog = nil
		}
		return nil
	}
	if c := d.change; c != nil {
		lose := len(c.dirty) > 0
		switch k {
		case "Y":
			return d.apply(m)
		case "y", "enter":
			if !lose {
				return d.apply(m)
			}
		case "n", "N", "esc", "q":
			d.change = nil
		}
		return nil
	}
	switch k {
	case "up", "k", "shift+tab":
		d.cursor = max(d.cursor-1, 0)
	case "down", "j", "tab":
		d.cursor = min(d.cursor+1, len(d.repos)-1)
	case " ", "x":
		d.toggle()
	case "enter":
		return d.check(m)
	case "esc", "q", "ctrl+c":
		m.dialog = nil
	}
	return nil
}

// check asks git, in the background, how the repositories picked would be
// added and what the ones let go of hold that is not saved.
func (d *repoDialog) check(m *Model) tea.Cmd {
	t := d.t
	if m.tabIndex(t) < 0 {
		m.dialog = nil
		return nil
	}
	if why := m.reposOff(t); why != "" {
		d.err = why
		return nil
	}
	cur := map[string]bool{}
	for _, w := range t.Worktrees {
		cur[filepath.Clean(w.Repo)] = true
	}
	var add []workspace.Repo
	for _, r := range d.pickedRepos() {
		if !cur[r.Path] {
			add = append(add, r)
		}
	}
	var remove []workspace.Worktree
	for _, w := range t.Worktrees {
		if !d.picked[filepath.Clean(w.Repo)] {
			remove = append(remove, w)
		}
	}
	if len(add) == 0 && len(remove) == 0 {
		m.dialog = nil
		return nil
	}
	nest := len(add) > 0 && len(t.Worktrees) == 1 && samePath(t.Worktrees[0].Path, t.Dir)
	dir, branch := t.Dir, t.Worktrees[0].Branch
	d.busy, d.err = "checking…", ""
	return func() tea.Msg {
		c := &repoChange{remove: remove, nest: nest}
		if len(add) > 0 {
			plan, err := workspace.PlanAdd(dir, branch, add)
			if err != nil {
				return repoCheckedMsg{d: d, err: err}
			}
			c.add = plan
		}
		c.dirty, c.unpushed = workspace.Dirty(remove), workspace.Unpushed(remove)
		return repoCheckedMsg{d: d, change: c}
	}
}

// onChecked takes what git said, and asks whether to go ahead.
func (d *repoDialog) onChecked(msg repoCheckedMsg) {
	d.busy = ""
	if msg.err != nil {
		d.err = msg.err.Error()
		return
	}
	d.change = msg.change
}

// apply makes the change, once the user has allowed the new repositories'
// setup. The dialog closes: the tab says how it went.
func (d *repoDialog) apply(m *Model) tea.Cmd {
	c, t := d.change, d.t
	m.dialog = nil
	if m.tabIndex(t) < 0 {
		return nil
	}
	if len(c.add.Items) == 0 {
		return m.changeRepos(t, c)
	}
	var repos []string
	for _, it := range c.add.Items {
		repos = append(repos, it.Repo.Path)
	}
	return m.askRepoSetup(repos, func(m *Model) tea.Cmd {
		m.dialog = nil
		return m.changeRepos(t, c)
	})
}

// changeRepos makes change c to t's worktrees in the background. A tab
// whose worktree is moved has every program in it stopped first: its
// agents, started again once it is done, and its terminals, closed.
func (m *Model) changeRepos(t *tab, c *repoChange) tea.Cmd {
	if m.tabIndex(t) < 0 {
		return nil
	}
	if why := m.reposOff(t); why != "" {
		// Changed or closed since the dialog asked git: what it found
		// may no longer be so.
		m.toast(t.Name + ": " + why)
		return nil
	}
	t.reposBusy = true
	var progs []*term.Pane
	var restart []*tab
	if c.nest {
		for _, a := range append([]*tab{t}, t.extraAgents()...) {
			if a.pane != nil {
				progs = append(progs, a.pane)
				a.pane = nil
			}
			a.status = stStarting
			restart = append(restart, a)
		}
		for _, p := range append([]*paneTab{}, t.panes()...) {
			if p.kind == paneShell {
				if p.shell != nil {
					progs = append(progs, p.shell)
				}
				m.dropPane(t, p)
			}
		}
		m.scrolling = false
		m.resizePanes()
	}
	m.toast(t.Name + ": " + c.doing())
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	allowed := setupAllowedFile()
	force := len(c.dirty) > 0
	remove := append([]workspace.Worktree(nil), c.remove...)
	return func() tea.Msg {
		for _, p := range progs {
			p.Close()
		}
		out := reposChangedMsg{t: t, wts: wts, restart: restart}
		if c.nest {
			w, err := workspace.Nest(wts[0])
			if err != nil {
				out.err = err
				return out
			}
			// One let go of in the same change is deleted where it is now.
			for i := range remove {
				if samePath(remove[i].Path, wts[0].Path) {
					remove[i] = w
				}
			}
			out.wts[0], out.nested = w, &w
		}
		if len(c.add.Items) > 0 {
			plan := c.add
			warnings, err := plan.Sync()
			out.warnings = append(out.warnings, warnings...)
			var made []workspace.Worktree
			if err == nil {
				made, err = plan.Execute()
			}
			if err != nil {
				out.err = err
				return out
			}
			out.warnings = append(out.warnings, workspace.Prepare(made, plan.SetupIn(), allowed, func(string) {})...)
			out.wts = append(out.wts, made...)
			out.added = made
		}
		for _, w := range remove {
			if err := workspace.Remove("", []workspace.Worktree{w}, force); err != nil {
				out.err = err
				continue
			}
			out.wts = slices.DeleteFunc(out.wts, func(x workspace.Worktree) bool { return samePath(x.Path, w.Path) })
			out.removed = append(out.removed, w)
		}
		return out
	}
}

// onReposChanged puts t's worktrees as they are now in place, and brings
// what shows them up to date: the group, the review panel's screens, the
// skills, and its agents, which are told.
func (m *Model) onReposChanged(msg reposChangedMsg) tea.Cmd {
	t := msg.t
	t.reposBusy = false
	var cmds []tea.Cmd
	live := m.tabIndex(t) >= 0
	for _, a := range msg.restart {
		if live && m.live(a) {
			cmds = append(cmds, m.start(a, true))
		}
	}
	if !live {
		return tea.Batch(cmds...)
	}
	before := len(t.Worktrees)
	t.Worktrees = msg.wts
	trustWorktrees(t)
	for _, a := range t.extraAgents() {
		a.Worktrees = t.Worktrees
	}
	switch {
	case len(t.Worktrees) > 1 && before < 2:
		t.Group = m.rememberGroup(t.Dir, "", t.Worktrees)
	case len(t.Worktrees) < 2:
		t.Group = ""
	}
	// F1 is pinned to the worktrees again, F2 shows their branches, and
	// F4 looks for the skills where the agent starts.
	t.info.stale, t.info.stalePR = true, true
	if p := m.branchPanel; p != nil && p.t == t && p.busy == "" {
		m.branchPanel = nil
	}
	m.skills.key = ""
	m.installProjectSkills(t)
	if note := msg.note(t); note != "" {
		for _, a := range append([]*tab{t}, t.extraAgents()...) {
			m.addMessage(a, note, true)
		}
	}
	m.saveSession()
	m.findOverlaps()
	m.resizePanes()
	m.reportHerdr() // its group may have changed
	switch {
	case msg.err != nil:
		m.toast(t.Name + ": " + msg.err.Error())
	case len(msg.warnings) > 0:
		m.toast(t.Name + ": " + strings.Join(msg.warnings, "; "))
	default:
		m.toast(t.Name + ": " + msg.done())
	}
	cmds = append(cmds, m.pinReview(), m.syncPRPanel(), m.refreshInfo(), m.resize())
	return tea.Batch(cmds...)
}

// doing says what c is about to do.
func (c *repoChange) doing() string {
	var parts []string
	if c.nest {
		parts = append(parts, "moving the worktree into the tab's folder")
	}
	if n := len(c.add.Items); n > 0 {
		parts = append(parts, fmt.Sprintf("adding %d repositor%s", n, yIes(n)))
	}
	if n := len(c.remove); n > 0 {
		parts = append(parts, fmt.Sprintf("deleting %d worktree%s", n, pluralS(n)))
	}
	return strings.Join(parts, ", ") + "…"
}

// done says what the change did.
func (msg reposChangedMsg) done() string {
	var parts []string
	for _, w := range msg.added {
		parts = append(parts, "added "+filepath.Base(w.Repo))
	}
	for _, w := range msg.removed {
		parts = append(parts, "removed "+filepath.Base(w.Repo)+", its branch kept")
	}
	if len(msg.restart) > 0 {
		parts = append(parts, "the agent"+pluralS(len(msg.restart))+" started again")
	}
	return strings.Join(parts, "; ")
}

// note is what t's agents are told of the change: it cannot see it
// otherwise, and its NERDR_WORKTREES is as it was when it started.
func (msg reposChangedMsg) note(t *tab) string {
	if len(msg.added) == 0 && len(msg.removed) == 0 && msg.nested == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("nerdr: the repositories of this tab have changed.")
	if w := msg.nested; w != nil {
		fmt.Fprintf(&b, " The worktree of %s moved to %s; you now start in %s, the folder above it.", filepath.Base(w.Repo), w.Path, t.Dir)
	}
	for _, w := range msg.added {
		fmt.Fprintf(&b, " Added %s: its worktree is %s, on branch %s.", filepath.Base(w.Repo), w.Path, w.Branch)
	}
	for _, w := range msg.removed {
		fmt.Fprintf(&b, " Removed %s: its worktree %s is deleted, its branch %s kept.", filepath.Base(w.Repo), w.Path, w.Branch)
	}
	b.WriteString(" The tab's worktrees are now:")
	for _, w := range t.Worktrees {
		b.WriteString(" " + w.Path)
	}
	b.WriteString(". Nothing to do about it unless your task needs it.")
	return b.String()
}

func yIes(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func (d *repoDialog) view(m *Model, w, h int) string {
	t := d.t
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	if c := d.change; c != nil {
		return box("Repositories of "+t.Name, d.changeLines(m, c), w)
	}
	cur := map[string]bool{}
	for _, wt := range t.Worktrees {
		cur[filepath.Clean(wt.Repo)] = true
	}
	var rows []string
	for i, r := range d.repos {
		mark := "[ ] "
		if d.picked[r.Path] {
			mark = "[x] "
		}
		label := mark + r.Name
		switch {
		case cur[r.Path] && !d.picked[r.Path]:
			label += warn("  removed")
		case !cur[r.Path] && d.picked[r.Path]:
			label += dim("  added")
		}
		i := i
		rows = append(rows, m.onClick(cursorLine(i == d.cursor, label, sel), func(m *Model) tea.Cmd {
			d.cursor = i
			d.toggle()
			return nil
		}))
	}
	lines := []string{
		"Tick the repositories the tab works in. A new one gets a worktree",
		"on " + t.Worktrees[0].Branch + " in " + dim(t.Dir) + "; one let go of has its",
		"worktree deleted, its branch kept.", "",
	}
	if len(rows) > 0 {
		lines = append(lines, scrollRows(rows, d.cursor, min(len(rows)+2, max(h-14, 5)))...)
	}
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	if d.busy != "" {
		lines = append(lines, "", dim(d.busy))
	}
	lines = append(lines, "", m.keys("", "↑↓ choose", " ", "tick", "enter", "go on", "esc", "cancel"))
	return box("Repositories of "+t.Name, lines, w)
}

// changeLines asks whether to make change c.
func (d *repoDialog) changeLines(m *Model, c *repoChange) []string {
	var lines []string
	if c.nest {
		lines = append(lines,
			"The worktree moves into "+dim(d.t.Dir)+" under its repository's name,",
			"so the new ones can go beside it. Its agents stop and start again on",
			"their conversations; its terminals close.", "")
	}
	if len(c.add.Items) > 0 {
		lines = append(lines, "Adds, on "+c.add.Branch+":")
		for _, l := range c.add.Describe() {
			lines = append(lines, "  "+l)
		}
		lines = append(lines, "")
	}
	if len(c.remove) > 0 {
		lines = append(lines, "Deletes the worktree, keeping the branch:")
		for _, w := range c.remove {
			lines = append(lines, "  "+filepath.Base(w.Repo)+"  "+dim(w.Path))
		}
		if len(c.dirty) > 0 {
			lines = append(lines, warn("uncommitted changes in: "+strings.Join(c.dirty, ", ")+" — they will be lost"))
		}
		if len(c.unpushed) > 0 {
			lines = append(lines, dim("commits not pushed in: "+strings.Join(c.unpushed, ", ")+" — kept in the branch"))
		}
		lines = append(lines, "")
	}
	if len(c.dirty) > 0 {
		return append(lines, m.button("Y", "go ahead anyway")+"   "+m.button("n", "back"))
	}
	return append(lines, m.button("y", "go ahead")+dim(" (enter)")+"   "+m.button("n", "back"))
}
