package app

import (
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/handoff"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

// A tab's agent can be swapped for another in the same worktree — claude
// for opencode, say — without closing the tab. The agent that is stopped
// keeps its conversation in the tab, so switching back continues it.
//
// The agent taking over is told what the old one did (internal/handoff).
// The user chooses whether a running agent is first asked to write a
// handoff. Without one, the new agent reads the old one's conversation
// from its log. With one asked for, the switch waits for it — but not for
// long, and not at all for an agent that cannot: one that answers without
// sending a handoff (out of tokens, say), does not take the message up, or
// has stopped is switched from at once, as if none had been asked for.

const (
	// handoffGrace is how long an agent that has answered the request and
	// gone idle without sending a handoff is given before it is switched
	// from anyway.
	handoffGrace = 5 * time.Second
	// handoffGiveUp is the longest the switch waits for a handoff at all.
	handoffGiveUp = 5 * time.Minute
	// handoffFresh is how long a handoff sent unasked — the user asked
	// the agent for one before switching — is kept for the next switch.
	handoffFresh = 30 * time.Minute
)

// handoffWait is a switch waiting for the old agent's handoff.
type handoffWait struct {
	to agents.Kind
	// asked is the request, in the tab's messages until it is typed in;
	// sent is set once it has been.
	asked   state.Message
	askedAt time.Time
	sent    bool
}

// handoffNote is a handoff an agent sent with no switch waiting for it.
type handoffNote struct {
	from agents.Kind
	text string
	at   time.Time
}

// readyHandoff is the handoff t's agent sent unasked, if it is still
// fresh.
func (t *tab) readyHandoff(now time.Time) string {
	h := t.handoffReady
	if h == nil || h.from != t.Agent || now.Sub(h.at) > handoffFresh {
		return ""
	}
	return h.text
}

// openSwitchAgent offers the other agents on PATH in place of the agent
// whose pane has the keyboard, or, while a switch is waiting for a
// handoff, not to wait.
func (m *Model) openSwitchAgent() tea.Cmd {
	t := m.curAgent()
	if t == nil {
		return nil
	}
	if w := t.handoff; w != nil {
		m.dialog = &menu{title: "Switch " + m.agentName(t) + " to " + string(w.to), x: m.width / 3, y: m.height / 3, hi: 1, items: []menuItem{
			{label: "Waiting for " + string(t.Agent) + "'s handoff"},
			{label: "Switch now: " + string(w.to) + " reads " + string(t.Agent) + "'s conversation", key: "1",
				run: func(m *Model) tea.Cmd { return m.switchAgent(t, w.to, "") }},
			{label: "Cancel the switch", key: "2",
				run: func(m *Model) tea.Cmd { m.cancelHandoff(t); return nil }},
		}}
		return nil
	}
	var items []menuItem
	for _, k := range agents.Available(m.cfg.Agents) {
		if k == t.Agent {
			continue
		}
		label := string(k)
		_, visited := t.Sessions[k]
		if t.Sessions[k] != "" || (k == agents.Codex && visited) {
			label += " · continues its conversation"
		}
		items = append(items, menuItem{label: label, key: strconv.Itoa(len(items) + 1),
			run: func(m *Model) tea.Cmd { return m.pickAgent(t, k) }})
	}
	if len(items) == 0 {
		m.toast("no other agent on PATH")
		return nil
	}
	head := "Stops " + string(t.Agent) + "; its conversation is kept"
	if t.readyHandoff(time.Now()) != "" {
		head = "Stops " + string(t.Agent) + "; the next agent gets the handoff it wrote"
	}
	items = append([]menuItem{{label: head}}, items...)
	m.dialog = &menu{title: "Switch " + m.agentName(t) + " to", items: items, x: m.width / 3, y: m.height / 3, hi: 1}
	return nil
}

// agentRunning reports whether t's agent is up, to be asked for a
// handoff.
func (t *tab) agentRunning() bool {
	if t.pane == nil || t.status == stExited || t.status == stStarting {
		return false
	}
	exited, _, _ := t.pane.Exited()
	return !exited
}

// pickAgent switches t to k with the handoff its agent already wrote, or
// with its agent running, asks whether to ask it for one first; else it
// switches at once.
func (m *Model) pickAgent(t *tab, k agents.Kind) tea.Cmd {
	if !m.live(t) || k == t.Agent {
		return nil
	}
	if text := t.readyHandoff(time.Now()); text != "" {
		return m.switchAgent(t, k, text)
	}
	if !t.agentRunning() || t.queuePaused {
		return m.switchAgent(t, k, "")
	}
	// Asking costs the old agent a turn and the user a wait, and is no use
	// when it is out of tokens: the user knows which, nerdr does not.
	old := t.Agent
	m.dialog = &menu{title: "Switch " + m.agentName(t) + " to " + string(k), x: m.width / 3, y: m.height / 3, hi: 1, items: []menuItem{
		{label: string(k) + " is told what " + string(old) + " did"},
		{label: "Switch now: " + string(k) + " reads " + string(old) + "'s conversation", key: "1",
			run: func(m *Model) tea.Cmd { return m.switchAgent(t, k, "") }},
		{label: "Ask " + string(old) + " for a handoff first", key: "2",
			run: func(m *Model) tea.Cmd { m.askHandoff(t, k); return nil }},
	}}
	return nil
}

// askHandoff asks t's agent for a handoff, and switches t to k once it has
// sent it, or once it is clear it will not (checkHandoff).
func (m *Model) askHandoff(t *tab, k agents.Kind) {
	if !m.live(t) || k == t.Agent || t.handoff != nil {
		return
	}
	w := &handoffWait{to: k, asked: state.Message{Text: handoff.Request(t.Agent, k), Auto: true}, askedAt: time.Now()}
	// Ahead of whatever else waits for the agent: those are for the work,
	// and go to the new agent.
	t.Messages = append([]state.Message{w.asked}, t.Messages...)
	t.handoff = w
	m.saveSession()
	m.resizePanes() // the panel says it is waiting
	m.toast(m.agentName(t) + ": asked " + string(t.Agent) + " for a handoff to " + string(k))
}

// cancelHandoff gives up the switch t is waiting on, with the request
// taken back if it was not typed in yet.
func (m *Model) cancelHandoff(t *tab) {
	w := t.handoff
	if w == nil {
		return
	}
	if i := t.findMessage(w.asked); i >= 0 {
		t.dropMessage(i)
	}
	t.handoff = nil
	m.saveSession()
	m.resizePanes()
}

// checkHandoff switches t once its agent is not going to send the handoff
// it was asked for: it has stopped, did not take the request up, answered
// it and went idle without sending one, or took too long.
func (m *Model) checkHandoff(t *tab, now time.Time) tea.Cmd {
	w := t.handoff
	if w == nil {
		return nil
	}
	if !w.sent && t.findMessage(w.asked) < 0 {
		w.sent = true
	}
	idle := t.status == stIdle || t.status == stDone
	switch {
	case !t.agentRunning() && t.status != stStarting,
		t.queuePaused,
		now.Sub(w.askedAt) > handoffGiveUp,
		w.sent && !t.awaitWork && idle && t.pane.Quiet() >= handoffGrace:
		return m.switchAgent(t, w.to, "")
	}
	return nil
}

// onHandoff takes the handoff an agent sent: the switch waiting for it
// goes ahead, or with none waiting, it is kept for the next one, which is
// offered at once when the user is in the tab.
func (m *Model) onHandoff(r hooks.Report) tea.Cmd {
	t := m.agentByHook(r.Pane)
	if t == nil || t.reviewOf != nil {
		return nil
	}
	if w := t.handoff; w != nil {
		return m.switchAgent(t, w.to, r.Handoff)
	}
	t.handoffReady = &handoffNote{from: t.Agent, text: r.Handoff, at: time.Now()}
	if t == m.curAgent() && m.dialog == nil {
		return m.openSwitchAgent()
	}
	m.toast(m.agentName(t) + ": " + string(t.Agent) + " wrote a handoff; " + m.pk("a s") + " switches the agent")
	return nil
}

// switchAgent stops t's agent and starts kind k in its place, continuing
// k's conversation in the worktree when it had one: the one it had in
// this tab, or for an agent nerdr cannot give an id to, the newest one it
// has in the worktree. k is given summary, the handoff the old agent
// wrote, as its first message, or with none, told to read the old agent's
// conversation.
func (m *Model) switchAgent(t *tab, k agents.Kind, summary string) tea.Cmd {
	if !m.live(t) || k == t.Agent {
		return nil
	}
	if w := t.handoff; w != nil {
		if i := t.findMessage(w.asked); i >= 0 {
			t.dropMessage(i)
		}
		t.handoff = nil
	}
	t.handoffReady = nil
	if t.pane != nil {
		old := t.pane
		go old.Close()
		t.pane = nil
	}
	if t.Sessions == nil {
		t.Sessions = map[agents.Kind]string{}
	}
	// An empty id still records that this agent has run here: Codex resumes
	// its directory's last session without reporting an id to nerdr.
	t.Sessions[t.Agent] = t.SessionID
	_, visited := t.Sessions[k]
	from, fromID, wasSandboxed := t.Agent, t.SessionID, t.sandboxed || m.sandboxedKind(t.Agent)
	t.Agent, t.SessionID = k, t.Sessions[k]
	// A model, and a resume command reported, are one kind's.
	t.Model, t.ResumeArgv = "", nil
	t.fallbackTried = false
	t.status = stStarting
	t.err = ""
	// What the old agent was sent is no concern of the new one's.
	t.awaitWork, t.queuePaused = false, false
	m.scrolling = false
	m.setFocus(focusAgent)
	m.saveSession()
	m.resizePanes()
	m.toast(m.agentName(t) + ": switched to " + string(k))
	specs, id, dir, tabID := m.specsFor(t), t.SessionID, t.Dir, t.ID
	skip, shared := m.othersSessions(t), m.sharesKind(t)
	worktree := dir
	if len(t.Worktrees) > 0 {
		worktree = t.Worktrees[0].Path
	}
	return func() tea.Msg {
		in := handoff.Info{From: from, To: k, Summary: summary}
		path, n, err := handoff.Write(specs, from, fromID, dir, worktree, tabID)
		if err != nil {
			in.TranscriptErr = err.Error()
		}
		in.Transcript, in.TranscriptLen = path, n
		if k == agents.Codex && (!visited || shared && id == "") {
			// Its --last could be the other codex's: only one known by
			// its id is carried on.
			return launchMsg{t: t, kind: k, l: agents.NewSessionIn(specs, k, dir), handoff: handoff.Prompt(in), sandboxed: wasSandboxed}
		}
		l := agents.Resume(specs, k, id, dir, skip)
		if l.New || l.SessionID == "" && k != agents.Codex {
			// Nothing of k's to continue here; its own "continue the
			// latest" could pick up another worktree's conversation, or
			// a reviewer's in this one.
			return launchMsg{t: t, kind: k, l: agents.NewSessionIn(specs, k, dir), handoff: handoff.Prompt(in), sandboxed: wasSandboxed}
		}
		in.Resumed = true
		return launchMsg{t: t, kind: k, l: l, resume: true, handoff: handoff.Prompt(in), sandboxed: wasSandboxed}
	}
}
