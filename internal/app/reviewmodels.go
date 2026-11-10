package app

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"

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
	if c := m.tabOn().Core.Cfg.Review.Models[k]; c != "" {
		for i, ch := range m.choices(m.reviewPick) {
			if ch.Model == c {
				m.reviewPick.cursor = i
			}
		}
	}
	return tea.Batch(textinput.Blink, m.do(core.AskModels{Kind: k}))
}

// pickModel starts of's review by kind with model, and keeps it in the
// config for the reviews to come; "" for the model kind starts with by
// itself.
func (m *Model) pickModel(of *core.Tab, k agents.Kind, model string) tea.Cmd {
	m.reviewPick = nil
	if model != m.tabOn().Core.Cfg.Review.Models[k] {
		if m.tabOn().Core.Cfg.Review.Models == nil {
			m.tabOn().Core.Cfg.Review.Models = map[agents.Kind]string{}
		}
		if model == "" {
			delete(m.tabOn().Core.Cfg.Review.Models, k)
		} else {
			m.tabOn().Core.Cfg.Review.Models[k] = model
		}
		value := ""
		if model != "" {
			value = config.Quote(model)
		}
		save := m.editCoreConfig(string(k)+"'s model", core.EditConfig{
			Keys: []config.Edit{{Table: "review.models", Key: string(k), Value: value}}})
		return tea.Batch(save, m.startReviewWith(of, core.Reviewer{Kind: k, Model: model}))
	}
	return m.startReviewWith(of, core.Reviewer{Kind: k, Model: model})
}

// choices are what can be picked: the model the agent starts with by
// itself, the one picked before and others it was started with, those it
// listed, all narrowed by what is typed, and what is typed, when it is
// none of them.
func (m *Model) choices(p *modelPick) []core.PickChoice {
	l := m.tabOn().Core.AgentModels[p.kind]
	own := "the model it starts with"
	if l.Own != "" {
		own = l.Own
	}
	out := []core.PickChoice{{Label: "its own: " + own}}
	typed := strings.TrimSpace(p.filter.Value())
	current := m.tabOn().Core.Cfg.Review.Models[p.kind]
	seen := map[string]bool{}
	add := func(am agents.AgentModel, note string) {
		if seen[am.ID] || !core.ModelMatches(am, typed) {
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
		out = append(out, core.PickChoice{Model: am.ID, Label: label})
	}
	listed := map[string]agents.AgentModel{}
	for _, am := range l.Models {
		listed[am.ID] = am
	}
	for _, id := range m.tabOn().Core.UsedModels(p.kind) {
		note := ""
		if _, ok := listed[id]; !ok {
			note = "used before"
		}
		add(cmp.Or(listed[id], agents.AgentModel{ID: id}), note)
	}
	for _, am := range l.Models {
		add(am, "")
	}
	if typed != "" && !seen[typed] && !strings.ContainsAny(typed, " \t") {
		out = append(out, core.PickChoice{Model: typed, Label: "use " + typed})
	}
	return out
}

// modelPickKey handles a key while the model is being picked: arrows
// move, enter starts the review, esc goes back to the agents, and the
// rest is typed into the filter.
func (m *Model) modelPickKey(of *core.Tab, msg tea.KeyMsg) tea.Cmd {
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
		return m.pickModel(of, p.kind, c.Model)
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
func (m *Model) modelPickLines(t *core.Tab, pane *core.PaneTab, w, h int) []string {
	p := m.reviewPick
	of := pane.Of
	lines := []string{" Review " + m.machineOf(t).Core.AgentLabel(t, of) + "'s work with " + string(p.kind) + ", with the model:", "",
		" " + p.filter.View(), ""}
	l := m.tabOn().Core.AgentModels[p.kind]
	cs := m.choices(p)
	p.cursor = min(max(p.cursor, 0), len(cs)-1)
	switch {
	case l.Asking:
		lines = append(lines, dim("   asking "+string(p.kind)+" for its models…"))
	case len(l.Models) == 0 && l.Err != "":
		lines = append(lines, dim("   "+string(p.kind)+" listed no models ("+l.Err+"): type one"))
	case len(l.Models) == 0:
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
		label := c.Label
		if i == p.cursor {
			cursor, label = sel.Render(" ❯ "), sel.Render(label)
		}
		lines = append(lines, m.onClick(cursor+label, func(m *Model) tea.Cmd {
			return tea.Batch(m.goToPane(t, pane), m.pickModel(of, p.kind, c.Model))
		}))
	}
	if more := len(cs) - start - room; more > 0 {
		lines = append(lines, dim("   … "+strconv.Itoa(more)+" more: type to narrow"))
	}
	return fitLines(append(lines, foot...), w, h)
}
