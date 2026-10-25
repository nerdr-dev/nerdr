package app

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// issueList is the open issues to pick work from, as F2 shows them: first those assigned to
// you, across the repositories nerdr finds; a key shows all open issues
// instead, and another only the current tab's repositories. Picking one
// goes to the tab working on it, or starts a tab for it, with the branch
// and the task filled in from the issue for you to read before the agent
// gets it.
type issueList struct {
	all  bool // all open issues, not only those assigned to you
	here bool // only the current tab's repositories
	// scope is the repositories listed: the current tab's, or all of them.
	scope   []workspace.Repo
	tabName string

	loading bool
	entries []issueEntry
	errs    []string
	cursor  int
	// news is the issues newly assigned to you since you last looked, by
	// issueKey.
	news map[string]bool
}

// setScope finds the repositories the list is of.
func (d *issueList) setScope(m *Model) {
	d.scope, d.tabName = nil, ""
	if t := m.cur(); d.here && t != nil {
		d.tabName = t.Name
		for _, w := range t.Worktrees {
			d.scope = append(d.scope, workspace.Repo{Name: filepath.Base(w.Repo), Path: w.Repo})
		}
		return
	}
	d.here = false
	repos, err := workspace.ScanAll(m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs)
	if err != nil {
		d.errs = []string{err.Error()}
	}
	d.scope = repos
}

// fill shows what is known of the issues in scope, keeping the cursor on
// the same issue.
func (d *issueList) fill(m *Model) {
	var cur string
	if d.cursor < len(d.entries) {
		e := d.entries[d.cursor]
		cur = issueKey(e.repo.Path, e.issue.Number)
	}
	w := &m.issueWatch
	in := map[string]bool{}
	for _, r := range d.scope {
		in[filepath.Clean(r.Path)] = true
	}
	d.entries, d.errs = nil, nil
	if d.all {
		d.loading = w.allBusy
		for _, r := range d.scope {
			got := w.all[r.Path]
			d.entries = append(d.entries, got.entries...)
			if got.err != "" {
				d.errs = append(d.errs, got.err)
			}
		}
	} else {
		d.loading = w.busy
		for _, e := range w.mine {
			if in[filepath.Clean(e.repo.Path)] {
				d.entries = append(d.entries, e)
			}
		}
		if w.mineErr != "" {
			d.errs = []string{w.mineErr}
		}
	}
	sortIssueEntries(d.entries)
	d.cursor = min(d.cursor, max(len(d.entries)-1, 0))
	for i, e := range d.entries {
		if issueKey(e.repo.Path, e.issue.Number) == cur {
			d.cursor = i
		}
	}
}

func (d *issueList) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		d.cursor = max(d.cursor-1, 0)
	case "down", "j":
		d.cursor = min(d.cursor+1, max(len(d.entries)-1, 0))
	case "r":
		if d.all {
			return m.refreshAllIssues(true)
		}
		return m.refreshIssues()
	case "a":
		d.all = !d.all
		d.fill(m)
		return m.refreshAllIssues(false)
	case "t":
		if m.cur() == nil {
			return nil
		}
		d.here = !d.here
		d.setScope(m)
		d.fill(m)
		return m.refreshAllIssues(false)
	case "o":
		if d.cursor < len(d.entries) {
			m.openURL(d.entries[d.cursor].issue.URL)
		}
	case "enter":
		if d.cursor < len(d.entries) {
			return m.goToIssue(d.entries[d.cursor])
		}
	}
	return nil
}

// goToIssue goes to the tab working on the issue; with none, it starts the
// new-tab steps in the issue's repository, the branch and the task filled
// in from the issue as typing #<number> as the branch does.
func (m *Model) goToIssue(e issueEntry) tea.Cmd {
	if t := m.tabOnIssue(e.repo.Path, e.issue.Number); t != nil {
		m.dialog = nil
		m.setFocus(focusAgent)
		return tea.Batch(m.selectTab(m.tabIndex(t)), m.resize())
	}
	cmd := m.openWizardFor(e.repo.Path)
	w, ok := m.dialog.(*wizard)
	if !ok {
		return cmd
	}
	if w.step != stepBranch {
		// The repository is not among those the wizard lists: #<number>
		// looks the issue up once one is picked.
		w.branch.SetValue("#" + strconv.Itoa(e.issue.Number))
		return cmd
	}
	w.branch.SetValue(e.issue.BranchName())
	w.branch.CursorEnd()
	return tea.Batch(cmd, w.lookUpIssue(e.repo.Path, e.issue.Number))
}

// tabOnIssue is a tab with a worktree of repo on a branch for issue
// number, as BranchName names them ("123-…"), or nil.
func (m *Model) tabOnIssue(repo string, number int) *tab {
	n := strconv.Itoa(number)
	for _, t := range m.tabs {
		for _, w := range t.Worktrees {
			if filepath.Clean(w.Repo) == filepath.Clean(repo) && (w.Branch == n || strings.HasPrefix(w.Branch, n+"-")) {
				return t
			}
		}
	}
	return nil
}

const (
	issueColRepo   = 16
	issueColNum    = 7
	issueColWhere  = 6
	issueColDate   = 12
	issueColAuthor = 14
)

// title is the list's title: which issues, where, and how many.
func (d *issueList) title() string {
	s := "Issues assigned to me"
	if d.all {
		s = "Open issues"
	}
	if d.here {
		s += " in " + d.tabName
	}
	if d.loading {
		return s + " …"
	}
	return fmt.Sprintf("%s (%d)", s, len(d.entries))
}

// table is the list inner columns wide, at most rows of it, scrolled to
// keep the cursor in view; with what went wrong and what the marks mean
// below it.
func (d *issueList) table(m *Model, inner, rows int) []string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	wide := inner >= 100
	header := "  " + padTo("repo", issueColRepo) + padTo("issue", issueColNum) + padTo("where", issueColWhere)
	if wide {
		header += padTo("updated", issueColDate) + padTo("by", issueColAuthor)
	}
	lines := []string{dim(ansi.Truncate(header+"title", inner, "…"))}
	top := min(max(d.cursor-rows/2, 0), max(len(d.entries)-rows, 0))
	for i := top; i < len(d.entries) && i < top+rows; i++ {
		e := d.entries[i]
		where := dim("·")
		if m.tabOnIssue(e.repo.Path, e.issue.Number) != nil {
			where = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("tab")
		}
		row := padTo(ansi.Truncate(e.repo.Name, issueColRepo-1, "…"), issueColRepo) +
			padTo(fmt.Sprintf("#%d", e.issue.Number), issueColNum) + padTo(where, issueColWhere)
		if wide {
			when := ""
			if !e.issue.UpdatedAt.IsZero() {
				when = dim(e.issue.UpdatedAt.Format("2006-01-02"))
			}
			row += padTo(when, issueColDate) + padTo(dim(ansi.Truncate(e.issue.Author, issueColAuthor-1, "…")), issueColAuthor)
		}
		title := e.issue.Title
		if d.news[issueKey(e.issue.Repo, e.issue.Number)] {
			title = warn("◆ new") + "  " + title
		}
		if len(e.issue.Labels) > 0 {
			title += "  " + dim("["+strings.Join(e.issue.Labels, ", ")+"]")
		}
		row = ansi.Truncate(cursorLine(i == d.cursor, row+title, sel), inner, "…")
		lines = append(lines, m.onClick(row, func(m *Model) tea.Cmd {
			d.cursor = i
			return m.goToIssue(e)
		}))
	}
	switch {
	case d.loading && len(d.entries) == 0:
		lines = append(lines, dim("asking gh…"))
	case len(d.entries) == 0 && len(d.errs) == 0 && d.all:
		lines = append(lines, dim("(no open issues)"))
	case len(d.entries) == 0 && len(d.errs) == 0:
		lines = append(lines, dim("(no open issues assigned to you)"))
	}
	for _, e := range d.errs {
		lines = append(lines, dim("("+ansi.Truncate(e, inner-2, "…")+")"))
	}
	legend := "tab: a tab works on it · ·: none yet"
	if len(d.news) > 0 {
		legend += " · ◆ newly assigned to you"
	}
	return append(lines, "", dim(ansi.Truncate(legend, inner, "…")))
}
