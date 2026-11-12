package core

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// The requests about a tab's panes, and the code reviews in them.

func init() {
	registerRequests(SeeAgent{}, ClosePane{}, DropPane{}, NewAgent{}, NewShell{}, EditInTab{}, MovePane{},
		OpenReviewPane{}, AskModels{}, StartReview{}, StopReview{}, SetRounds{}, DropReviewDone{},
		SendFindings{}, CarryOnConversation{})
}

// SeeAgent is the user going to agent A's pane, or looking at it: what it
// did is seen, and its queue, paused by the user's typing, goes on, unless
// they only glanced at it (Glance).
type SeeAgent struct {
	A      TabRef
	Glance bool
}

func (q SeeAgent) Do(m *Core) Cmd {
	if a := m.ByRef(q.A); a != nil {
		a.Seen()
		if !q.Glance {
			a.QueuePaused = false
		}
	}
	return nil
}

// ClosePane closes pane P, stopping what runs in it.
type ClosePane struct{ P PaneRef }

func (q ClosePane) Do(m *Core) Cmd {
	t, p := m.PaneByRef(q.P)
	if p == nil {
		return nil
	}
	return m.ClosePane(t, p, nil)
}

// DropPane takes pane P out of its tab, with nothing running in it: a
// review pane still picking its reviewer.
type DropPane struct{ P PaneRef }

func (q DropPane) Do(m *Core) Cmd {
	if t, p := m.PaneByRef(q.P); p != nil {
		m.DropPane(t, p)
	}
	return nil
}

// NewAgent starts another agent of Kind beside tab T's own, in a new pane.
type NewAgent struct {
	T    TabRef
	Kind agents.Kind
}

func (q NewAgent) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		return m.NewAgent(t, q.Kind)
	}
	return nil
}

// NewShell opens a terminal in tab T's worktree, in a new pane, last;
// with Quiet the keyboard stays where it is.
type NewShell struct {
	T     TabRef
	Quiet bool
}

func (q NewShell) Do(m *Core) Cmd {
	if t := m.ByRef(q.T); t != nil {
		_, cmd := m.NewShellIn(t, q.Quiet)
		return cmd
	}
	return nil
}

// EditInTab opens File, a file in tab T's worktree, on Line (none when 0),
// or with no File the worktree itself, in a terminal pane of T's that
// closes when the editor exits: for a client on another machine than
// core, where no editor of its own can reach the files. The editor is
// Setting (the client's editor or worktree_editor setting), else $VISUAL
// or $EDITOR as the programs core starts have them, else vim. One with a
// window of its own cannot open one here, and vim opens the file instead.
type EditInTab struct {
	T       TabRef
	File    string `json:",omitempty"`
	Line    int    `json:",omitempty"`
	Setting string `json:",omitempty"`
}

func (q EditInTab) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil || t.Dir == "" {
		return nil
	}
	env := m.BaseEnv()
	getenv := func(k string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], k+"="); ok {
				return v
			}
		}
		return ""
	}
	var c editor.Command
	if q.File == "" {
		c = editor.DirCommandIn(q.Setting, t.Dir)
	} else {
		c = editor.FileCommandIn(q.Setting, getenv, t.Dir, q.File, q.Line)
	}
	if c.Window {
		if q.File == "" {
			m.Toast(c.Argv[0] + " opens a window, which cannot show here: set worktree_editor to one in a terminal")
			return nil
		}
		c = editor.FileCommandIn("vim", getenv, t.Dir, q.File, q.Line)
	}
	_, cmd := m.newTermIn(t, false, c.Argv)
	return cmd
}

// MovePane moves pane P By places along its tab's panes, not past the
// ends; the tab's own agent stays first.
type MovePane struct {
	P  PaneRef
	By int
}

func (q MovePane) Do(m *Core) Cmd {
	t, p := m.PaneByRef(q.P)
	if p == nil {
		return nil
	}
	ps := t.Panes()
	i := IndexPane(ps, p)
	j := i + q.By
	if i < 1 || j < 1 || j >= len(ps) {
		return nil
	}
	ps[i], ps[j] = ps[j], ps[i]
	m.SaveSession()
	return m.Tell(LayoutMsg{})
}

// IndexPane is where p is in ps, -1 when it is not.
func IndexPane(ps []*PaneTab, p *PaneTab) int {
	for i, q := range ps {
		if q == p {
			return i
		}
	}
	return -1
}

// OpenReviewPane opens a pane in tab T to review agent Of's work in, for
// the user to pick the reviewer.
type OpenReviewPane struct{ T, Of TabRef }

func (q OpenReviewPane) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	return m.AddPane(t, &PaneTab{Kind: PaneReview, Of: m.ByRef(q.Of)}, false)
}

// AskModels has the models of agent Kind looked up, for the user to
// pick from.
type AskModels struct{ Kind agents.Kind }

func (q AskModels) Do(m *Core) Cmd { return m.AskModels(q.Kind) }

// StartReview starts reviewing agent Of's work By a reviewer, or adds
// the reviewer to the review going on.
type StartReview struct {
	Of TabRef
	By Reviewer
}

func (q StartReview) Do(m *Core) Cmd {
	of := m.ByRef(q.Of)
	if of == nil {
		return nil
	}
	if of.Review != nil {
		return Batch(m.AddReviewer(of, q.By), m.Tell(LayoutMsg{}))
	}
	return m.LookAtTree(of, TreeHand, q.By)
}

// StopReview stops the review of agent A's work.
type StopReview struct{ A TabRef }

func (q StopReview) Do(m *Core) Cmd {
	if a := m.ByRef(q.A); a != nil && a.Review != nil {
		m.EndReview(a, "stopped", false)
		return m.Tell(LayoutMsg{})
	}
	return nil
}

// SetRounds has the review of agent A's work go N rounds at most.
type SetRounds struct {
	A TabRef
	N int
}

func (q SetRounds) Do(m *Core) Cmd {
	if a := m.ByRef(q.A); a != nil && a.Review != nil {
		a.Review.Rounds = q.N
	}
	return nil
}

// DropReviewDone forgets the conclusions of the reviews of agents A: told
// of now, or no longer worth telling of.
type DropReviewDone struct{ A []TabRef }

func (q DropReviewDone) Do(m *Core) Cmd {
	for _, r := range q.A {
		if a := m.ByRef(r); a != nil {
			a.ReviewDone = nil
		}
	}
	m.SaveSession()
	return nil
}

// SendFindings sends agent A the findings of the review of its work,
// waiting for it as Msg.
type SendFindings struct {
	A   TabRef
	Msg state.Message
}

func (q SendFindings) Do(m *Core) Cmd {
	if a := m.ByRef(q.A); a != nil {
		m.SendFindings(a, q.Msg)
	}
	return nil
}

// CarryOnConversation carries on an agent's conversation in a new pane of
// tab T, as an agent of id ID: conversation Session of agent Kind, had in
// folder Dir.
type CarryOnConversation struct {
	T       TabRef
	ID      string
	Kind    agents.Kind
	Session string
	Dir     string
}

func (q CarryOnConversation) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	// In the folder the conversation was had in, which may be one of the
	// tab's repositories rather than the tab's own folder: the agent finds
	// its conversations by folder, and its paths are from there.
	a := &Tab{AgentState: Agent{Tab: state.Tab{ID: q.ID, Name: t.Name, Dir: q.Dir, Agent: q.Kind, Worktrees: t.Worktrees,
		Num: t.NextNum(), SessionID: q.Session}, Light: StStarting}, PartOf: t}
	cmd := m.AddPane(t, &PaneTab{Kind: PaneAgent, A: a}, false)
	start := m.Start(a, true)
	m.SaveSession()
	m.Toast(t.Name + ": " + m.AgentLabel(t, a) + " carries on the conversation")
	return Batch(cmd, start)
}
