package app

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/review"

	"github.com/charmbracelet/x/ansi"
)

// panelLines draws the review panel into a w×h area, as its pane or its
// dialog: the screen chosen by its F-key. The screens nerdr draws are
// framed here; the panel's own frame its panes.
func (m *Model) panelLines(w, h int) []string { return m.panelBody(w, h) }

// reuseCheck, set by NERDR_CHECK_REUSE and in the tests, draws the review
// panel anew each time its last drawing is used again, and panics if the
// two differ: a change to the panel that reviewReuse let through.
var reuseCheck = os.Getenv("NERDR_CHECK_REUSE") != ""

// reviewSet gives r what the panel is drawn with from outside it.
func (m *Model) reviewSet(r review.Model, w int) review.Model {
	if r.BranchPR() {
		r = r.SetBranch(m.branchRows(w - 3)) // the border, and a column off it
	}
	return r
}

// reviewFitKey is what reviewFit was fitted from and to.
type reviewFitKey struct {
	seq  uint64
	w, h int
}

// panelBody is what the review panel shows, w×h: the screen chosen, below
// the menu row when it has one.
func (m *Model) panelBody(w, h int) []string {
	// The branch's rows have zones in them, numbered for one frame only:
	// drawn anew each time.
	// Core may have changed it since Update, on its own goroutine.
	if !m.reviewReuse || m.fleet.Gen() != m.reviewGen || m.reviewView == "" || m.review.BranchPR() {
		m.review = m.reviewSet(m.review, w)
		m.reviewView = m.review.View()
		m.reviewGen = m.fleet.Gen()
		m.reviewSeq++
	} else if reuseCheck {
		if v := m.reviewSet(m.review, w).View(); v != m.reviewView {
			panic(fmt.Sprintf("review panel reused, but it has changed:\n--- reused\n%s\n--- now\n%s", ansi.Strip(m.reviewView), ansi.Strip(v)))
		}
	}
	// Fitted once for each drawing: it measures every line, and the panel
	// is the same from one frame to the next while only programs draw.
	if key := (reviewFitKey{m.reviewSeq, w, h}); m.reviewFit == nil || key != m.reviewFitOf {
		m.reviewFit, m.reviewFitOf = fitLines(strings.Split(m.reviewView, "\n"), w, h), key
	}
	lines := slices.Clone(m.reviewFit) // what is drawn over it changes its lines
	if h < 3 || w < 3 {
		return lines
	}
	iw, ih := w-2, h-2
	active := m.focus == core.FocusReview
	var title string
	var body []string
	switch {
	case m.review.Messages() && m.saved.on:
		title, body = "saved prompts", m.savedBody(iw, ih)
	case m.review.Messages():
		title, body = "tasks", []string{"", "  No tab open."}
		if a := m.CurAgent(); a != nil {
			body = m.messagesBody(a.Owner(), iw, ih)
		}
	case m.review.Skills():
		title, body = "skills", m.skillsBody(m.Cur(), iw, ih)
	case m.review.Plugins():
		title, body = m.pluginsTitle(m.panelTab()), m.pluginsBody(m.panelTab(), iw, ih)
	case m.review.History():
		title, body = "history ("+strconv.Itoa(len(m.history.entries))+")", m.historyBody(iw, ih)
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
	return framed(fitLines(body, iw, ih), w, h, title, active)
}
