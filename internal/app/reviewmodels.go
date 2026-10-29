package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// In a review pane, m on an agent, or the model button beside it, picks
// the model it reviews with: from the models the agent itself lists
// (agents.Models), those it was started with before, narrowed by what is
// typed, or what is typed when it is none of them. The lists are asked for
// as the pane opens, so they are there by the time one is picked. The model
// picked is kept in [review.models] in the config, for the reviews to
// come, as the rounds are.

// modelPick is the model being picked for kind.
type modelPick struct {
	kind   agents.Kind
	filter textinput.Model
	cursor int
}

// modelList is the models an agent listed, when, and why none; own is
// the model it starts with when nerdr names none (agents.OwnModel).
type modelList struct {
	models []agents.AgentModel
	own    string
	at     time.Time
	err    string
	asking bool
}

// modelsMsg carries the models kind listed; again is set on the second
// asking, after the first listed none.
type modelsMsg struct {
	kind   agents.Kind
	models []agents.AgentModel
	own    string
	err    error
	again  bool
}

// modelsFor is how long an agent's list of models is kept before it is
// asked again; modelsAgain how long after listing none it is asked once
// more: opencode lists none while it refreshes its own.
const (
	modelsFor   = 10 * time.Minute
	modelsAgain = 2 * time.Second
)

// askModels asks k for its models, when it has not been asked lately.
func (m *Model) askModels(k agents.Kind) tea.Cmd {
	if !agents.TakesModel(k) {
		return nil
	}
	if m.agentModels == nil {
		m.agentModels = map[agents.Kind]modelList{}
	}
	l := m.agentModels[k]
	if l.asking || (len(l.models) > 0 && time.Since(l.at) < modelsFor) {
		return nil
	}
	l.asking = true
	m.agentModels[k] = l
	specs := m.cfg.Agents
	return func() tea.Msg { return listModels(specs, k, false) }
}

// listModels asks k for its models, and what it starts with.
func listModels(specs map[agents.Kind]agents.Spec, k agents.Kind, again bool) modelsMsg {
	ms, err := agents.Models(specs, k)
	return modelsMsg{kind: k, models: ms, own: agents.OwnModel(specs, k), err: err, again: again}
}

// openModelPick starts picking the model kind reviews with, on the model
// it reviews with now.
func (m *Model) openModelPick(k agents.Kind) tea.Cmd {
	if !agents.TakesModel(k) {
		m.toast("nerdr cannot start " + string(k) + " with a model of its choosing")
		return nil
	}
	in := textinput.New()
	in.Prompt = "model: "
	in.Placeholder = "type to narrow, or a model of your own"
	in.Focus()
	m.reviewPick = &modelPick{kind: k, filter: in}
	if c := m.cfg.Review.Models[k]; c != "" {
		for i, ch := range m.choices(m.reviewPick) {
			if ch.model == c {
				m.reviewPick.cursor = i
			}
		}
	}
	return tea.Batch(textinput.Blink, m.askModels(k))
}

// onModels keeps the models an agent listed. An empty list is not kept:
// an agent can list none for a moment (opencode refreshing its own), so
// it is asked once more shortly, and again the next time.
func (m *Model) onModels(msg modelsMsg) tea.Cmd {
	if m.agentModels == nil {
		m.agentModels = map[agents.Kind]modelList{}
	}
	l := modelList{models: msg.models, own: msg.own}
	if len(msg.models) > 0 {
		l.at = time.Now()
	}
	if msg.err != nil {
		l.err = msg.err.Error()
	}
	var cmd tea.Cmd
	if len(msg.models) == 0 && msg.err == nil && !msg.again {
		l.asking = true
		specs, k := m.cfg.Agents, msg.kind
		cmd = tea.Tick(modelsAgain, func(time.Time) tea.Msg { return listModels(specs, k, true) })
	}
	m.agentModels[msg.kind] = l
	return cmd
}

// usedModels are the models k is known to have been started with, besides
// those it lists: the config's for reviews, and those of the agents of its
// kind running now.
func (m *Model) usedModels(k agents.Kind) []string {
	var out []string
	if c := m.cfg.Review.Models[k]; c != "" {
		out = append(out, c)
	}
	for _, a := range m.allAgents() {
		if a.Agent == k && a.Model != "" && !slices.Contains(out, a.Model) {
			out = append(out, a.Model)
		}
	}
	return out
}

// reviewModel is the model k reviews with, for its button in the review
// pane: the config's, or what it starts with by itself, or "" when that
// is not known.
func (m *Model) reviewModel(k agents.Kind) string {
	if c := m.cfg.Review.Models[k]; c != "" {
		return c
	}
	return m.agentModels[k].own
}

// pickModel starts of's review by kind with model, and keeps it in the
// config for the reviews to come; "" for the model kind starts with by
// itself.
func (m *Model) pickModel(of *tab, k agents.Kind, model string) tea.Cmd {
	m.reviewPick = nil
	if model != m.cfg.Review.Models[k] {
		if m.cfg.Review.Models == nil {
			m.cfg.Review.Models = map[agents.Kind]string{}
		}
		if model == "" {
			delete(m.cfg.Review.Models, k)
		} else {
			m.cfg.Review.Models[k] = model
		}
		value := ""
		if model != "" {
			value = config.Quote(model)
		}
		if err := config.SetKey("review.models", string(k), value); err != nil {
			m.toast(string(k) + ": " + cmp.Or(model, "its own model") + " for now; not saved: " + err.Error())
		}
	}
	return m.startReviewWith(of, reviewer{kind: k, model: model})
}

// pickChoice is a line of the list: a model, or the agent's own.
type pickChoice struct {
	model, label string
}

// choices are what can be picked: the model the agent starts with by
// itself, the one picked before and others it was started with, those it
// listed, all narrowed by what is typed, and what is typed, when it is
// none of them.
func (m *Model) choices(p *modelPick) []pickChoice {
	l := m.agentModels[p.kind]
	own := "the model it starts with"
	if l.own != "" {
		own = l.own
	}
	out := []pickChoice{{label: "its own: " + own}}
	typed := strings.TrimSpace(p.filter.Value())
	current := m.cfg.Review.Models[p.kind]
	seen := map[string]bool{}
	add := func(am agents.AgentModel, note string) {
		if seen[am.ID] || !modelMatches(am, typed) {
			return
		}
		seen[am.ID] = true
		label := am.ID
		if am.Name != "" && !strings.EqualFold(am.Name, am.ID) {
			label += dim("  " + am.Name)
		}
		if am.ID == current {
			note = "picked before"
		}
		if note != "" {
			label += dim("  · " + note)
		}
		out = append(out, pickChoice{model: am.ID, label: label})
	}
	listed := map[string]agents.AgentModel{}
	for _, am := range l.models {
		listed[am.ID] = am
	}
	for _, id := range m.usedModels(p.kind) {
		note := ""
		if _, ok := listed[id]; !ok {
			note = "used before"
		}
		add(cmp.Or(listed[id], agents.AgentModel{ID: id}), note)
	}
	for _, am := range l.models {
		add(am, "")
	}
	if typed != "" && !seen[typed] && !strings.ContainsAny(typed, " \t") {
		out = append(out, pickChoice{model: typed, label: "use " + typed})
	}
	return out
}

// modelMatches reports whether am is among those typed narrows the list
// to: each word typed, apart at spaces and dashes, is in its id or name,
// so "sonnet 5.5" finds claude-sonnet-5.5. The agent takes only its exact
// id, so enter starts it with the first that matches rather than with
// what is typed.
func modelMatches(am agents.AgentModel, typed string) bool {
	hay := strings.ToLower(am.ID + " " + am.Name)
	for _, w := range strings.FieldsFunc(strings.ToLower(typed), func(r rune) bool { return r == ' ' || r == '-' }) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// modelPickKey handles a key while the model is being picked: arrows
// move, enter starts the review, esc goes back to the agents, and the
// rest is typed into the filter.
func (m *Model) modelPickKey(of *tab, msg tea.KeyMsg) tea.Cmd {
	p := m.reviewPick
	cs := m.choices(p)
	switch msg.String() {
	case "esc":
		m.reviewPick = nil
		return nil
	case "up":
		p.cursor = max(p.cursor-1, 0)
		return nil
	case "down":
		p.cursor = min(p.cursor+1, len(cs)-1)
		return nil
	case "enter":
		c := cs[min(max(p.cursor, 0), len(cs)-1)]
		return m.pickModel(of, p.kind, c.model)
	}
	var cmd tea.Cmd
	before := p.filter.Value()
	p.filter, cmd = p.filter.Update(msg)
	if p.filter.Value() != before {
		// Typing names a model: the first that matches, or what is typed.
		p.cursor = 0
		if strings.TrimSpace(p.filter.Value()) != "" && len(m.choices(p)) > 1 {
			p.cursor = 1
		}
	}
	return cmd
}

// modelPickLines draws review pane p while the model is picked.
func (m *Model) modelPickLines(t *tab, pane *paneTab, w, h int) []string {
	p := m.reviewPick
	of := pane.of
	lines := []string{" Review " + m.agentLabel(t, of) + "'s work with " + string(p.kind) + ", with the model:", "",
		" " + p.filter.View(), ""}
	l := m.agentModels[p.kind]
	cs := m.choices(p)
	p.cursor = min(max(p.cursor, 0), len(cs)-1)
	switch {
	case l.asking:
		lines = append(lines, dim("   asking "+string(p.kind)+" for its models…"))
	case len(l.models) == 0 && l.err != "":
		lines = append(lines, dim("   "+string(p.kind)+" listed no models ("+l.err+"): type one"))
	case len(l.models) == 0:
		lines = append(lines, dim("   "+string(p.kind)+" lists no models: type one"))
	}
	foot := []string{"", " " + keyHint("↑↓") + dim(" pick · ") + keyHint("enter") + dim(" start · ") +
		dim("type to narrow · ") + keyHint("esc") + dim(" back")}
	room := max(h-len(lines)-len(foot), 1)
	start := 0
	if p.cursor >= room {
		start = p.cursor - room + 1
	}
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	for i := start; i < len(cs) && i < start+room; i++ {
		c := cs[i]
		cursor := "   "
		label := c.label
		if i == p.cursor {
			cursor, label = sel.Render(" ❯ "), sel.Render(label)
		}
		lines = append(lines, m.onClick(cursor+label, func(m *Model) tea.Cmd {
			return tea.Batch(m.goToPane(t, pane), m.pickModel(of, p.kind, c.model))
		}))
	}
	if more := len(cs) - start - room; more > 0 {
		lines = append(lines, dim("   … "+strconv.Itoa(more)+" more: type to narrow"))
	}
	return fitLines(append(lines, foot...), w, h)
}
