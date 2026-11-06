package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// A code review is the core's alone, but its reviewers run in the holder
// with the other programs: so a core started after a crash can take it up,
// the session keeps it while nerdr runs over a holder. Without one the
// reviewers stop with nerdr, and the session keeps no review, as before.

// savedReview is t's review as the session keeps it, nil for none or with
// no holder.
func (m *Core) savedReview(t *Tab) *state.Review {
	run := t.Review
	if m.Held == nil || run == nil {
		return nil
	}
	r := &state.Review{ID: run.Id, Phase: int(run.Phase), Round: run.Round, Rounds: run.Rounds, Base: run.Base,
		Focus: run.Focus, Tree: run.Tree, FixMsg: run.FixMsg, Brief: run.Brief, Approved: run.Approved}
	for _, s := range run.Seats {
		r.Seats = append(r.Seats, state.ReviewSeat{Agent: s.R.Tab, Result: s.Res})
	}
	return r
}

// saveReview saves the session for a change to a review, when the session
// keeps reviews; see savedReview.
func (m *Core) saveReview() {
	if m.Held != nil {
		m.SaveSession()
	}
}

// takeUpReviews puts back the reviews the session kept, with the
// reviewers the holder kept: a review none of whose reviewers is there is
// dropped, as one is when nerdr stops. m.Mu is held.
func (m *Core) takeUpReviews() {
	for _, t := range m.AllAgents() {
		saved := t.Tab.Review
		t.Tab.Review = nil
		if saved == nil || t.ReviewOf != nil {
			continue
		}
		run := &ReviewRun{Id: saved.ID, Phase: ReviewPhase(saved.Phase), Round: saved.Round, Rounds: saved.Rounds,
			Base: saved.Base, Focus: saved.Focus, Tree: saved.Tree, FixMsg: saved.FixMsg, Brief: saved.Brief,
			Approved: saved.Approved}
		t.Review = run
		for _, seat := range saved.Seats {
			if !m.Held.has(func(l Label) bool { return l.Kind == HeldAgent && l.Agent == seat.Agent.ID }) {
				continue
			}
			r := &Tab{AgentState: Agent{Tab: seat.Agent, Light: StStarting}, ReviewOf: t}
			p := m.ReviewPaneFor(t, r)
			p.Opened = time.Now()
			m.takeUpAgent(r)
			run.Seats = append(run.Seats, &Seat{R: r, Res: seat.Result})
		}
		if len(run.Seats) == 0 {
			t.Review = nil
			continue
		}
		Debugf("review %s: taken up with %s", m.AgentName(t), m.ReviewerNames(t))
		m.Tell(TermsMsg{T: m.Ref(t.Owner())})
	}
	m.Tell(PanesMsg{})
}

// has reports whether a program not yet taken up has a label wanted
// says is it.
func (h *Held) has(wanted func(Label) bool) bool {
	for _, p := range h.Progs {
		if l, ok := ParseLabel(p.Label); ok && wanted(l) {
			return true
		}
	}
	return false
}
