package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"
)

// A tab's work can be reviewed by a second agent (internal/codereview).
// The reviewer runs in the tab's worktree, in a pane of the tab's beside
// the agent (see reviewerpanel.go): what matters is its conclusion, which comes back through
// `nerdr post-review` and is shown against the code in the review panel and,
// in short, in the panel above the agent. Its findings go to the tab's
// agent as a message — kept in F3 for the user to send, or sent at once —
// and when the agent has worked on them, the reviewer looks again, until
// it approves or the rounds run out.
//
// Several agents can review the same work side by side, one of each kind:
// a round ends when all of them have sent their review, and their findings
// go to the agent together, in one message. One that approves is let go;
// the others look again at the agent's fixes.
//
// A reviewer is a tab of its own as far as running it goes (it is started,
// read off the screen and sent its messages as a tab's agent is), but it
// is not in the tab row: it has a pane in the tab of the agent it reviews
// (see reviewerpanel.go), and goes with it.
//
// While the reviewer looks, the tab's own messages wait, so the code does
// not change under it. A review starts by itself, when the config says so,
// once the agent finishes a turn that left the worktree other than the
// last review saw it: any such turn, or with when = "ready", one in which
// the agent said with `nerdr request-review` that the work is ready for a
// review — it knows better than a changed file does whether it is
// halfway, and can say what it wants looked at most.

// askReviewDone asks, when the tab shown has a review's findings waiting
// in F3, whether to send them to its agent now or keep them there, or
// tells of a review that approved with none. It is
// asked once, the first time the tab is shown with nothing else open: at
// once when the review ends in it, or when the user comes back to it, also
// after nerdr has been restarted: it is saved with the tab until then. Not
// while the prefix is on: walking the tabs with the arrows, the tab is
// only passed, and the enter that ends the walk would answer it unseen.
// The walk shows it meanwhile, without asking; see reviewPeek.
// The review may be of any of the tab's agents.
func (m *Model) askReviewDone() {
	cur := m.Cur()
	if cur == nil || m.prefix || m.dialog != nil || m.editor != nil {
		return
	}
	var drop []core.TabRef
	for _, a := range append([]*core.Tab{cur}, cur.ExtraAgents()...) {
		if a.ReviewDone != nil && a.ReviewDoneStale() {
			drop = append(drop, m.fleet.Ref(a))
		}
	}
	t, d := cur.ReviewToTell()
	if t != nil {
		drop = append(drop, m.fleet.Ref(t))
		m.dialog = &reviewDoneDialog{t: t, d: d}
	}
	if len(drop) > 0 {
		m.do(core.DropReviewDone{A: drop})
	}
}

// notifyReview tells of a review's conclusion as a finished turn is told
// of.
func (m *Model) notifyReview(t *core.Tab, brief string) {
	if !m.cfg().NotifyOn("done") || (!m.blurred && t == m.Cur()) {
		return
	}
	if !m.blurred {
		m.inApp = append(m.inApp, t.Name+": review "+brief)
		m.inAppTabs = append(m.inAppTabs, t)
		return
	}
	m.notes = append(m.notes, core.Note{T: t, Title: "nerdr · " + t.Name, Body: "review " + brief})
}
