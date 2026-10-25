package app

import (
	"path/filepath"
	"strconv"
	"strings"

	"nerdr/internal/browser"
	"nerdr/internal/github"
	"nerdr/internal/term"
	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// tabPanel is where a tab's branches stand, as F2 shows them above the
// pull request, with what to do about them: push, open a pull request. It
// also lists the other tabs that change the same files as this one.
type tabPanel struct {
	t    *tab
	busy string
	// result is how the last push or pull request went, kept in the
	// panel: a toast is gone before a reason for doing nothing is read.
	result string
	failed bool
}

type prActionMsg struct {
	t    *tab
	note string
	err  string
}

// showBranchPR is F2 on the tab's branches and its pull request, with the
// keyboard: where ctrl+q i i takes you.
func (m *Model) showBranchPR() tea.Cmd {
	if m.cur() == nil {
		return nil
	}
	m.dialog = nil
	m.reviewOpen = true
	cmd := m.review.ShowBranchPR()
	m.setFocus(focusReview)
	return tea.Batch(cmd, m.syncPRPanel(), m.resize())
}

// act starts what key k does to the tab's branches, p a push and P a
// pull request, reporting whether k is one of those.
func (p *tabPanel) act(k string) (tea.Cmd, bool) {
	job, busy := p.job(k)
	if job == nil {
		return nil, false
	}
	p.busy = busy
	return func() tea.Msg { return job() }, true
}

// job is what key k does to the tab's branches, to run in the background,
// and what the panel says while it runs; nil for another key.
func (p *tabPanel) job(k string) (func() prActionMsg, string) {
	t := p.t
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	switch k {
	case "p":
		return func() prActionMsg { return pushAll(t, wts) }, "pushing…"
	case "P":
		have := map[string]bool{}
		for _, w := range t.info.wts {
			if w.hasPR && w.pr.State == "OPEN" {
				have[w.repo] = true
			}
		}
		return func() prActionMsg { return createPRs(t, wts, have) }, "pushing and opening the pull request…"
	}
	return nil, ""
}

// handle takes the result of an action.
func (m *Model) prDone(msg prActionMsg) tea.Cmd {
	if p := m.branchPanel; p != nil && p.t == msg.t {
		p.busy = ""
		p.result, p.failed = msg.note, msg.err != ""
		if p.failed {
			p.result = msg.err
		}
	}
	if msg.err != "" {
		m.toast(msg.err)
	} else if msg.note != "" {
		m.toast(msg.note)
	}
	msg.t.lookAgain(true)
	var pr tea.Cmd
	if msg.t == m.cur() {
		pr = m.review.ReloadPR() // F2 shows the new PR at once
	}
	return tea.Batch(m.refreshInfo(), pr)
}

func pushAll(t *tab, wts []workspace.Worktree) prActionMsg {
	var errs []string
	for _, w := range wts {
		if err := workspace.Push(w); err != nil {
			errs = append(errs, filepath.Base(w.Repo)+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return prActionMsg{t: t, err: "push failed: " + strings.Join(errs, "; ")}
	}
	return prActionMsg{t: t, note: "pushed " + t.Name}
}

func createPRs(t *tab, wts []workspace.Worktree, have map[string]bool) prActionMsg {
	var urls, errs []string
	for _, w := range wts {
		if have[w.Repo] {
			continue
		}
		name := filepath.Base(w.Repo)
		// A pull request is made of commits: work not yet committed is in
		// none, and gh's refusal would not say so.
		if s, err := workspace.BranchStats(w); err == nil && s.Ahead == 0 {
			why := name + ": no commits ahead of " + s.Base + " to open a pull request with"
			if n := workspace.Uncommitted(w); n > 0 {
				why += " — " + plural(n, "file") + " changed but not committed: commit them first"
			}
			errs = append(errs, why)
			continue
		}
		if err := workspace.Push(w); err != nil {
			errs = append(errs, name+": push: "+err.Error())
			continue
		}
		url, err := github.CreatePR(w.Path, w.Origin)
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		urls = append(urls, url)
	}
	switch {
	case len(errs) > 0:
		return prActionMsg{t: t, err: strings.Join(errs, "; ")}
	case len(urls) == 0:
		return prActionMsg{t: t, note: t.Name + " already has its pull request"}
	}
	return prActionMsg{t: t, note: "opened " + strings.Join(urls, ", ")}
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
	if !t.info.known() {
		lines = append(lines, dim("looking at the branches…"))
	}
	for i, wi := range t.info.wts {
		if i >= len(t.Worktrees) {
			break
		}
		wt := t.Worktrees[i]
		head := filepath.Base(wt.Repo) + "  " + keyHint(wt.Branch)
		lines = append(lines, head)
		if wi.err != "" {
			lines = append(lines, "  "+warn(wi.err))
			continue
		}
		s := wi.stats
		lines = append(lines, "  "+plural(len(s.Changed), "file")+" changed, +"+strconv.Itoa(wi.add)+" −"+strconv.Itoa(wi.del)+
			" · "+strconv.Itoa(s.Ahead)+" ahead, "+strconv.Itoa(s.Behind)+" behind "+s.Base)
		switch {
		case shown != "" && filepath.Clean(wt.Path) == filepath.Clean(shown):
			// F2 draws this one's PR below.
		case wi.hasPR:
			line := "  PR #" + strconv.Itoa(wi.pr.Number) + " " + strings.ToLower(wi.pr.State)
			if wi.pr.CI != "" {
				line += " · CI " + wi.pr.CI
			}
			url := wi.pr.URL
			lines = append(lines, line, "  "+m.onClick(dim(url), func(*Model) tea.Cmd {
				m.openURL(url)
				return nil
			}))
		case !github.Available():
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
	if !t.info.known() {
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
		if i < len(t.info.wts) {
			wi := t.info.wts[i]
			switch {
			case wi.err != "":
				row += warn(wi.err)
			default:
				row += dim("+"+strconv.Itoa(wi.add)+" −"+strconv.Itoa(wi.del)+
					" · "+strconv.Itoa(wi.stats.Ahead)+" ahead, "+strconv.Itoa(wi.stats.Behind)+" behind") + "  "
				switch {
				case wi.hasPR:
					pr := "#" + strconv.Itoa(wi.pr.Number) + " " + strings.ToLower(wi.pr.State)
					if wi.pr.CI != "" {
						pr += " · CI " + wi.pr.CI
					}
					row += pr
				case github.Available():
					row += dim("no PR")
				}
			}
		}
		if sel && m.review.OnRepoList() && m.focus == focusReview {
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
	if i < len(p.t.info.wts) && p.t.info.wts[i].branch != "" {
		return p.t.info.wts[i].branch
	}
	return p.t.Worktrees[i].Branch
}

// tabNotes is what goes below the tab's branches: that they are merged,
// and the other tabs changing the same files.
func (p *tabPanel) tabNotes(m *Model) []string {
	t := p.t
	var lines []string
	if t.info.merged() {
		lines = append(lines, "", keyHint("merged")+" — closing the tab ("+m.pk("x")+") can delete the branch too.")
	}
	if ov := m.overlaps[t.ID]; len(ov) > 0 {
		lines = append(lines, "", warn("Also changed in other tabs — expect merge conflicts:"))
		// The branches are enough to know what to merge with what; the
		// files would fill the screen.
		for _, o := range ov {
			lines = append(lines, "  "+keyHint(strings.Join(o.branches(), ", "))+dim("  "+o.with.Name+" · "+plural(len(o.files), "file")))
		}
	}
	return lines
}
