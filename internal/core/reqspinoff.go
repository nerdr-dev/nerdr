package core

import (
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The requests that make tabs: from the wizard, and for the parts an
// agent spun off.

func init() { registerRequests(AddTab{}, SpinoffCreate{}, SpinoffNext{}) }

// AddTab opens a tab of id ID on worktrees Wts, in the group named Group
// if they span several repositories, and starts its agent of Kind (with
// Model, or its own). With CarryOn the agent carries on the conversation
// it had in them, if any. Task, if any, is its first message, marked
// Sandboxed when an agent in a sandbox gave it; Goal is shown for its
// task.
type AddTab struct {
	ID, Name, Dir, Group string
	Wts                  []workspace.Worktree
	Kind                 agents.Kind
	Model, Task, Goal    string
	Sandboxed, CarryOn   bool
}

func (q AddTab) Do(m *Core) Cmd {
	_, cmd := m.addTab(q)
	return cmd
}

// addTab is AddTab, and the tab it made.
func (m *Core) addTab(q AddTab) (*Tab, Cmd) {
	group := m.RememberGroup(q.Dir, q.Group, q.Wts)
	t := &Tab{AgentState: Agent{Tab: newSessionTab(q.ID, q.Name, q.Dir, q.Kind, q.Model, group, q.Wts)}}
	TrustWorktrees(t)
	m.Tabs = append(m.Tabs, t)
	m.AddMessageFrom(t, q.Task, true, q.Sandboxed) // before the start, which may take it
	var cmd Cmd
	if q.CarryOn {
		cmd = m.CarryOn(t)
	} else {
		cmd = m.Start(t, false)
	}
	if g := strings.TrimSpace(q.Goal); g != "" {
		t.Goal = g
	}
	m.SaveSession()
	return t, Batch(cmd, m.Tell(LayoutMsg{}))
}

// SpinoffCreate makes the worktrees and tab of tab T's first part, Part,
// once the user has allowed the repositories' setup; see Core.SpinoffCreate.
type SpinoffCreate struct {
	T         TabRef
	Part      spinoff.Part
	Kind      agents.Kind
	Name      string
	Reuse     bool
	Elsewhere bool
	Group     string
	Repos     []workspace.Repo
	Own       map[string]bool
}

func (q SpinoffCreate) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	var paths []string
	for _, r := range q.Repos {
		paths = append(paths, r.Path)
	}
	return m.AskSetup(paths, func() Cmd {
		return m.SpinoffCreate(t, q.Part, q.Kind, q.Name, q.Reuse, q.Elsewhere, q.Group, q.Repos, q.Own)
	})
}

// SpinoffNext records Outcome as what became of tab T's first part, and
// goes on to the next.
type SpinoffNext struct {
	T       TabRef
	Outcome spinoff.Outcome
}

func (q SpinoffNext) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		return m.SpinoffNext(t, q.Outcome)
	}
	return nil
}

// SpinoffFailedMsg is the tab for tab T's first part not made, for the
// user to put right: a branch name taken, say.
type SpinoffFailedMsg struct {
	T      TabRef
	Branch string
	Err    string
}

// SpinoffNextMsg is tab T's first part done with: Left are still to go
// through, its first now in front.
type SpinoffNextMsg struct {
	T    TabRef
	Left int
}

// SpinoffMade takes the result of making a part's tab: the tab is opened
// beside its parent, the part is done, and the client asks about the next.
func (m *Core) SpinoffMade(msg SpinoffMsg) Cmd {
	t := msg.T
	t.SpinoffBusy = ""
	if msg.Err != nil {
		return m.Tell(SpinoffFailedMsg{T: m.Ref(t), Branch: msg.Outcome.Branch, Err: msg.Err.Error()})
	}
	parent := spinoff.Parent{Name: t.Name, Dir: t.Dir}
	if len(t.Worktrees) > 0 {
		parent.Branch = t.Worktrees[0].Branch
	}
	if pt := msg.Outcome.Part; pt.Sandboxed && !m.SandboxedKind(msg.Kind) {
		// The config changed while the worktree was made.
		msg.Outcome.Err = string(msg.Kind) + " is no longer set sandboxed; the worktree is made, with no agent in it"
		m.Toast("spinoff " + msg.Outcome.Branch + ": " + msg.Outcome.Err)
		return m.SpinoffNext(t, msg.Outcome)
	}
	var cmds []Cmd
	if m.TabIndex(t) >= 0 {
		nt, cmd := m.addTab(AddTab{ID: NewTabID(), Name: msg.Plan.Branch, Dir: msg.Plan.Dir, Group: msg.Group, Wts: msg.Wts,
			Kind: msg.Kind, Task: spinoff.Prompt(msg.Outcome.Part, parent, msg.Outcome), Goal: msg.Outcome.Part.Summary,
			Sandboxed: msg.Outcome.Part.Sandboxed})
		msg.Outcome.Tab = nt.ID
		cmds = append(cmds, cmd, m.Tell(PanesMsg{}))
	}
	note := "made " + msg.Plan.Branch
	if msg.Outcome.PickFailed != "" {
		note += ": its commits did not apply cleanly, so its agent brings them in"
	}
	if len(msg.Warnings) > 0 {
		note += "; " + strings.Join(msg.Warnings, "; ")
	}
	m.Toast(note)
	return Batch(append(cmds, m.SpinoffNext(t, msg.Outcome))...)
}

// SpinoffNext records what became of t's first part, and moves on: to the
// next part, or, with none left, to telling the agent that sent them.
func (m *Core) SpinoffNext(t *Tab, o spinoff.Outcome) Cmd {
	p := t.Spinoff
	if p == nil || len(p.Next) == 0 {
		return nil
	}
	p.Done = append(p.Done, o)
	p.Next = p.Next[1:]
	if len(p.Next) > 0 {
		m.SaveSession()
		return m.Tell(SpinoffNextMsg{T: m.Ref(t), Left: len(p.Next)})
	}
	t.Spinoff = nil
	t.SpinoffReport, t.SpinoffSince = append(t.SpinoffReport, p.Done...), time.Time{}
	m.SaveSession()
	// The light is the agent's again, and the report may go.
	return Batch(m.Tell(SpinoffNextMsg{T: m.Ref(t)}), m.DetectAll(time.Now()))
}
