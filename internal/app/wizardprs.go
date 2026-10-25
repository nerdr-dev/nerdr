package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The new-tab dialog lists, below the branch name, the open pull requests
// of the one repository picked, whoever opened them, those your review is
// asked for marked and first: a tab is as often made to carry on, or look
// at, work that is there as to start some. Those a tab is open on are left
// out: there is no new tab to make for them. What is typed as the branch
// narrows the list. Picking one opens the worktree it is checked out in,
// or goes on to its branch, continued, a fork's fetched first.
//
// The list costs one gh pr list, and the search for review requests,
// when the repository is picked; what the F2 list asked lately is used
// instead. It is what a typed name is checked against too, for a fork's
// branch: a new branch of its name would not be the pull request.

// wizardPRRows is how many pull requests the dialog shows at once; ↑ and
// ↓ bring the rest into view.
const wizardPRRows = 8

// wizardPRs is the pull requests the new-tab dialog lists.
type wizardPRs struct {
	repo    string // the repository's path they are of, "" for none
	entries []prEntry
	loading bool
	loaded  bool
	err     string
	cursor  int // in shown
}

// wizardPRsMsg is the open pull requests of repo, asked for w.
type wizardPRsMsg struct {
	w        *wizard
	repo     string
	got      repoPRs
	asked    map[string]bool
	askedErr string
}

// loadPRs has the dialog list the pull requests of the repository picked,
// when one is: those F2's list knows of, if it asked lately, or else
// GitHub's.
func (w *wizard) loadPRs(m *Model) tea.Cmd {
	picked := w.pickedRepos()
	if len(picked) != 1 || !github.Available() {
		w.pulls = wizardPRs{}
		return nil
	}
	r := picked[0]
	if w.pulls.repo == r.Path && (w.pulls.loaded || w.pulls.loading) {
		return nil
	}
	w.pulls = wizardPRs{repo: r.Path}
	if got, ok := m.prAll.repos[r.Path]; ok && m.prAll.asked != nil && time.Since(got.at) < m.githubEvery(prAllEvery) {
		w.setPRs(m, got, m.prAll.asked)
		return nil
	}
	w.pulls.loading = true
	return func() tea.Msg {
		msg := wizardPRsMsg{w: w, repo: r.Path, got: fetchRepoPRs(r)}
		var err error
		if msg.asked, err = github.ReviewRequested(); err != nil {
			msg.askedErr = "review requests: " + err.Error()
		}
		return msg
	}
}

// setPRs lists got, but those a tab is open on, asked marking those your
// review is asked for.
func (w *wizard) setPRs(m *Model, got repoPRs, asked map[string]bool) {
	w.pulls.entries = nil
	for _, e := range got.marked(asked) {
		if m.tabOnBranch(e.repo.Path, e.branch()) == nil {
			w.pulls.entries = append(w.pulls.entries, e)
		}
	}
	sortAllPREntries(w.pulls.entries)
	w.pulls.loading, w.pulls.loaded, w.pulls.err = false, got.err == "", got.err
}

// onWizardPRs takes a repository's pull requests, for the dialog that
// asked and for F2's list.
func (m *Model) onWizardPRs(msg wizardPRsMsg) {
	a := &m.prAll
	if a.repos == nil {
		a.repos = map[string]repoPRs{}
	}
	a.repos[msg.repo] = msg.got
	if msg.asked != nil {
		a.asked = msg.asked
	}
	if w, ok := m.dialog.(*wizard); ok && w == msg.w && w.pulls.repo == msg.repo {
		w.setPRs(m, msg.got, a.asked)
		if msg.askedErr != "" && w.pulls.err == "" {
			w.pulls.err = msg.askedErr
		}
	}
	m.applyPRs()
}

// shownPRs is the pull requests listed, narrowed by what is typed as the
// branch: its number, branch, title or who opened it.
func (w *wizard) shownPRs() []prEntry {
	q := strings.ToLower(strings.TrimSpace(w.branch.Value()))
	if q == "" {
		return w.pulls.entries
	}
	var out []prEntry
	for _, e := range w.pulls.entries {
		hay := strings.ToLower(strings.Join([]string{"#" + strconv.Itoa(e.pr.Number), e.headName(), e.pr.Title, e.pr.Author}, " "))
		if strings.Contains(hay, q) {
			out = append(out, e)
		}
	}
	return out
}

// prsKey handles a key while the cursor is in the pull requests. Others
// go to the branch name, narrowing the list.
func (w *wizard) prsKey(m *Model, msg tea.KeyMsg) (tea.Cmd, bool) {
	shown := w.shownPRs()
	// Not j and k: letters narrow the list.
	switch msg.String() {
	case "up":
		if w.pulls.cursor == 0 {
			return w.focusField(fieldBranch), true
		}
		w.pulls.cursor--
		return nil, true
	case "down":
		if w.pulls.cursor < len(shown)-1 {
			w.pulls.cursor++
		} else if len(w.repoExisting()) > 0 {
			w.exCursor = 0
			return w.focusField(fieldExisting), true
		}
		return nil, true
	case "enter":
		if w.pulls.cursor < len(shown) {
			return w.pickPR(m, shown[w.pulls.cursor]), true
		}
		return nil, true
	case "esc", "tab", "shift+tab", "ctrl+c":
		return nil, false
	}
	cmd := w.focusField(fieldBranch)
	var typed tea.Cmd
	w.branch, typed = w.branch.Update(msg)
	w.pulls.cursor, w.useExisting = 0, false
	return tea.Batch(cmd, typed), true
}

// pickPR is enter on a pull request in the list: a tab on the worktree it
// is checked out in, or on to the branch, continued, once a fork's is
// fetched.
func (w *wizard) pickPR(m *Model, e prEntry) tea.Cmd {
	if e.wt = checkedOut(e); e.wt != "" {
		g := m.worktreeGroup(e)
		w.group, w.fromBranch, w.err = &g, true, ""
		return w.toAgent(m)
	}
	if e.pr.Fork != "" {
		w.err, w.busy = "", "fetching #"+strconv.Itoa(e.pr.Number)+" from origin…"
		return func() tea.Msg {
			return forkFetchedMsg{w: w, e: e, err: workspace.FetchForkPR(e.repo.Path, e.pr.Number, e.branch())}
		}
	}
	return w.continuePR(m, e)
}

// continuePR goes on with e's branch as the branch, continuing it.
func (w *wizard) continuePR(m *Model, e prEntry) tea.Cmd {
	w.branch.SetValue(e.branch())
	w.branch.CursorEnd()
	w.useExisting = true
	cmd := w.focusField(fieldBranch)
	return tea.Batch(cmd, w.key(m, keyMsg("enter")))
}

// forkPRNamed is the listed pull request from a fork whose branch is
// named name, and whether the list can say: it can when it has the
// repository's.
func (w *wizard) forkPRNamed(name string) (prEntry, bool, bool) {
	if !w.pulls.loaded {
		return prEntry{}, false, false
	}
	for _, e := range w.pulls.entries {
		if e.pr.Fork != "" && e.pr.Branch == name {
			return e, true, true
		}
	}
	return prEntry{}, false, true
}

// prLines draws the list, inner wide and at most rows rows of pull
// requests (at least 3), the cursor on it when it has the keys.
func (w *wizard) prLines(m *Model, inner, rows int, sel lipgloss.Style) []string {
	if w.pulls.repo == "" {
		return nil
	}
	shown := w.shownPRs()
	w.pulls.cursor = min(w.pulls.cursor, max(len(shown)-1, 0))
	head := "Or carry on with an open pull request " + dim("(↓; typing narrows it)") + ":"
	if n := askedCount(shown); n > 0 { // of those shown: the rest are filtered away
		head += " " + askedMark + dim(fmt.Sprintf(" on %d", n))
	}
	lines := []string{"", head}
	switch {
	case w.pulls.loading:
		return append(lines, dim("  asking gh…"))
	case len(w.pulls.entries) == 0 && w.pulls.err == "":
		return append(lines, dim("  (none open)"))
	case len(shown) == 0 && len(w.pulls.entries) > 0:
		lines = append(lines, dim("  (none match "+strings.TrimSpace(w.branch.Value())+")"))
	}
	on := w.field == fieldPRs
	rows = max(min(rows, wizardPRRows), 3)
	var out []string
	for i, e := range shown {
		row := padTo("#"+strconv.Itoa(e.pr.Number), prColNum) +
			padTo(m.prWhere(e), prColWhere) +
			padTo(ansi.Truncate(term.PrintableLine(e.headName()), prColBranch(true)-1, "…"), prColBranch(true)) +
			padTo(ansi.Truncate(term.PrintableLine(e.pr.Author), prColAuthor-1, "…"), prColAuthor)
		if e.asked {
			row += askedMark + "  "
		}
		row = ansi.Truncate(cursorLine(on && i == w.pulls.cursor, row+term.PrintableLine(e.pr.Title), sel), inner, "…")
		out = append(out, m.onClick(row, func(m *Model) tea.Cmd {
			w.pulls.cursor = i
			return w.pickPR(m, e)
		}))
	}
	if len(out) > rows {
		cur := 0
		if on {
			cur = w.pulls.cursor
		}
		out = scrollRows(out, cur, rows+2)
	}
	lines = append(lines, out...)
	if w.pulls.err != "" {
		lines = append(lines, dim("  ("+ansi.Truncate(term.PrintableLine(w.pulls.err), inner-4, "…")+")"))
	}
	return lines
}
