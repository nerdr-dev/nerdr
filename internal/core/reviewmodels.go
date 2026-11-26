package core

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
)

// ModelList is the models an agent listed, when, and why none; own is
// the model it starts with when nerdr names none (agents.OwnModel).
type ModelList struct {
	Models []agents.AgentModel
	Own    string
	At     time.Time
	Err    string
	Asking bool
}

// ModelsMsg carries the models kind listed; again is set on the second
// asking, after the first listed none.
type ModelsMsg struct {
	Kind   agents.Kind
	Models []agents.AgentModel
	Own    string
	Err    error
	Again  bool
}

// modelsFor is how long an agent's list of models is kept before it is
// asked again; modelsAgain how long after listing none it is asked once
// more: opencode lists none while it refreshes its own.
const (
	ModelsFor   = 10 * time.Minute
	ModelsAgain = 2 * time.Second
)

// AskModels asks k for its models, when it has not been asked lately.
func (m *Core) AskModels(k agents.Kind) Cmd {
	if !agents.TakesModel(k) {
		return nil
	}
	if m.AgentModels == nil {
		m.AgentModels = map[agents.Kind]ModelList{}
	}
	l := m.AgentModels[k]
	if l.Asking || (len(l.Models) > 0 && time.Since(l.At) < ModelsFor) {
		return nil
	}
	l.Asking = true
	m.AgentModels[k] = l
	specs := m.Cfg.Agents
	return func() Event { return ListModels(specs, k, false) }
}

// ListModels asks k for its models, and what it starts with.
func ListModels(specs map[agents.Kind]agents.Spec, k agents.Kind, again bool) ModelsMsg {
	ms, err := agents.Models(specs, k)
	return ModelsMsg{Kind: k, Models: ms, Own: agents.OwnModel(specs, k), Err: err, Again: again}
}

// OnModels keeps the models an agent listed. An empty list is not kept:
// an agent can list none for a moment (opencode refreshing its own), so
// it is asked once more shortly, and again the next time.
func (m *Core) OnModels(msg ModelsMsg) Cmd {
	if m.AgentModels == nil {
		m.AgentModels = map[agents.Kind]ModelList{}
	}
	l := ModelList{Models: msg.Models, Own: msg.Own}
	if len(msg.Models) > 0 {
		l.At = time.Now()
	}
	if msg.Err != nil {
		l.Err = msg.Err.Error()
	}
	var cmd Cmd
	if len(msg.Models) == 0 && msg.Err == nil && !msg.Again {
		l.Asking = true
		specs, k := m.Cfg.Agents, msg.Kind
		cmd = m.Tick(ModelsAgain, func(time.Time) Event { return ListModels(specs, k, true) })
	}
	m.AgentModels[msg.Kind] = l
	return cmd
}

// UsedModels are the models k is known to have been started with, besides
// those it lists: the config's for reviews, and those of the agents of its
// kind running now.
func (m *Core) UsedModels(k agents.Kind) []string {
	var out []string
	if c := m.Cfg.Review.Models[k]; c != "" {
		out = append(out, c)
	}
	for _, a := range m.AllAgents() {
		if a.Agent == k && a.Model != "" && !slices.Contains(out, a.Model) {
			out = append(out, a.Model)
		}
	}
	return out
}

// ReviewModel is the model k reviews with, for its button in the review
// pane: the config's, or what it starts with by itself, or "" when that
// is not known.
func (m *Core) ReviewModel(k agents.Kind) string {
	if c := m.Cfg.Review.Models[k]; c != "" {
		return c
	}
	return m.AgentModels[k].Own
}

// PickChoice is a line of the list: a model, or the agent's own.
type PickChoice struct {
	Model, Label string
}

// ModelMatches reports whether am is among those typed narrows the list
// to: each word typed, apart at spaces and dashes, is in its id or name,
// so "sonnet 5.5" finds claude-sonnet-5.5. The agent takes only its exact
// id, so enter starts it with the first that matches rather than with
// what is typed.
func ModelMatches(am agents.AgentModel, typed string) bool {
	hay := strings.ToLower(am.ID + " " + am.Name)
	for _, w := range strings.FieldsFunc(strings.ToLower(typed), func(r rune) bool { return r == ' ' || r == '-' }) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// ResolveModel resolves model, as the user named it for k to review with
// ("astra 6.2"), into k's own name for it, from the models k lists and
// those it has been started with (UsedModels). With model_names =
// "nerdr" a name that matches one of them better than the rest is that
// one, and one that matches several equally well is refused with them,
// for the agent to ask the user; with "agent", only k's own names are
// taken, and a loose one is refused with where to find them. A name
// that matches none of them is taken as it is: the lists can be short of
// what k takes (claude lists only its aliases), and one that cannot be
// a name is refused.
func (m *Core) ResolveModel(k agents.Kind, model string) (string, error) {
	known := slices.Clone(m.AgentModels[k].Models)
	for _, u := range m.UsedModels(k) {
		if !slices.ContainsFunc(known, func(am agents.AgentModel) bool { return am.ID == u }) {
			known = append(known, agents.AgentModel{ID: u})
		}
	}
	for _, am := range known {
		if strings.EqualFold(am.ID, model) {
			return am.ID, nil
		}
	}
	var matches []agents.AgentModel
	for _, am := range known {
		if ModelMatches(am, model) {
			matches = append(matches, am)
		}
	}
	loose := strings.ContainsAny(model, " \t")
	if m.Cfg.Review.ModelNames == config.NamesByAgent {
		if !loose && len(matches) == 0 {
			return model, nil
		}
		where := agents.ModelsListing(k)
		if where != "" {
			where = " (" + where + ")"
		}
		return "", fmt.Errorf("%s has no model named %q, and nerdr is set to take only %s's own names for its models "+
			"(model_names = \"agent\" in [review]): find its name for the model the user meant%s and run the command again with it",
			k, model, k, where)
	}
	switch best := bestModels(matches, model); {
	case len(best) == 1:
		return best[0].ID, nil
	case len(best) > 1:
		return "", fmt.Errorf("%q could be any of %s's models %s: ask the user which they mean, and give its name", model, k, modelIDs(best))
	case loose && len(known) == 0:
		return "", fmt.Errorf("nerdr could not get %s's models to find %q among: give %s's own name for the model", k, model, k)
	case loose:
		return "", fmt.Errorf("none of %s's models is %q; it has %s: ask the user which they mean, and give its name", k, model, modelIDs(known))
	}
	return model, nil
}

// bestModels are those of ms that have the fewest words besides those of
// typed: "astra 6.2" is gpt-6.2-astra rather than gpt-6.2-astra-mini.
func bestModels(ms []agents.AgentModel, typed string) []agents.AgentModel {
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '/' })
	}
	want := words(typed)
	extra := func(am agents.AgentModel) int {
		n := 0
		for _, w := range words(am.ID) {
			if !slices.ContainsFunc(want, func(t string) bool { return strings.Contains(w, t) }) {
				n++
			}
		}
		return n
	}
	var out []agents.AgentModel
	least := -1
	for _, am := range ms {
		switch n := extra(am); {
		case least < 0 || n < least:
			out, least = []agents.AgentModel{am}, n
		case n == least:
			out = append(out, am)
		}
	}
	return out
}

// modelIDs lists ms by id, the first few.
func modelIDs(ms []agents.AgentModel) string {
	var ids []string
	for i, am := range ms {
		if i == 12 {
			ids = append(ids, "…")
			break
		}
		ids = append(ids, am.ID)
	}
	return strings.Join(ids, ", ")
}
