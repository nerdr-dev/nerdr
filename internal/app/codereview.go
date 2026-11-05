package app

import (
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
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

// onTree goes on from a look at a tab's worktree.
func (m *Model) onTree(msg core.TreeMsg) tea.Cmd {
	t := msg.T
	if !m.Live(t) {
		return nil
	}
	if msg.Err != nil {
		if msg.Why != core.TreeAuto {
			m.toast(t.Name + ": could not look at the worktree for a review: " + msg.Err.Error())
		}
		return nil
	}
	switch msg.Why {
	case core.TreeAuto, core.TreeHand:
		if t.Review != nil {
			if msg.Why == core.TreeHand && msg.By.Kind != "" {
				// Picked while the look for another reviewer was on its
				// way: it joins that one.
				return m.addReviewer(t, msg.By)
			}
			return nil
		}
		if msg.Why == core.TreeAuto && (t.Light.AtWork() || t.NextAuto() >= 0) {
			// The agent was given more to do while the worktree was
			// looked at: the end of that turn is the time.
			return nil
		}
		focus, by := "", msg.By
		if a := t.ReviewAsk; a != nil {
			focus = a.Focus
			if by.Kind == "" {
				by = a.By
			}
		}
		t.ReviewAsk = nil
		if !msg.Changed {
			if msg.Why == core.TreeHand {
				m.toast(t.Name + ": nothing to review — the worktree is as its base")
			}
			return nil
		}
		if msg.Why == core.TreeAuto && msg.Tree == t.Reviewed {
			return nil // reviewed as it is
		}
		if by.Kind == "" {
			by = core.Reviewer{Kind: m.ReviewKind(t)}
		}
		return m.startReview(t, by, msg.Base, msg.Tree, focus)
	case core.TreeFixed:
		run := t.Review
		if run == nil || run.Phase != core.Fixing {
			return nil
		}
		if msg.Tree == run.Tree {
			m.EndReview(t, run.Brief+" · "+string(t.Agent)+" changed nothing", true)
			return nil
		}
		run.Round++
		run.Tree = msg.Tree
		run.Phase = core.Reviewing
		for _, s := range run.Seats {
			s.Res = nil
			m.AddMessage(s.R, codereview.Again(string(t.Agent), run.Round, run.Rounds), true)
		}
		m.resizePanes()
		m.toast(t.Name + ": " + m.ReviewerNames(t) + " review again (round " + strconv.Itoa(run.Round) + ")")
		return nil
	}
	return nil
}

// startReview starts by reviewing t's work, looking most at focus.
func (m *Model) startReview(t *core.Tab, by core.Reviewer, base, tree, focus string) tea.Cmd {
	t.Review = &core.ReviewRun{Round: 1, Rounds: max(m.Cfg.Review.Rounds, 1), Base: base, Tree: tree, Focus: focus,
		Id: time.Now().Format(time.RFC3339Nano)}
	cmd := m.addReviewer(t, by)
	if len(t.Review.Seats) == 0 {
		t.Review = nil
	}
	return cmd
}

// addReviewer starts by reviewing t's work, beside the reviewers at it
// already, in the round they are in.
func (m *Model) addReviewer(t *core.Tab, by core.Reviewer) tea.Cmd {
	k, model := by.Kind, by.Model
	if model == "" {
		model = m.Cfg.Review.Models[k]
	}
	if !agents.TakesModel(k) {
		model = ""
	}
	// The review prompt contains the author's text and the reviewer reads
	// its worktree. Keep that work inside a sandbox too, including when
	// the config changed while the worktree was being inspected.
	if m.Confined(hooks.Report{}, t) && !m.SandboxedKind(k) {
		m.toast(t.Name + ": a sandboxed agent's work needs a sandboxed reviewer")
		return nil
	}
	run := t.Review
	if run.Phase != core.Reviewing {
		m.toast(t.Name + ": " + string(t.Agent) + " is on the findings · another reviewer can join when the work is reviewed again")
		return nil
	}
	for _, s := range run.Seats {
		if s.R.Agent == k {
			m.toast(t.Name + ": " + string(k) + " is reviewing already")
			return nil
		}
	}
	r := &core.Tab{AgentState: core.Agent{Tab: state.Tab{
		ID: t.ID + "-review-" + string(k), Name: t.Owner().Name + " · review", Dir: t.Dir, Agent: k, Worktrees: t.Worktrees,
		Model: model,
	}, Light: core.StStarting}, ReviewOf: t}
	req := codereview.Request(string(t.Agent), string(k), run.Base, run.Focus, run.Round, run.Rounds)
	l := agents.NewSession(agents.WithModel(m.Cfg.Agents, k, model), k)
	l.Argv = append(l.Argv, m.Cfg.Review.Args[k]...)
	if first := agents.StartPrompt(k, req); first != nil {
		l.Argv = append(l.Argv, first...)
	} else {
		r.Messages = []state.Message{{Text: req, Auto: true}}
	}
	p := m.reviewPaneFor(t, r)
	cmd := m.Spawn(r, l, false)
	if r.Pane == nil {
		m.toast(t.Name + ": " + r.Err)
		p.A = nil
		return fromCore(cmd)
	}
	run.Seats = append(run.Seats, &core.Seat{R: r})
	m.focusTerms(t.Owner())
	m.resizePanes() // the panes say so
	with := string(k)
	if model != "" {
		with += " (" + model + ")"
	}
	core.Debugf("review %s: %s started", m.agentName(t), with)
	m.toast(m.agentName(t) + ": " + with + " reviews " + string(t.Agent) + "'s work, in a pane beside it")
	return fromCore(cmd)
}

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
	dropped := false
	for _, a := range append([]*core.Tab{cur}, cur.ExtraAgents()...) {
		if a.ReviewDone != nil && a.ReviewDoneStale() {
			a.ReviewDone = nil
			dropped = true
		}
	}
	t, d := cur.ReviewToTell()
	if t == nil {
		if dropped {
			m.saveSession()
		}
		return
	}
	t.ReviewDone = nil
	m.saveSession()
	m.dialog = &reviewDoneDialog{t: t, d: d}
}

// notifyReview tells of a review's conclusion as a finished turn is told
// of.
func (m *Model) notifyReview(t *core.Tab, brief string) {
	if !m.Cfg.NotifyOn("done") || (!m.blurred && t == m.Cur()) {
		return
	}
	if !m.blurred {
		m.inApp = append(m.inApp, t.Name+": review "+brief)
		m.inAppTabs = append(m.inAppTabs, t)
		return
	}
	m.notes = append(m.notes, core.Note{T: t, Title: "nerdr · " + t.Name, Body: "review " + brief})
}
