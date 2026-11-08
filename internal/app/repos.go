package app

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
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
	t      *core.Tab
	repos  []workspace.Repo
	picked map[string]bool // by repository path
	cursor int
	// change is what was picked, once git has been asked about it; the
	// dialog then asks whether to go ahead.
	change *core.RepoChange
	// job is the id of the check of the change, while git is asked.
	job  string
	busy string
	err  string
}

// openRepos opens the dialog for the active tab's repositories.
func (m *Model) openRepos() tea.Cmd {
	t := m.Cur()
	if why := m.ReposOff(t); why != "" {
		m.toast(why)
		return nil
	}
	d := &repoDialog{t: t, picked: map[string]bool{}}
	for _, w := range t.Worktrees {
		d.picked[filepath.Clean(w.Repo)] = true
	}
	d.scan(m)
	m.dialog = d
	return m.do(core.ScanRepos{})
}

// scan has the repositories to pick from as core last found them (see
// core.RepoScan), and the tab's own.
func (d *repoDialog) scan(m *Model) {
	repos := slices.Clone(m.Scan.Repos)
	if m.Scan.Err != "" && d.err == "" {
		d.err = m.Scan.Err
	}
	have := map[string]bool{}
	for _, r := range repos {
		have[filepath.Clean(r.Path)] = true
	}
	for _, w := range d.t.Worktrees {
		if !have[filepath.Clean(w.Repo)] {
			// From a folder no longer scanned: still the tab's.
			repos = append(repos, workspace.Repo{Name: filepath.Base(w.Repo), Path: filepath.Clean(w.Repo)})
		}
	}
	for i := range repos {
		repos[i].Path = filepath.Clean(repos[i].Path)
	}
	d.repos = repos
	d.cursor = min(d.cursor, max(len(repos)-1, 0))
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
		lose := len(c.Dirty) > 0
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
	if m.TabIndex(t) < 0 {
		m.dialog = nil
		return nil
	}
	if why := m.ReposOff(t); why != "" {
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
	d.busy, d.err, d.job = "checking…", "", core.NewJobID()
	return m.do(core.CheckRepoChange{Job: d.job, T: m.Ref(t), Add: add, Remove: remove})
}

// onRepoChecked takes what git said, and asks whether to go ahead.
func (m *Model) onRepoChecked(msg core.RepoCheckedMsg) {
	d, ok := m.dialog.(*repoDialog)
	if !ok || d.job != msg.Job {
		return
	}
	d.busy, d.job = "", ""
	if msg.Err != "" {
		d.err = msg.Err
		return
	}
	d.change = msg.Change
}

// apply has core make the change, once the user has allowed the new
// repositories' setup. The dialog closes: the tab says how it went.
func (d *repoDialog) apply(m *Model) tea.Cmd {
	m.dialog = nil
	if m.TabIndex(d.t) < 0 {
		return nil
	}
	return m.do(core.ChangeRepos{T: m.Ref(d.t), C: *d.change})
}

// reposDone is t's repositories changed by core: what shows them is
// brought up to date, and text says how it went.
func (m *Model) reposDone(t *core.Tab, text string) tea.Cmd {
	if p := m.branchPanel; p != nil && p.t == t && p.busy == "" {
		m.branchPanel = nil
	}
	m.resizePanes()
	m.reportHerdr() // its group may have changed
	m.toast(text)
	return tea.Batch(m.pinReview(), m.syncPRPanel(), m.resize())
}

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
func (d *repoDialog) changeLines(m *Model, c *core.RepoChange) []string {
	var lines []string
	if c.Nest {
		lines = append(lines,
			"The worktree moves into "+dim(d.t.Dir)+" under its repository's name,",
			"so the new ones can go beside it. Its agents stop and start again on",
			"their conversations; its terminals close.", "")
	}
	if len(c.Add.Items) > 0 {
		lines = append(lines, "Adds, on "+c.Add.Branch+":")
		for _, l := range c.Add.Describe() {
			lines = append(lines, "  "+l)
		}
		lines = append(lines, "")
	}
	if len(c.Remove) > 0 {
		lines = append(lines, "Deletes the worktree, keeping the branch:")
		for _, w := range c.Remove {
			lines = append(lines, "  "+filepath.Base(w.Repo)+"  "+dim(w.Path))
		}
		if len(c.Dirty) > 0 {
			lines = append(lines, warn("uncommitted changes in: "+strings.Join(c.Dirty, ", ")+" — they will be lost"))
		}
		if len(c.Unpushed) > 0 {
			lines = append(lines, dim("commits not pushed in: "+strings.Join(c.Unpushed, ", ")+" — kept in the branch"))
		}
		lines = append(lines, "")
	}
	if len(c.Dirty) > 0 {
		return append(lines, m.button("Y", "go ahead anyway")+"   "+m.button("n", "back"))
	}
	return append(lines, m.button("y", "go ahead")+dim(" (enter)")+"   "+m.button("n", "back"))
}

// rescanned has what lists the repositories list those core found again.
func (m *Model) rescanned() tea.Cmd {
	var cmd tea.Cmd
	switch d := m.dialog.(type) {
	case *wizard:
		cmd = d.rescanned(m)
	case *repoDialog:
		d.scan(m)
	}
	if d := m.issuePanel; d != nil && !d.here {
		d.setScope(m)
		d.fill(m)
		cmd = tea.Batch(cmd, m.refreshAllIssues(false))
	}
	return cmd
}
