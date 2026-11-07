package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"

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
	t := m.Cur()
	if t == nil {
		return nil
	}
	if why := m.commandOff("t r"); why != "" {
		m.toast(why)
		return nil
	}
	of := m.focused(t).Agent()
	m.reviewerCursor = 0
	cmds := []tea.Cmd{m.do(core.OpenReviewPane{T: m.Ref(t), Of: m.Ref(of)})}
	for _, k := range m.reviewKinds(of) {
		cmds = append(cmds, m.do(core.AskModels{Kind: k}))
	}
	return tea.Batch(cmds...)
}

// startReviewWith starts by reviewing of's work, or adds it to the
// review going on.
func (m *Model) startReviewWith(of *core.Tab, by core.Reviewer) tea.Cmd {
	return m.do(core.StartReview{Of: m.Ref(of), By: by})
}

// reviewPickKey handles a key in review pane p while it lists the agents
// to review with.
func (m *Model) reviewPickKey(t *core.Tab, p *core.PaneTab, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	of := p.Of
	if m.reviewPick != nil {
		return m.modelPickKey(of, msg)
	}
	switch k {
	case "esc", "q":
		return tea.Batch(m.do(core.DropPane{P: m.PaneRef(p)}), m.resize())
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
		return m.startReviewWith(of, core.Reviewer{Kind: kinds[m.reviewerCursor]})
	case "m":
		return m.openModelPick(kinds[m.reviewerCursor])
	default:
		if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(kinds) {
			m.reviewerCursor = n - 1
			return m.startReviewWith(of, core.Reviewer{Kind: kinds[n-1]})
		}
	}
	return nil
}

// reviewPaneLines draws review pane p of t's, w×h inside its frame: the
// reviewer under how the review stands, or the agents to start one with.
func (m *Model) reviewPaneLines(t *core.Tab, p *core.PaneTab, w, h int) []string {
	of := p.Of
	if r := p.A; r != nil {
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
	case of.Review != nil:
		lines = append(lines, " "+ansi.Truncate(m.reviewLine(of), w-2, "…"), "",
			" Review "+m.AgentLabel(t, of)+"'s work beside "+m.ReviewerNames(of)+" with:", "")
	default:
		lines = append(lines, " Review "+m.AgentLabel(t, of)+"'s work in this worktree with:", "")
	}
	kinds := m.reviewKinds(of)
	if len(kinds) == 0 {
		if of.InSandbox || m.SandboxedKind(of.Agent) {
			lines = append(lines, dim("   (no sandboxed reviewer is available to add)"))
		} else {
			lines = append(lines, dim("   (every agent installed is reviewing already)"))
		}
	}
	m.reviewerCursor = min(max(m.reviewerCursor, 0), len(kinds)-1)
	keys := m.focus == core.FocusAgent && t == m.Cur() && m.focused(t) == p
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	for i, k := range kinds {
		var notes []string
		if i == 0 && k == m.ReviewKind(of) {
			notes = append(notes, "the config's choice")
		}
		if k == of.Agent {
			notes = append(notes, "the same kind as the author")
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
		row := m.onClick(cursor+label, func(m *Model) tea.Cmd {
			m.reviewerCursor = i
			return tea.Batch(m.goToPane(t, p), m.startReviewWith(of, core.Reviewer{Kind: k}))
		})
		if agents.TakesModel(k) {
			row += "  " + m.onClick(buttonStyle.Render(" "+cmp.Or(m.ReviewModel(k), "model")+" ▾ "), func(m *Model) tea.Cmd {
				m.reviewerCursor = i
				return tea.Batch(m.goToPane(t, p), m.openModelPick(k))
			})
		}
		lines = append(lines, row)
	}
	rounds := max(m.Cfg.Review.Rounds, 1)
	if of.Review != nil {
		rounds = of.Review.Rounds
	}
	lines = append(lines, "",
		" Rounds: "+m.onClick(buttonStyle.Render(" − "), func(m *Model) tea.Cmd { return m.setRounds(of, -1) })+
			" "+lipgloss.NewStyle().Bold(true).Render(strconv.Itoa(rounds))+" "+
			m.onClick(buttonStyle.Render(" + "), func(m *Model) tea.Cmd { return m.setRounds(of, 1) })+
			dim("  how many times the reviewer looks, with fixes between, before you decide"))
	switch {
	case m.Cfg.Review.Auto && m.WaitsForReady(of):
		lines = append(lines, dim(" Auto: on · a review starts by itself when the agent says its work is ready for one"))
	case m.Cfg.Review.Auto:
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
func (m *Model) reviewerButtons(t *core.Tab, p *core.PaneTab) string {
	run, r := p.Of.Review, p.A
	if run == nil {
		return ""
	}
	label := " stop " + string(r.Agent) + " "
	if len(run.Seats) == 1 {
		label = " stop the review "
	}
	s := m.onClick(buttonStyle.Render(label), func(m *Model) tea.Cmd { return m.do(core.ClosePane{P: m.PaneRef(p)}) })
	if run.Phase == core.Reviewing {
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
	t := m.Cur()
	if t == nil {
		return "no tab open"
	}
	if m.focused(t).Agent() == nil {
		return "a review is of an agent's work: go to the agent's pane first"
	}
	return ""
}

// stopReview stops the review of the agent nearest the keyboard, when one
// goes on.
func (m *Model) stopReview() tea.Cmd {
	a := m.CurAgent()
	if a == nil || a.Review == nil {
		return nil
	}
	return m.do(core.StopReview{A: m.Ref(a)})
}

// reviewKinds are the agents t's work can be reviewed by: the one the
// config picks first, then the others installed. While a review goes on,
// those reviewing are left out.
func (m *Model) reviewKinds(t *core.Tab) []agents.Kind {
	var kinds []agents.Kind
	for _, k := range append([]agents.Kind{m.ReviewKind(t)}, agents.Available(m.Cfg.Agents)...) {
		if (t.InSandbox || m.SandboxedKind(t.Agent)) && !m.SandboxedKind(k) {
			continue
		}
		if !slices.Contains(kinds, k) && !t.ReviewedBy(k) {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// maxRounds is the most rounds a review pane offers: past it, a review
// that has not settled is better looked at by you.
const maxRounds = 9

// setRounds changes how many rounds a review has by d, in the config so
// it holds for the reviews to come, and for t's review when one goes on:
// never below the round it is in.
func (m *Model) setRounds(t *core.Tab, d int) tea.Cmd {
	n := min(max(max(m.Cfg.Review.Rounds, 1)+d, 1), maxRounds)
	var cmd tea.Cmd
	if t != nil && t.Review != nil {
		n = min(max(t.Review.Rounds+d, t.Review.Round, 1), maxRounds)
		cmd = m.do(core.SetRounds{A: m.Ref(t), N: n})
	}
	if n == m.Cfg.Review.Rounds {
		return cmd
	}
	m.Cfg.Review.Rounds = n
	cmd = tea.Batch(cmd, m.do(core.SetConfig{Cfg: m.Cfg}))
	if err := config.SaveReviewRounds(n); err != nil {
		m.toast("rounds: " + strconv.Itoa(n) + " for now; not saved: " + err.Error())
	}
	return cmd
}
