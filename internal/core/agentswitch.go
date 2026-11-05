package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/handoff"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// askHandoff asks t's agent for a handoff, and switches t to k once it has
// sent it, or once it is clear it will not (checkHandoff).
func (m *Core) AskHandoff(t *Tab, k agents.Kind) {
	if !m.Live(t) || k == t.Agent || t.Handoff != nil {
		return
	}
	w := &HandoffWait{To: k, Asked: state.Message{Text: handoff.Request(t.Agent, k), Auto: true}, AskedAt: time.Now()}
	// Ahead of whatever else waits for the agent: those are for the work,
	// and go to the new agent.
	t.Messages = append([]state.Message{w.Asked}, t.Messages...)
	t.Handoff = w
	m.SaveSession()
	m.Tell(PanesMsg{}) // the panel says it is waiting
	m.Toast(m.AgentName(t) + ": asked " + string(t.Agent) + " for a handoff to " + string(k))
}

// cancelHandoff gives up the switch t is waiting on, with the request
// taken back if it was not typed in yet.
func (m *Core) CancelHandoff(t *Tab) {
	w := t.Handoff
	if w == nil {
		return
	}
	if i := t.FindMessage(w.Asked); i >= 0 {
		t.DropMessage(i)
	}
	t.Handoff = nil
	m.SaveSession()
	m.Tell(PanesMsg{})
}

// checkHandoff switches t once its agent is not going to send the handoff
// it was asked for: it has stopped, did not take the request up, answered
// it and went idle without sending one, or took too long.
func (m *Core) CheckHandoff(t *Tab, now time.Time) Cmd {
	w := t.Handoff
	if w == nil {
		return nil
	}
	if !w.Sent && t.FindMessage(w.Asked) < 0 {
		w.Sent = true
	}
	idle := t.Light == StIdle || t.Light == StDone
	switch {
	case !t.AgentRunning() && t.Light != StStarting,
		t.QueuePaused,
		now.Sub(w.AskedAt) > HandoffGiveUp,
		w.Sent && !t.AwaitWork && idle && t.Pane.Quiet() >= HandoffGrace:
		return m.SwitchAgent(t, w.To, "")
	}
	return nil
}

// switchAgent stops t's agent and starts kind k in its place, continuing
// k's conversation in the worktree when it had one: the one it had in
// this tab, or for an agent nerdr cannot give an id to, the newest one it
// has in the worktree. k is given summary, the handoff the old agent
// wrote, as its first message, or with none, told to read the old agent's
// conversation.
func (m *Core) SwitchAgent(t *Tab, k agents.Kind, summary string) Cmd {
	if !m.Live(t) || k == t.Agent || m.ReposHold(t) {
		return nil
	}
	if w := t.Handoff; w != nil {
		if i := t.FindMessage(w.Asked); i >= 0 {
			t.DropMessage(i)
		}
		t.Handoff = nil
	}
	t.HandoffReady = nil
	if t.Pane != nil {
		old := t.Pane
		go old.Close()
		t.Pane = nil
	}
	if t.Sessions == nil {
		t.Sessions = map[agents.Kind]string{}
	}
	// An empty id still records that this agent has run here: Codex resumes
	// its directory's last session without reporting an id to nerdr.
	t.Sessions[t.Agent] = t.SessionID
	_, visited := t.Sessions[k]
	from, fromID, wasSandboxed := t.Agent, t.SessionID, t.InSandbox || m.SandboxedKind(t.Agent)
	t.Agent, t.SessionID = k, t.Sessions[k]
	// A model, and a resume command reported, are one kind's.
	t.Model, t.ResumeArgv = "", nil
	t.FallbackTried = false
	t.Light = StStarting
	t.Err = ""
	// What the old agent was sent is no concern of the new one's.
	t.AwaitWork, t.QueuePaused = false, false
	m.Tell(SwitchedMsg{T: t})
	m.SaveSession()
	m.Tell(PanesMsg{})
	m.Toast(m.AgentName(t) + ": switched to " + string(k))
	specs, id, dir, tabID := m.SpecsFor(t), t.SessionID, t.Dir, t.ID
	skip, shared := m.OthersSessions(t), m.SharesKind(t)
	worktree := dir
	if len(t.Worktrees) > 0 {
		worktree = t.Worktrees[0].Path
	}
	return func() Event {
		in := handoff.Info{From: from, To: k, Summary: summary}
		path, n, err := handoff.Write(specs, from, fromID, dir, worktree, tabID)
		if err != nil {
			in.TranscriptErr = err.Error()
		}
		in.Transcript, in.TranscriptLen = path, n
		if k == agents.Codex && (!visited || shared && id == "") {
			// Its --last could be the other codex's: only one known by
			// its id is carried on.
			return LaunchMsg{T: t, Kind: k, L: agents.NewSessionIn(specs, k, dir), Handoff: handoff.Prompt(in), Sandboxed: wasSandboxed}
		}
		l := agents.Resume(specs, k, id, dir, skip)
		if l.New || l.SessionID == "" && k != agents.Codex {
			// Nothing of k's to continue here; its own "continue the
			// latest" could pick up another worktree's conversation, or
			// a reviewer's in this one.
			return LaunchMsg{T: t, Kind: k, L: agents.NewSessionIn(specs, k, dir), Handoff: handoff.Prompt(in), Sandboxed: wasSandboxed}
		}
		in.Resumed = true
		return LaunchMsg{T: t, Kind: k, L: l, Resume: true, Handoff: handoff.Prompt(in), Sandboxed: wasSandboxed}
	}
}

const (
	// HandoffGrace is how long an agent that has answered the request and
	// gone idle without sending a handoff is given before it is switched
	// from anyway.
	HandoffGrace = 5 * time.Second
	// HandoffGiveUp is the longest the switch waits for a handoff at all.
	HandoffGiveUp = 5 * time.Minute
	// HandoffFresh is how long a handoff sent unasked — the user asked
	// the agent for one before switching — is kept for the next switch.
	HandoffFresh = 30 * time.Minute
)

// HandoffWait is a switch waiting for the old agent's handoff.
type HandoffWait struct {
	To agents.Kind
	// Asked is the request, in the tab's messages until it is typed in;
	// sent is set once it has been.
	Asked   state.Message
	AskedAt time.Time
	Sent    bool
}

// HandoffNote is a handoff an agent sent with no switch waiting for it.
type HandoffNote struct {
	From agents.Kind
	Text string
	At   time.Time
}

// ReadyHandoff is the handoff t's agent sent unasked, if it is still
// fresh.
func (t *Tab) ReadyHandoff(now time.Time) string {
	h := t.HandoffReady
	if h == nil || h.From != t.Agent || now.Sub(h.At) > HandoffFresh {
		return ""
	}
	return h.Text
}

// AgentRunning reports whether t's agent is up, to be asked for a
// handoff.
func (t *Tab) AgentRunning() bool {
	if t.Pane == nil || t.Light == StExited || t.Light == StStarting {
		return false
	}
	exited, _, _ := t.Pane.Exited()
	return !exited
}

// onHandoff takes the handoff an agent sent: the switch waiting for it
// goes ahead, or with none waiting, it is kept for the next one, which is
// offered at once when the user is in the tab.
func (m *Core) OnHandoff(r hooks.Report) Cmd {
	t := m.AgentByHook(r.Pane)
	if t == nil || t.ReviewOf != nil {
		return nil
	}
	if w := t.Handoff; w != nil {
		return m.SwitchAgent(t, w.To, r.Handoff)
	}
	t.HandoffReady = &HandoffNote{From: t.Agent, Text: r.Handoff, At: time.Now()}
	if t == m.FrontAgent() && m.Client.DialogClosed() {
		return m.Client.OpenSwitchAgent()
	}
	m.Toast(m.AgentName(t) + ": " + string(t.Agent) + " wrote a handoff; " + KeyRef("a s") + " switches the agent")
	return nil
}
