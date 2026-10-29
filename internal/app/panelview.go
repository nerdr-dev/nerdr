package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/review"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// panelLines draws the review panel into a w×h area: the screen chosen in
// the column of F-keys beside it (see rail.go), or in the menu row over
// it while the panel is kept open. The screens nerdr draws are framed
// here; the panel's own frame its panes.
//
// The menu row has a frame of its own, open at the bottom, where it runs
// into the frame of the screen below it.
func (m *Model) panelLines(w, h int) []string {
	if !m.panelMenu() {
		return m.panelBody(w, h)
	}
	body := m.panelBody(w, max(h-1, 1))
	if w < 3 {
		return append([]string{strings.Repeat(" ", w)}, body...)
	}
	bs := lipgloss.NewStyle().Foreground(frameColor)
	if m.focus == focusReview && m.review.TopFocused() {
		bs = lipgloss.NewStyle().Foreground(accent)
	}
	out := []string{bs.Render("╭" + strings.Repeat("─", w-2) + "╮")}
	if w > 8 && !m.reviewZoom {
		out[0] = bs.Render("╭─") + m.panelToggle(m.panelSmall) + bs.Render(strings.Repeat("─", w-4)+"╮")
	}
	for i, l := range body {
		left, right := "", ""
		switch {
		case i == 0:
			left, right = "│", "│"
		case i == 1 && strings.HasPrefix(ansi.Strip(l), "╭") && strings.HasSuffix(ansi.Strip(l), "╮"):
			left, right = "├", "┤"
		}
		if left != "" {
			l = bs.Render(left) + ansi.Cut(l, 1, w-1) + "\x1b[0m" + bs.Render(right)
		}
		out = append(out, l)
	}
	return out
}

// reuseCheck, set by NERDR_CHECK_REUSE and in the tests, draws the review
// panel anew each time its last drawing is used again, and panics if the
// two differ: a change to the panel that reviewReuse let through.
var reuseCheck = os.Getenv("NERDR_CHECK_REUSE") != ""

// reviewSet gives r what the panel is drawn with from outside it.
func (m *Model) reviewSet(r review.Model, w int) review.Model {
	if r.BranchPR() {
		r = r.SetBranch(m.branchRows(w - 3)) // the border, and a column off it
	}
	// F4 can turn the skill off, and the config be read anew, at any time.
	return r.SetExplain(m.skillOn(m.cur(), "nerdr-explain"))
}

// panelBody is what the review panel shows, w×h: the screen chosen, below
// the menu row when it has one.
func (m *Model) panelBody(w, h int) []string {
	// The branch's rows have zones in them, numbered for one frame only:
	// drawn anew each time.
	if !m.reviewReuse || m.reviewView == "" || m.review.BranchPR() {
		m.review = m.reviewSet(m.review, w)
		m.reviewView = m.review.View()
	} else if reuseCheck {
		if v := m.reviewSet(m.review, w).View(); v != m.reviewView {
			panic(fmt.Sprintf("review panel reused, but it has changed:\n--- reused\n%s\n--- now\n%s", ansi.Strip(m.reviewView), ansi.Strip(v)))
		}
	}
	lines := fitLines(strings.Split(m.reviewView, "\n"), w, h)
	if h < 3 || w < 3 {
		return lines
	}
	var menu []string
	if m.panelMenu() {
		menu, h = lines[:1], h-1
	}
	iw, ih := w-2, h-2
	active := m.focus == focusReview
	var title string
	var body []string
	switch {
	case m.review.Messages() && m.saved.on:
		title, body = "saved prompts", m.savedBody(iw, ih)
	case m.review.Messages():
		title, body = "tasks", []string{"", "  No tab open."}
		if a := m.curAgent(); a != nil {
			body = m.messagesBody(a.owner(), iw, ih)
		}
	case m.review.Skills():
		title, body = "nerdr's skills", m.skillsBody(m.cur(), iw, ih)
	case m.review.Plugins():
		title, body = m.pluginsTitle(m.cur()), m.pluginsBody(m.cur(), iw, ih)
	case m.review.AllPRs():
		title = "my pull requests …"
		if d := m.prPanel; d != nil && d.all {
			title = "all open pull requests …"
			if !d.loading {
				title = fmt.Sprintf("all open pull requests (%d, %d asking your review)", len(d.entries), askedCount(d.entries))
			}
		} else if d != nil && !d.loading {
			title = fmt.Sprintf("my pull requests (%d)", len(d.entries))
		}
		body = m.allPRsBody(iw, ih)
	case m.review.Issues():
		title, body = m.issuesTitle(), m.issuesBody(iw, ih)
	default:
		return lines
	}
	return append(menu, framed(fitLines(body, iw, ih), w, h, title, active)...)
}
