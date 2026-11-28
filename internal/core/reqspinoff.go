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
	// ImportedSandboxed is provenance of files already in Wts, rather
	// than only of the task text. It must survive a failed agent start.
	ImportedSandboxed bool
}

func (q AddTab) Do(m *Core) Cmd {
	_, cmd := m.addTab(q)
	return cmd
}

// addTab is AddTab, and the tab it made.
func (m *Core) addTab(q AddTab) (*Tab, Cmd) {
	group := m.RememberGroup(q.Dir, q.Group, q.Wts)
	t := &Tab{AgentState: Agent{Tab: newSessionTab(q.ID, q.Name, q.Dir, q.Kind, q.Model, group, q.Wts)}}
	t.Sandboxed = q.ImportedSandboxed
	m.MarkSandboxed(t)
	TrustWorktrees(t)
	m.givePorts(t)
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
	// The part asked about, as core has it: the client's copy may be of
	// one gone through since, and may not have a prompt sent after the
	// plan yet.
	if t.Spinoff == nil || len(t.Spinoff.Next) == 0 || t.Spinoff.Next[0].ID != q.Part.ID {
		return m.spinoffFailed(t, q.Name, "this part is no longer the one asked about")
	}
	if t.SpinoffBusy != "" {
		// Being made, or held while its setup is asked about: once is
		// enough.
		m.Toast("spinoff " + q.Name + ": " + t.SpinoffBusy)
		return nil
	}
	if q.Part.Prompt = t.Spinoff.Next[0].Prompt; q.Part.Prompt == "" {
		return m.spinoffFailed(t, q.Name, "the agent has not sent this part's prompt yet")
	}
	var paths []string
	for _, r := range q.Repos {
		paths = append(paths, r.Path)
	}
	if q.Part.Sandboxed && q.Reuse {
		var imported []workspace.Worktree
		for _, r := range q.Repos {
			imported = append(imported, workspace.Worktree{Repo: r.Path})
		}
		m.ImportSandboxed(imported)
	}
	// The part is held from here, while the setup is asked about too: it
	// cannot be made a second time, or skipped, meanwhile, and the next
	// gone through in its place.
	t.SpinoffBusy, t.SpinoffFailed = "waiting to be told whether the repositories' setup may run…", nil
	return m.AskSetup(paths, func() Cmd {
		// The tab, or the part, may be gone while the user was asked.
		if m.TabIndex(t) < 0 || t.Spinoff == nil || len(t.Spinoff.Next) == 0 || t.Spinoff.Next[0].ID != q.Part.ID {
			t.SpinoffBusy = ""
			return nil
		}
		return m.SpinoffCreate(t, q.Part, q.Kind, q.Name, q.Reuse, q.Elsewhere, q.Group, q.Repos, q.Own)
	})
}

// SpinoffNext records Outcome as what became of tab T's first part, and
// goes on to the next. Job is that of the client's SpinoffHold for a part
// made on another machine: its outcome is taken only while the hold is
// still Job's. With no Job, as for a part skipped, the part must not be
// being made, nor held, by anyone.
type SpinoffNext struct {
	T       TabRef
	Outcome spinoff.Outcome
	Job     string
}

func (q SpinoffNext) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	name := q.Outcome.Branch
	if name == "" {
		name = q.Outcome.Part.Branch
	}
	switch {
	case q.Job != "" && t.SpinoffHold != q.Job:
		// Held too long, and let go of: what it made is no longer the
		// part's, which may be answered another way since.
		m.Toast("spinoff " + name + ": made too late to count for the part, which waits to be gone through again")
		return nil
	case q.Job == "" && t.SpinoffBusy != "":
		m.Toast("spinoff " + name + ": " + t.SpinoffBusy)
		return nil
	}
	return m.SpinoffNext(t, q.Outcome)
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
	if msg.Outcome.Part.Sandboxed && msg.Outcome.Reused {
		m.ImportSandboxed(msg.Wts)
	}
	t.SpinoffBusy = ""
	if msg.Err != nil {
		return m.spinoffFailed(t, msg.Outcome.Branch, msg.Err.Error())
	}
	parent := spinoff.Parent{ID: t.ID, Agent: 1, Name: t.Name, Dir: t.Dir}
	if len(t.Worktrees) > 0 {
		parent.Branch = t.Worktrees[0].Branch
	}
	// The part goes back to the agent of the tab that sent it, which
	// may be another than its first.
	if a := m.AgentByID(msg.Outcome.Part.From); a != nil && a.Owner() == t {
		parent.Agent = a.Num
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
			Sandboxed: msg.Outcome.Part.Sandboxed, ImportedSandboxed: msg.Outcome.Part.Sandboxed && msg.Outcome.Reused})
		msg.Outcome.Tab = nt.ID
		m.LinkTabs(t, nt)
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
	if o.Part.ID != p.Next[0].ID {
		// Of a part gone through since, another way: the one first now is
		// not to go in its place.
		return nil
	}
	p.Done = append(p.Done, o)
	p.Next = p.Next[1:]
	// What was done about the part gone through, and why it failed.
	t.SpinoffFailed, t.SpinoffBusy, t.SpinoffHold, t.SpinoffHeldUntil = nil, "", "", time.Time{}
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
