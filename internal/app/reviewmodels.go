package app

import (
	"strconv"
	"strings"
	"time"

	"nerdr/internal/agents"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// In a review pane, m on an agent picks the model it reviews with: from
// the models the agent itself lists (agents.Models), narrowed by what is
// typed, or what is typed when the agent lists none, or none of them.

// modelPick is the model being picked for kind.
type modelPick struct {
	kind   agents.Kind
	filter textinput.Model
	cursor int
}

// modelList is the models an agent listed, when, and why none.
type modelList struct {
	models []agents.AgentModel
	at     time.Time
	err    string
	asking bool
}

// modelsMsg carries the models kind listed.
type modelsMsg struct {
	kind   agents.Kind
	models []agents.AgentModel
	err    error
}

// modelsFor is how long an agent's list of models is kept before it is
// asked again.
const modelsFor = 10 * time.Minute

// openModelPick starts picking the model kind reviews with, asking it for
// its models when it has not been asked lately.
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
	if m.agentModels == nil {
		m.agentModels = map[agents.Kind]modelList{}
	}
	l := m.agentModels[k]
	if l.asking || (len(l.models) > 0 && time.Since(l.at) < modelsFor) {
		return textinput.Blink
	}
	l.asking = true
	m.agentModels[k] = l
	specs := m.cfg.Agents
	return tea.Batch(textinput.Blink, func() tea.Msg {
		ms, err := agents.Models(specs, k)
		return modelsMsg{kind: k, models: ms, err: err}
	})
}

// onModels keeps the models an agent listed. An empty list is not kept:
// an agent can list none for a moment (opencode refreshing its own), and
// is asked again next time.
func (m *Model) onModels(msg modelsMsg) {
	if m.agentModels == nil {
		m.agentModels = map[agents.Kind]modelList{}
	}
	l := modelList{models: msg.models}
	if len(msg.models) > 0 {
		l.at = time.Now()
	}
	if msg.err != nil {
		l.err = msg.err.Error()
	}
	m.agentModels[msg.kind] = l
}

// pickChoice is a line of the list: a model, or the agent's own.
type pickChoice struct {
	model, label string
}

// choices are what can be picked: the model the reviewer has otherwise,
// those listed that match what is typed, and what is typed, when it is
// none of them.
func (m *Model) choices(p *modelPick) []pickChoice {
	own := "the model it starts with"
	if c := m.cfg.Review.Models[p.kind]; c != "" {
		own = c + ", from the config"
	}
	out := []pickChoice{{label: "its own: " + own}}
	typed := strings.TrimSpace(p.filter.Value())
	exact := false
	for _, am := range m.agentModels[p.kind].models {
		if !modelMatches(am, typed) {
			continue
		}
		exact = exact || am.ID == typed
		label := am.ID
		if am.Name != "" && !strings.EqualFold(am.Name, am.ID) {
			label += dim("  " + am.Name)
		}
		out = append(out, pickChoice{model: am.ID, label: label})
	}
	if typed != "" && !exact && !strings.ContainsAny(typed, " \t") {
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
		m.reviewPick = nil
		return m.startReviewWith(of, reviewer{kind: p.kind, model: c.model})
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
			m.reviewPick = nil
			return tea.Batch(m.goToPane(t, pane), m.startReviewWith(of, reviewer{kind: p.kind, model: c.model}))
		}))
	}
	if more := len(cs) - start - room; more > 0 {
		lines = append(lines, dim("   … "+strconv.Itoa(more)+" more: type to narrow"))
	}
	return fitLines(append(lines, foot...), w, h)
}
