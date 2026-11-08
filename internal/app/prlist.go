package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// prList is every open pull request of yours, across the repositories in
// the folder nerdr was started in — a way to get to the one that needs
// you — as F2 shows it; m shows everyone's instead (see prall.go). Picking
// one goes to the tab working on its branch; with no tab, it offers to
// open the branch's worktree, or to make one.
type prList struct {
	all     bool // everyone's open pull requests, not only yours
	loading bool
	entries []core.PREntry
	errs    []string
	cursor  int
	// want is the branches of the tab the list was opened from, by
	// repository: the cursor goes to the first of their pull requests
	// once the list has it, unless moved before.
	want map[string]string
	// news is what happened to the pull requests since you last looked,
	// by prKey; see prwatch.go.
	news map[string][]string
}

// headName is the pull request's branch as the list shows it: from a
// fork, with the fork's owner before it, as GitHub shows it.
func headName(e core.PREntry) string {
	if e.PR.Fork != "" {
		return e.PR.Fork + ":" + e.PR.Branch
	}
	return e.PR.Branch
}

// showPRs fills the lists on screen with what is known, and asks GitHub
// again when that is old or there is none.
func (m *Model) showPRs() tea.Cmd {
	m.applyPRs()
	if time.Since(m.PRWatch.At) < m.GithubEvery(core.PrWatchEvery) {
		return nil
	}
	return m.do(core.RefreshPRs{})
}

// apply installs the list, keeping the cursor on the same PR.
func (d *prList) apply(entries []core.PREntry, errs []string) {
	var repo string
	var number int
	if d.cursor < len(d.entries) {
		repo, number = d.entries[d.cursor].Repo.Path, d.entries[d.cursor].PR.Number
	}
	d.entries, d.errs = entries, errs
	d.cursor = min(d.cursor, max(len(d.entries)-1, 0))
	for i, e := range d.entries {
		if e.Repo.Path == repo && e.PR.Number == number {
			d.cursor = i
		}
	}
	for i, e := range d.entries {
		if b, ok := d.want[filepath.Clean(e.Repo.Path)]; ok && b == e.Branch() {
			d.cursor, d.want = i, nil
			break
		}
	}
}

// pointAt has the cursor go to t's pull request, now or once the list
// has it.
func (d *prList) pointAt(t *core.Tab) {
	d.want = nil
	if t == nil {
		return
	}
	d.want = map[string]string{}
	for _, w := range t.Worktrees {
		d.want[filepath.Clean(w.Repo)] = w.Branch
	}
	d.apply(d.entries, d.errs)
}

func (d *prList) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		d.cursor, d.want = max(d.cursor-1, 0), nil
	case "down", "j":
		d.cursor, d.want = min(d.cursor+1, max(len(d.entries)-1, 0)), nil
	case "r":
		if d.all {
			return m.refreshAllPRs(true)
		}
		return m.do(core.RefreshPRs{})
	case "m":
		d.all = !d.all
		m.applyPRs()
		if d.all {
			return m.refreshAllPRs(false)
		}
		return m.showPRs()
	case "o":
		if d.cursor < len(d.entries) {
			m.openURL(d.entries[d.cursor].PR.URL)
		}
	case "t":
		if d.cursor < len(d.entries) {
			m.dialog = &prTitleDialog{e: d.entries[d.cursor]}
		}
	case "enter":
		if d.cursor < len(d.entries) {
			return m.goToPR(d.entries[d.cursor])
		}
	}
	return nil
}

// prTitleDialog shows a PR's whole title, which the list cuts to fit.
// Any key closes it.
type prTitleDialog struct{ e core.PREntry }

func (d *prTitleDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	m.dialog = nil
	return nil
}

func (d *prTitleDialog) view(m *Model, w, h int) string {
	e := d.e
	lines := wrapWords(term.PrintableLine(e.PR.Title), max(min(w-10, 90), 20))
	lines = append(lines, "", dim(term.PrintableLine(e.Repo.Name)+" · "+term.PrintableLine(headName(e))))
	if e.PR.URL != "" {
		lines = append(lines, dim(e.PR.URL))
	}
	return box(fmt.Sprintf("#%d", e.PR.Number), lines, w)
}

// goToPR goes to the tab working on e's branch. Without one it asks
// whether to open the worktree the branch is checked out in, or, when
// there is none, whether to make one.
func (m *Model) goToPR(e core.PREntry) tea.Cmd {
	if t := m.TabOnBranch(e.Repo.Path, e.Branch()); t != nil {
		m.dialog = nil
		m.setFocus(core.FocusAgent)
		return tea.Batch(m.selectTab(m.TabIndex(t)), m.resize())
	}
	e.Wt = checkedOut(e)
	if e.Wt != "" {
		g := m.worktreeGroup(e)
		cmd := m.openWizard()
		if w, ok := m.dialog.(*wizard); ok {
			// Opened for this PR: going back closes it, not on to the
			// repositories.
			w.group, w.repoGiven = &g, true
			return tea.Batch(cmd, w.toAgent(m))
		}
		return cmd
	}
	lines := []string{
		"No worktree has " + e.Branch() + " checked out.",
		"Make one in " + m.WtRoot + ", and a tab for it?",
	}
	if e.PR.Fork != "" {
		// Its branch is not on origin: a branch of its name made here
		// would start from main, and not be the pull request at all.
		lines = []string{
			"#" + fmt.Sprint(e.PR.Number) + " is from " + e.PR.Fork + "'s fork, not a branch on origin.",
			"Fetch it as " + e.Branch() + ", and make a worktree in " + m.WtRoot + " and a tab for it?",
			"nerdr will not push it: the branch is in the fork.",
		}
	}
	m.dialog = &confirm{
		title: "#" + fmt.Sprint(e.PR.Number) + " " + headName(e),
		lines: lines,
		yes: func(m *Model) tea.Cmd {
			if e.PR.Fork == "" {
				return m.openPRWizard(e)
			}
			m.toast("fetching #" + fmt.Sprint(e.PR.Number) + " from origin…")
			return func() tea.Msg {
				return forkFetchedMsg{e: e, err: workspace.FetchForkPR(e.Repo.Path, e.PR.Number, e.Branch())}
			}
		},
	}
	return nil
}

// forkFetchedMsg is a pull request from a fork fetched into its local
// branch, for a tab to be made on it.
type forkFetchedMsg struct {
	w   *wizard // the new-tab dialog it was picked in, nil from F2
	e   core.PREntry
	err error
}

func (m *Model) onForkFetched(msg forkFetchedMsg) tea.Cmd {
	if msg.w != nil {
		w, ok := m.dialog.(*wizard)
		if !ok || w != msg.w {
			return nil // closed since; the branch is fetched for next time
		}
		w.busy = ""
		if msg.err != nil {
			w.err = msg.err.Error()
			return nil
		}
		return w.continuePR(m, msg.e)
	}
	if msg.err != nil {
		m.toast(msg.err.Error())
		return nil
	}
	return m.openPRWizard(msg.e)
}

// openPRWizard starts the new-tab steps on e's branch, continuing it.
func (m *Model) openPRWizard(e core.PREntry) tea.Cmd {
	cmd := m.openWizardFor(e.Repo.Path)
	if w, ok := m.dialog.(*wizard); ok {
		w.branch.SetValue(e.Branch())
		w.branch.CursorEnd()
		w.useExisting = true
	}
	return cmd
}

// checkedOut is the worktree with e's branch checked out, as git says
// now: the list's, from when GitHub was last asked, can be minutes old,
// from before a worktree was made or removed. The list's when git cannot
// say.
func checkedOut(e core.PREntry) string {
	wts, err := git.ListWorktrees(e.Repo.Path)
	if err != nil {
		return e.Wt
	}
	for _, w := range wts {
		if w.Branch == e.Branch() {
			return w.Path
		}
	}
	return ""
}

// worktreeGroup is what a tab on e's worktree opens: the folder nerdr made
// it in, with the other repositories' worktrees beside it when the branch
// spans several, or the worktree alone when nerdr did not make it.
func (m *Model) worktreeGroup(e core.PREntry) workspace.Group {
	for _, g := range m.ClosedGroups() {
		for _, w := range g.Worktrees {
			if filepath.Clean(w.Path) == filepath.Clean(e.Wt) {
				return g
			}
		}
	}
	return workspace.Group{
		Name:      e.Branch(),
		Dir:       e.Wt,
		Worktrees: []workspace.Worktree{{Repo: e.Repo.Path, Path: e.Wt, Branch: e.Branch()}},
	}
}

// Where a PR's branch is, as the list's last glyph column says it.
func (m *Model) prWhere(e core.PREntry) string {
	switch {
	case m.TabOnBranch(e.Repo.Path, e.Branch()) != nil:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("tab")
	case e.Wt != "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Render("disk")
	}
	return dim("·")
}

const (
	prColRepo  = 16
	prColNum   = 7
	prColGlyph = 8
	prColWhere = 6
	prColDate  = 12
)

// prColBranch is how wide the branch column is: narrower when the title
// already has little room.
func prColBranch(wide bool) int {
	if wide {
		return 26
	}
	return 18
}

// prListHeader is the columns' names, over the rows' cursor column; the
// date is left out when it is not wide.
func prListHeader(wide bool) string {
	date := ""
	if wide {
		date = padTo("updated", prColDate)
	}
	return "  " + padTo("pr", prColNum) + padTo("repo", prColRepo) + padTo("branch", prColBranch(wide)) +
		padTo("review", prColGlyph) + padTo("ci", 4) + padTo("where", prColWhere) + date + "title"
}

// prColAuthor is how wide the column of who opened it is, in the list of
// everyone's.
const prColAuthor = 14

// askedMark is before the title of a pull request your review is asked for.
var askedMark = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render("● review asked")

// table is the list inner columns wide, at most rows of it, scrolled to
// keep the cursor in view; with what went wrong and what the marks mean
// below it.
func (d *prList) table(m *Model, inner, rows int) []string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	// Narrow, as in the review panel, the title needs the date's room.
	wide := inner >= 100
	header := prListHeader(wide)
	if d.all {
		header = strings.Replace(header, "title", padTo("by", prColAuthor)+"title", 1)
	}
	lines := []string{dim(ansi.Truncate(header, inner, "…"))}
	top := min(max(d.cursor-rows/2, 0), max(len(d.entries)-rows, 0))
	for i := top; i < len(d.entries) && i < top+rows; i++ {
		e := d.entries[i]
		rev, ci := review.PRGlyphs(e.PR)
		when := ""
		if wide {
			when = padTo("", prColDate)
			if !e.PR.UpdatedAt.IsZero() {
				when = padTo(dim(e.PR.UpdatedAt.Format("2006-01-02")), prColDate)
			}
		}
		title := term.PrintableLine(e.PR.Title)
		if news := d.news[core.PrKey(e.Repo.Path, e.PR.Number)]; len(news) > 0 {
			// Before the title, so a long one is what gets cut. An approval
			// is the review column's ✓, with who in its tip.
			var said []string
			for _, n := range news {
				if !strings.HasPrefix(n, "approved by ") {
					said = append(said, n)
				}
			}
			mark := "◆ " + term.PrintableLine(strings.Join(said, " · "))
			if len(said) == 0 {
				mark = "◆"
			}
			title = warn(mark) + "  " + title
		}
		if e.Asked {
			title = askedMark + "  " + title
		}
		if d.all {
			title = padTo(ansi.Truncate(term.PrintableLine(e.PR.Author), prColAuthor-1, "…"), prColAuthor) + title
		}
		var tip []string
		for _, s := range []struct{ state, what string }{{"APPROVED", "approved by "}, {"CHANGES_REQUESTED", "changes requested by "}} {
			if who := review.Reviewers(e.PR, s.state); len(who) > 0 {
				tip = append(tip, s.what+strings.Join(who, ", "))
			}
		}
		rev = m.tip(rev, strings.Join(tip, " · "))
		row := padTo(fmt.Sprintf("#%d", e.PR.Number), prColNum) +
			padTo(ansi.Truncate(term.PrintableLine(e.Repo.Name), prColRepo-1, "…"), prColRepo) +
			padTo(ansi.Truncate(term.PrintableLine(headName(e)), prColBranch(wide)-1, "…"), prColBranch(wide)) +
			padTo(rev, prColGlyph) + padTo(ci, 4) + padTo(m.prWhere(e), prColWhere) + when + title
		row = ansi.Truncate(cursorLine(i == d.cursor, row, sel), inner, "…")
		lines = append(lines, m.onClick(row, func(m *Model) tea.Cmd {
			d.cursor, d.want = i, nil
			if d == m.prPanel {
				return m.openPRInPanel(e)
			}
			return m.goToPR(e)
		}))
	}
	switch {
	case d.loading && len(d.entries) == 0:
		lines = append(lines, dim("asking gh…"))
	case len(d.entries) == 0 && len(d.errs) == 0 && d.all:
		lines = append(lines, dim("(no open pull requests)"))
	case len(d.entries) == 0 && len(d.errs) == 0:
		lines = append(lines, dim("(no open pull requests of yours)"))
	}
	for _, e := range d.errs {
		lines = append(lines, dim("("+ansi.Truncate(term.PrintableLine(e), inner-2, "…")+")"))
	}
	// What the where column says, and what enter does about it.
	legend := "where — tab: open in a tab · disk: in a worktree, no tab yet · ·: no worktree yet (enter makes one)"
	if d.all {
		legend += " · owner:branch is from a fork"
	}
	if len(d.news) > 0 {
		legend += " · ◆ new since you last looked"
	}
	return append(lines, "", dim(ansi.Truncate(legend, inner, "…")))
}
