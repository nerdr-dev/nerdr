package app

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// tabPanel is where a tab's branches stand, as F2 shows them above the
// pull request, with what to do about them: push, open a pull request. It
// also lists the other tabs that change the same files as this one.
type tabPanel struct {
	t    *core.Tab
	busy string
	// result is how the last push or pull request went, kept in the
	// panel: a toast is gone before a reason for doing nothing is read.
	result string
	failed bool
}

// showBranchPR is F2 on the tab's branches and its pull request, with the
// keyboard: where ctrl+q i i takes you.
func (m *Model) showBranchPR() tea.Cmd {
	if m.Cur() == nil {
		return nil
	}
	m.dialog = nil
	m.reviewOpen = true
	cmd := m.review.ShowBranchPR()
	m.setFocus(core.FocusReview)
	return tea.Batch(cmd, m.syncPRPanel(), m.resize())
}

// branchAct starts what key k does to the branches of p's tab, p a push
// and P a pull request, reporting whether k is one of those.
func (m *Model) branchAct(p *tabPanel, k string) (tea.Cmd, bool) {
	q, busy, ok := p.work(k)
	if !ok {
		return nil, false
	}
	p.busy = busy
	q.T = m.fleet.Ref(p.t)
	return m.do(q), true
}

// work is what key k has core do to the tab's branches, and what the
// panel says while it does; false for another key. The request is still
// to be given its tab.
func (p *tabPanel) work(k string) (core.BranchWork, string, bool) {
	var q core.BranchWork
	switch k {
	case "p":
		q.Push = true
		return q, "pushing…", true
	case "P":
		q.PR = true
		return q, "pushing and opening the pull request…", true
	}
	return q, "", false
}

// prDone takes the result of an action.
func (m *Model) prDone(msg core.BranchDoneMsg) tea.Cmd {
	t := m.fleet.ByRef(msg.T)
	if t == nil {
		return nil
	}
	if p := m.branchPanel; p != nil && p.t == t {
		p.busy = ""
		p.result, p.failed = msg.Note, msg.Err != ""
		if p.failed {
			p.result = msg.Err
		}
	}
	if msg.Err != "" {
		m.toast(msg.Err)
	} else if msg.Note != "" {
		m.toast(msg.Note)
	}
	var pr tea.Cmd
	if t == m.Cur() {
		pr = m.review.ReloadPR() // F2 shows the new PR at once
	}
	return tea.Batch(m.do(core.LookAgain{T: msg.T, PR: true}), pr)
}

// openURL opens url in the browser, without waiting for it; see
// internal/browser.
func (m *Model) openURL(url string) {
	if url == "" {
		return
	}
	if err := browser.Open(url); err != nil {
		m.toast(browser.Command(url)[0] + ": " + err.Error())
	}
}

// openTabPRs opens the active tab's pull requests in the browser, one for
// each branch that has one.
func (m *Model) openTabPRs() {
	t := m.Cur()
	if t == nil {
		return
	}
	prs := t.Info.Prs()
	if len(prs) == 0 {
		m.toast(t.Name + " has no pull request")
		return
	}
	for _, pr := range prs {
		m.openURL(pr.URL)
	}
}

// outcome is how the last push or pull request went, after a blank line;
// none before the first.
func (p *tabPanel) outcome() []string {
	if p.result == "" {
		return nil
	}
	show := dim
	if p.failed {
		show = warn
	}
	return []string{"", show(p.result)}
}

// branchLines is where the tab's branches stand, each with its pull
// request but the one in worktree shown (the PR F2 draws below), and the
// other tabs changing the same files.
func (p *tabPanel) branchLines(m *Model, shown string) []string {
	t := p.t
	var lines []string
	if !t.Info.Known() {
		lines = append(lines, dim("looking at the branches…"))
	}
	for i, wi := range t.Info.Wts {
		if i >= len(t.Worktrees) {
			break
		}
		wt := t.Worktrees[i]
		head := filepath.Base(wt.Repo) + "  " + keyHint(wt.Branch)
		lines = append(lines, head)
		if wi.Err != "" {
			lines = append(lines, "  "+warn(wi.Err))
			continue
		}
		s := wi.Stats
		lines = append(lines, "  "+core.Plural(len(s.Changed), "file")+" changed, +"+strconv.Itoa(wi.Add)+" −"+strconv.Itoa(wi.Del)+
			" · "+strconv.Itoa(s.Ahead)+" ahead, "+strconv.Itoa(s.Behind)+" behind "+s.Base)
		switch {
		case shown != "" && filepath.Clean(wt.Path) == filepath.Clean(shown):
			// F2 draws this one's PR below.
		case wi.HasPR:
			line := "  PR #" + strconv.Itoa(wi.Pr.Number) + " " + core.PrState(wi.Pr)
			if wi.Pr.CI != "" {
				line += " · CI " + wi.Pr.CI
			}
			url := wi.Pr.URL
			lines = append(lines, line, "  "+m.onClick(dim(url), func(*Model) tea.Cmd {
				m.openURL(url)
				return nil
			}))
		case m.machineOf(t).Core.NoGh:
			lines = append(lines, "  "+dim("no gh CLI: pull requests unknown"))
		default:
			lines = append(lines, "  "+dim("no pull request"))
		}
	}
	return append(lines, p.tabNotes(m)...)
}

// branchList is branchLines for a tab on several repositories, in F2: a
// row for each branch, with the one whose PR F2 draws below marked. A
// row clicked, or ← and → ([ and ] too), shows another one's PR there.
func (p *tabPanel) branchList(m *Model, shown string) []string {
	t := p.t
	var lines []string
	if !t.Info.Known() {
		lines = append(lines, dim("looking at the branches…"))
	}
	repoW, branchW := 0, 0
	for i, wt := range t.Worktrees {
		repoW = max(repoW, lipgloss.Width(term.PrintableLine(filepath.Base(wt.Repo))))
		branchW = max(branchW, lipgloss.Width(term.PrintableLine(p.branchOf(i))))
	}
	for i, wt := range t.Worktrees {
		sel := shown != "" && filepath.Clean(wt.Path) == filepath.Clean(shown)
		mark, branch := "  ", term.PrintableLine(p.branchOf(i))
		if sel {
			mark, branch = keyHint("▸ "), keyHint(branch)
		}
		row := mark + padTo(term.PrintableLine(filepath.Base(wt.Repo)), repoW+2) + padTo(branch, branchW+2)
		if i < len(t.Info.Wts) {
			wi := t.Info.Wts[i]
			switch {
			case wi.Err != "":
				row += warn(wi.Err)
			default:
				row += dim("+"+strconv.Itoa(wi.Add)+" −"+strconv.Itoa(wi.Del)+
					" · "+strconv.Itoa(wi.Stats.Ahead)+" ahead, "+strconv.Itoa(wi.Stats.Behind)+" behind") + "  "
				switch {
				case wi.HasPR:
					pr := "#" + strconv.Itoa(wi.Pr.Number) + " " + core.PrState(wi.Pr)
					if wi.Pr.CI != "" {
						pr += " · CI " + wi.Pr.CI
					}
					row += pr
				case !m.machineOf(t).Core.NoGh:
					row += dim("no PR")
				}
			}
		}
		if sel && m.review.OnRepoList() && m.focus == core.FocusReview {
			// The cursor is on the list: ↑ ↓ move it, as on any list.
			row = lipgloss.NewStyle().Background(navActiveBg).Render(ansi.Strip(row))
		}
		lines = append(lines, m.onClick(row, func(m *Model) tea.Cmd { return m.review.ShowPin(i) }))
	}
	return append(lines, p.tabNotes(m)...)
}

// branchOf is the branch the tab's i-th worktree has checked out now, or
// the one it was made on until that is known.
func (p *tabPanel) branchOf(i int) string {
	if i < len(p.t.Info.Wts) && p.t.Info.Wts[i].Branch != "" {
		return p.t.Info.Wts[i].Branch
	}
	return p.t.Worktrees[i].Branch
}

// tabNotes is what goes below the tab's branches: that they are merged,
// and the other tabs changing the same files.
func (p *tabPanel) tabNotes(m *Model) []string {
	t := p.t
	var lines []string
	if t.Info.Merged() {
		lines = append(lines, "", keyHint("merged")+" — closing the tab ("+m.pk("x")+") can delete the branch too.")
	}
	if ov := m.machineOf(t).Core.Overlaps[t.ID]; len(ov) > 0 {
		lines = append(lines, "", warn("Also changed in other tabs — expect merge conflicts:"))
		// The branches are enough to know what to merge with what; the
		// files would fill the screen.
		for _, o := range ov {
			lines = append(lines, "  "+keyHint(strings.Join(o.Branches(), ", "))+dim("  "+o.With.Name+" · "+core.Plural(len(o.Files), "file")))
		}
	}
	return lines
}
