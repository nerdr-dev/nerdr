package api

import (
	"slices"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// Event is what a subscribed client is told, as it happens:
//
//   - "tab_opened", Tab: a tab is there, made by anyone.
//   - "tab_closed", TabID, Name: a tab is gone.
//   - "tab", Tab: a tab's name, task, status, branches or terminals
//     changed.
//   - "agent", TabID, Agent, Was: an agent's state changed (Was is "" for
//     one just come, and Agent.State "gone" for one gone).
//   - "waiting", Waiting: the agents that want the user changed, neediest
//     first.
//   - "question", Question: nerdr asks the user something.
//   - "answered", QuestionID: a question is answered, here or elsewhere,
//     or its time ran out.
//   - "notice", Text, Important: what nerdr tells the user in its status
//     bar; Important for what it keeps on screen until seen.
//   - "lost": the client was too slow, and events were dropped; get the
//     state afresh.
type Event struct {
	Event      string            `json:"event"`
	Tab        *core.APITab      `json:"tab,omitempty"`
	TabID      string            `json:"tab_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Agent      *core.APIAgent    `json:"agent,omitempty"`
	Was        string            `json:"was,omitempty"`
	Waiting    []string          `json:"waiting,omitempty"`
	Question   *core.APIQuestion `json:"question,omitempty"`
	QuestionID int               `json:"question_id,omitempty"`
	Text       string            `json:"text,omitempty"`
	Important  bool              `json:"important,omitempty"`
}

// Diff is the events that take a client from state a to state b.
func Diff(a, b core.APIState) []Event {
	var out []Event
	before := map[string]core.APITab{}
	for _, t := range a.Tabs {
		before[t.ID] = t
	}
	now := map[string]bool{}
	for _, t := range b.Tabs {
		now[t.ID] = true
		old, ok := before[t.ID]
		if !ok {
			out = append(out, Event{Event: "tab_opened", Tab: &t})
			for _, ag := range t.Agents {
				out = append(out, Event{Event: "agent", TabID: t.ID, Agent: &ag})
			}
			continue
		}
		if tabChanged(old, t) {
			out = append(out, Event{Event: "tab", Tab: &t})
		}
		was := map[string]core.APIAgent{}
		for _, ag := range old.Agents {
			was[ag.ID] = ag
		}
		for _, ag := range t.Agents {
			o, ok := was[ag.ID]
			delete(was, ag.ID)
			switch {
			case !ok:
				out = append(out, Event{Event: "agent", TabID: t.ID, Agent: &ag})
			case o != agentKey(ag, o):
				out = append(out, Event{Event: "agent", TabID: t.ID, Agent: &ag, Was: o.State})
			}
		}
		for _, ag := range old.Agents {
			if _, gone := was[ag.ID]; gone {
				g := ag
				g.State = "gone"
				out = append(out, Event{Event: "agent", TabID: t.ID, Agent: &g, Was: ag.State})
			}
		}
	}
	for _, t := range a.Tabs {
		if !now[t.ID] {
			out = append(out, Event{Event: "tab_closed", TabID: t.ID, Name: t.Name})
		}
	}
	if !slices.Equal(a.Waiting, b.Waiting) {
		out = append(out, Event{Event: "waiting", Waiting: append([]string{}, b.Waiting...)})
	}
	asked := map[int]bool{}
	for _, q := range a.Questions {
		asked[q.ID] = true
	}
	still := map[int]bool{}
	for _, q := range b.Questions {
		still[q.ID] = true
		if !asked[q.ID] {
			out = append(out, Event{Event: "question", Question: &q})
		}
	}
	for _, q := range a.Questions {
		if !still[q.ID] {
			out = append(out, Event{Event: "answered", QuestionID: q.ID})
		}
	}
	return out
}

// agentKey is ag as it counts for a change, to compare with o: the
// context used is left as o had it, since it changes all the time an
// agent works and is not news of its own.
func agentKey(ag, o core.APIAgent) core.APIAgent {
	ag.Context = o.Context
	return ag
}

// tabChanged reports whether tab a and b differ other than in their
// agents, which have events of their own.
func tabChanged(a, b core.APITab) bool {
	if a.Name != b.Name || a.Group != b.Group || a.Dir != b.Dir || a.Task != b.Task || a.Status != b.Status ||
		len(a.Terminals) != len(b.Terminals) || len(a.Branches) != len(b.Branches) {
		return true
	}
	for i := range a.Terminals {
		if a.Terminals[i] != b.Terminals[i] {
			return true
		}
	}
	for i := range a.Branches {
		if a.Branches[i] != b.Branches[i] {
			return true
		}
	}
	return false
}
