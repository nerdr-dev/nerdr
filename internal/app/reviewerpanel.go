package app

import (
	"slices"
	"strconv"
	"strings"

	"nerdr/internal/agents"
	"nerdr/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A review pane: the code review by a second agent (see codereview.go) of
// the work of the agent whose pane it was opened from. Opened, it offers
// the agents to review with; once one is picked, it shows how the review
// stands above the reviewer itself, which has the keyboard there as an
// agent has in its pane. Every reviewer of a review has a pane of its own;
// another pane added to a review going on adds a reviewer to it.

// reviewerHeadRows is the rows above the reviewer while it runs: how the
// review stands, its keys, and a rule.
const reviewerHeadRows = 3

// newReviewPane opens a review of the agent whose pane has the keyboard,
// in a pane of its own: one has to be in an agent's pane, as its work is
// what is reviewed and its findings go back to it.
func (m *Model) newReviewPane() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	if why := m.commandOff("t r"); why != "" {
		m.toast(why)
		return nil
	}
	of := t.focused().agent()
	m.reviewerCursor = 0
	return m.addPane(t, &paneTab{kind: paneReview, of: of}, false)
}

// startReviewWith starts by reviewing of's work, or adds it to the
// review going on.
func (m *Model) startReviewWith(of *tab, by reviewer) tea.Cmd {
	if of.review != nil {
		return tea.Batch(m.addReviewer(of, by), m.resize())
	}
	return m.lookAtTree(of, treeHand, by)
}

// reviewPaneFor is the pane reviewer r of of's work is shown in: the
// pane it was picked in, or, for a review that started by itself, a new
// one beside of's that leaves the keyboard where it is.
func (m *Model) reviewPaneFor(of, r *tab) *paneTab {
	o := of.owner()
	for _, p := range o.panes() {
		if p.kind == paneReview && p.of == of && p.a == nil {
			p.a = r
			return p
		}
	}
	p := &paneTab{kind: paneReview, a: r, of: of}
	ps := o.panes()
	at := len(ps) - 1
	if op := of.paneOf(); op != nil {
		at = slices.Index(ps, op)
	}
	o.paneList = slices.Insert(ps, at+1, p)
	return p
}

// dropReviewPane takes reviewer r's pane away, once it is let go.
func (m *Model) dropReviewPane(r *tab) {
	if p := r.paneOf(); p != nil {
		m.dropPane(r.owner(), p)
	}
}

// reviewPickKey handles a key in review pane p while it lists the agents
// to review with.
func (m *Model) reviewPickKey(t *tab, p *paneTab, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	of := p.of
	if m.reviewPick != nil {
		return m.modelPickKey(of, msg)
	}
	switch k {
	case "esc", "q":
		m.dropPane(t, p)
		return m.resize()
	case "-", "left":
		return m.setRounds(of, -1)
	case "+", "=", "right":
		return m.setRounds(of, 1)
	}
	kinds := m.reviewKinds(of)
	if len(kinds) == 0 {
		return nil
	}
	m.reviewerCursor = min(max(m.reviewerCursor, 0), len(kinds)-1)
	switch k {
	case "up", "k":
		m.reviewerCursor = max(m.reviewerCursor-1, 0)
	case "down", "j":
		m.reviewerCursor = min(m.reviewerCursor+1, len(kinds)-1)
	case "enter":
		return m.startReviewWith(of, reviewer{kind: kinds[m.reviewerCursor]})
	case "m":
		return m.openModelPick(kinds[m.reviewerCursor])
	default:
		if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(kinds) {
			m.reviewerCursor = n - 1
			return m.startReviewWith(of, reviewer{kind: kinds[n-1]})
		}
	}
	return nil
}

// reviewPaneLines draws review pane p of t's, w×h inside its frame: the
// reviewer under how the review stands, or the agents to start one with.
func (m *Model) reviewPaneLines(t *tab, p *paneTab, w, h int) []string {
	of := p.of
	if r := p.a; r != nil {
		head := []string{
			" " + ansi.Truncate(m.reviewLine(of), w-2, "…"),
			" " + m.reviewerButtons(t, p),
			dim(strings.Repeat("─", w)),
		}
		return append(fitLines(head, w, reviewerHeadRows), m.paneLines(t, p, w, max(h-reviewerHeadRows, 1))...)
	}

	if m.reviewPick != nil {
		return m.modelPickLines(t, p, w, h)
	}
	var lines []string
	switch {
	case of.review != nil:
		lines = append(lines, " "+ansi.Truncate(m.reviewLine(of), w-2, "…"), "",
			" Review "+m.agentLabel(t, of)+"'s work beside "+m.reviewerNames(of)+" with:", "")
	default:
		lines = append(lines, " Review "+m.agentLabel(t, of)+"'s work in this worktree with:", "")
	}
	kinds := m.reviewKinds(of)
	if len(kinds) == 0 {
		if of.sandboxed || m.sandboxedKind(of.Agent) {
			lines = append(lines, dim("   (no sandboxed reviewer is available to add)"))
		} else {
			lines = append(lines, dim("   (every agent installed is reviewing already)"))
		}
	}
	m.reviewerCursor = min(max(m.reviewerCursor, 0), len(kinds)-1)
	keys := m.focus == focusAgent && t == m.cur() && t.focused() == p
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	for i, k := range kinds {
		var notes []string
		if i == 0 && k == m.reviewKind(of) {
			notes = append(notes, "the config's choice")
		}
		if k == of.Agent {
			notes = append(notes, "the same kind as the author")
		}
		if model := m.cfg.Review.Models[k]; model != "" && agents.TakesModel(k) {
			notes = append(notes, model)
		}
		label := strconv.Itoa(i+1) + "  " + string(k)
		cursor := "   "
		if i == m.reviewerCursor && keys {
			cursor, label = sel.Render(" ❯ "), sel.Render(label)
		} else if i == m.reviewerCursor {
			label = lipgloss.NewStyle().Bold(true).Render(label)
		}
		if len(notes) > 0 {
			label += dim(" · " + strings.Join(notes, " · "))
		}
		lines = append(lines, m.onClick(cursor+label, func(m *Model) tea.Cmd {
			m.reviewerCursor = i
			return tea.Batch(m.goToPane(t, p), m.startReviewWith(of, reviewer{kind: k}))
		}))
	}
	rounds := max(m.cfg.Review.Rounds, 1)
	if of.review != nil {
		rounds = of.review.rounds
	}
	lines = append(lines, "",
		" Rounds: "+m.onClick(buttonStyle.Render(" − "), func(m *Model) tea.Cmd { return m.setRounds(of, -1) })+
			" "+lipgloss.NewStyle().Bold(true).Render(strconv.Itoa(rounds))+" "+
			m.onClick(buttonStyle.Render(" + "), func(m *Model) tea.Cmd { return m.setRounds(of, 1) })+
			dim("  how many times the reviewer looks, with fixes between, before you decide"))
	switch {
	case m.cfg.Review.Auto && m.waitsForReady(of):
		lines = append(lines, dim(" Auto: on · a review starts by itself when the agent says its work is ready for one"))
	case m.cfg.Review.Auto:
		lines = append(lines, dim(" Auto: on · a review starts by itself when the agent ends a turn that changed files"))
	default:
		lines = append(lines, dim(" Auto: off · a review starts only when you start one here (auto in [review] in the config)"))
	}
	foot := []string{"", " " + keyHint("↑↓") + dim(" pick · ") + keyHint("enter") + dim(" start · ") + keyHint("m") + dim(" model · ") +
		keyHint("− +") + dim(" rounds · ") + keyHint("esc") + dim(" close")}
	return fitLines(append(fitLines(lines, w, max(h-len(foot), 0)), foot...), w, h)
}

// reviewerButtons are the buttons above the reviewer in review pane p:
// stop it (or the review, when it is the only one), and add another.
func (m *Model) reviewerButtons(t *tab, p *paneTab) string {
	run, r := p.of.review, p.a
	if run == nil {
		return ""
	}
	label := " stop " + string(r.Agent) + " "
	if len(run.seats) == 1 {
		label = " stop the review "
	}
	s := m.onClick(buttonStyle.Render(label), func(m *Model) tea.Cmd { return m.closePane(t, p) })
	if run.phase == reviewing {
		s += " " + m.onClick(buttonStyle.Render(" + reviewer "), func(m *Model) tea.Cmd {
			cmd := m.goToPane(t, p)
			return tea.Batch(cmd, m.newReviewPane())
		})
	}
	return s
}

// reviewOff says why the pane with the keyboard cannot be reviewed: only
// an agent's work can.
func (m *Model) reviewOff() string {
	t := m.cur()
	if t == nil {
		return "no tab open"
	}
	if t.focused().agent() == nil {
		return "a review is of an agent's work: go to the agent's pane first"
	}
	return ""
}

// stopReview stops the review of the agent nearest the keyboard, when one
// goes on.
func (m *Model) stopReview() tea.Cmd {
	a := m.curAgent()
	if a == nil || a.review == nil {
		return nil
	}
	m.endReview(a, "stopped", false)
	return m.resize()
}

// reviewKinds are the agents t's work can be reviewed by: the one the
// config picks first, then the others installed. While a review goes on,
// those reviewing are left out.
func (m *Model) reviewKinds(t *tab) []agents.Kind {
	var kinds []agents.Kind
	for _, k := range append([]agents.Kind{m.reviewKind(t)}, agents.Available(m.cfg.Agents)...) {
		if (t.sandboxed || m.sandboxedKind(t.Agent)) && !m.sandboxedKind(k) {
			continue
		}
		if !slices.Contains(kinds, k) && !t.reviewedBy(k) {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// reviewedBy reports whether kind k is among t's reviewers.
func (t *tab) reviewedBy(k agents.Kind) bool {
	if t.review == nil {
		return false
	}
	for _, s := range t.review.seats {
		if s.r.Agent == k {
			return true
		}
	}
	return false
}

// maxRounds is the most rounds a review pane offers: past it, a review
// that has not settled is better looked at by you.
const maxRounds = 9

// setRounds changes how many rounds a review has by d, in the config so
// it holds for the reviews to come, and for t's review when one goes on:
// never below the round it is in.
func (m *Model) setRounds(t *tab, d int) tea.Cmd {
	n := min(max(max(m.cfg.Review.Rounds, 1)+d, 1), maxRounds)
	if t != nil && t.review != nil {
		n = min(max(t.review.rounds+d, t.review.round, 1), maxRounds)
		t.review.rounds = n
	}
	if n == m.cfg.Review.Rounds {
		return nil
	}
	m.cfg.Review.Rounds = n
	if err := config.SaveReviewRounds(n); err != nil {
		m.toast("rounds: " + strconv.Itoa(n) + " for now; not saved: " + err.Error())
	}
	return nil
}
