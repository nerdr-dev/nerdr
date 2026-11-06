package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// EnvHolder turns on running nerdr's programs in a holder, a process of
// their own that keeps them when nerdr's core crashes: NERDR_HOLDER=1.
// Without it nothing changes.
const EnvHolder = "NERDR_HOLDER"

// Held is the holder the programs of the tabs run in, when nerdr runs
// over one, and the programs it had when this core started: a core
// started after a crash takes those up again rather than starting them
// anew. See TakeUp.
type Held struct {
	Client *holder.Client
	// Progs are the programs the holder had at start that are not taken up
	// yet, by id, and Checkpoints the screen it kept of each.
	Progs       map[string]holder.Prog
	Checkpoints map[string]Checkpoint
}

// Checkpoint is a screen's block as the holder kept it, and the position
// in the program's output it is for; Pos is -1 for none.
type Checkpoint struct {
	Pos   int64
	Block []byte
}

// NewHeld is the core's side of the holder at the other end of cl, with
// the programs it has now and their checkpoints: all fetched here, before
// the core runs, so taking a program up asks the holder nothing.
func NewHeld(cl *holder.Client) (*Held, error) {
	progs, err := cl.List()
	if err != nil {
		return nil, err
	}
	h := &Held{Client: cl, Progs: map[string]holder.Prog{}, Checkpoints: map[string]Checkpoint{}}
	for _, p := range progs {
		if p.State == holder.Reaped {
			continue
		}
		h.Progs[p.ID] = p
		if p.Checkpoint < 0 {
			continue
		}
		pos, block, err := cl.FetchCheckpoint(p.ID)
		if err != nil {
			// A holder from before fetch: the screen is shown from what
			// is left of the output, marked incomplete.
			Debugf("checkpoint of %s: %v", p.ID, err)
			continue
		}
		h.Checkpoints[p.ID] = Checkpoint{pos, block}
	}
	return h, nil
}

// Kinds of held program, as a Label has them.
const (
	HeldAgent    = "agent"
	HeldShell    = "shell"
	HeldPluginUI = "plugin-ui"
	HeldRun      = "plugin-run"
)

// Label is what a program's mark in the holder says of it, beyond its
// sandbox and secret: what a core started later needs to take it up
// again. Fields are only ever added.
type Label struct {
	Kind string `json:"kind"`
	// Tab is the id of the tab it is of: for an agent, its own; for a
	// reviewer, the agent whose work it reviews.
	Tab string `json:"tab"`
	// Agent is a reviewer's id, which is made from Tab.
	Agent string `json:"agent,omitempty"`
	// N numbers a terminal among its tab's.
	N int `json:"n,omitempty"`
	// Plugin is a plugin's folder, Key the content it was started from,
	// and Events the file its screen is told of runs in.
	Plugin string `json:"plugin,omitempty"`
	Key    string `json:"key,omitempty"`
	Events string `json:"events,omitempty"`
	// Run is a plugin run's id, From the agent that ran it, Values what
	// it was run with, and Watch and Confined as PluginRun has them.
	Run      string         `json:"run,omitempty"`
	From     string         `json:"from,omitempty"`
	Values   map[string]any `json:"values,omitempty"`
	Watch    bool           `json:"watch,omitempty"`
	Confined bool           `json:"confined,omitempty"`
}

// AgentHeldLabel is the label of agent t's program: a reviewer's names the
// agent whose work it reviews.
func AgentHeldLabel(t *Tab) Label {
	if t.ReviewOf != nil {
		return Label{Kind: HeldAgent, Tab: t.ReviewOf.ID, Agent: t.ID}
	}
	return Label{Kind: HeldAgent, Tab: t.ID}
}

// ParseLabel reads a held program's label; ok is false for one that is
// not nerdr's.
func ParseLabel(s string) (Label, bool) {
	var l Label
	if err := json.Unmarshal([]byte(s), &l); err != nil || l.Kind == "" {
		return Label{}, false
	}
	return l, true
}

// Hold gives s the id and mark the holder is to keep it by, when the
// core runs over one: a new id, sandboxed as said, the secret its reports
// are known by, and l.
func (m *Core) Hold(s *spawn.Spec, l Label, sandboxed bool, secret string) {
	if m.Held == nil {
		return
	}
	b, _ := json.Marshal(l)
	var id [8]byte
	_, _ = rand.Read(id[:])
	s.ID = l.Kind + "/" + hex.EncodeToString(id[:])
	s.Mark = &spawn.Mark{Sandboxed: sandboxed, Secret: secret, Label: string(b)}
}

// ShutdownHolder has the holder stop what it still runs and end, once
// the user has quit: without it, the holder would start the core again.
func (m *Core) ShutdownHolder() { m.Held.Shutdown() }

// Shutdown has the holder stop what it still runs and end; nothing for
// no holder.
func (h *Held) Shutdown() {
	if h == nil {
		return
	}
	if err := h.Client.Shutdown(); err != nil {
		Debugf("holder shutdown: %v", err)
	}
}

// heldPane is a pane whose program the holder keeps, by its id there.
type heldPane struct {
	id string
	p  *term.Pane
}

// heldPanes are the panes of programs the holder keeps. m.Mu is held.
func (m *Core) heldPanes() []heldPane {
	var out []heldPane
	add := func(p *term.Pane) {
		if p == nil {
			return
		}
		if id := p.HeldID(); id != "" {
			out = append(out, heldPane{id, p})
		}
	}
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			add(p.Term())
		}
	}
	// Not PluginUIPanes, which is for quitting: it stops the runs.
	for _, u := range m.PluginUIs {
		add(u.Pane)
	}
	return out
}

// CheckpointQuiet is how long a pane is to have drawn nothing before its
// screen is sent to the holder.
const CheckpointQuiet = time.Second

// Checkpoints sends the holder the screen of each pane it keeps, every
// second, once the pane has gone quiet and has drawn since the last: a
// core started after a crash puts the screen back from it. It returns
// when stop is closed. Nothing of it runs under m.Mu but finding the
// panes.
func (m *Core) Checkpoints(stop <-chan struct{}) {
	if m.Held == nil {
		return
	}
	sent := map[string]int64{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		m.Mu.Lock()
		panes := m.heldPanes()
		m.Mu.Unlock()
		live := map[string]bool{}
		for _, hp := range panes {
			live[hp.id] = true
			m.checkpoint(hp, sent)
		}
		for id := range sent {
			if !live[id] {
				delete(sent, id)
			}
		}
	}
}

// checkpoint sends hp's screen to the holder if it is quiet and has
// drawn since the one last sent, at sent[hp.id].
func (m *Core) checkpoint(hp heldPane, sent map[string]int64) {
	if exited, _, _ := hp.p.Exited(); exited || hp.p.Quiet() < CheckpointQuiet {
		return
	}
	if last, ok := sent[hp.id]; ok && last == hp.p.Position() {
		return
	}
	block, pos, err := hp.p.CheckpointAt()
	if err != nil {
		// Inside a sequence, or the emulator failed: the next quiet
		// moment will do.
		return
	}
	if len(block) > holder.MaxCheckpoint {
		Debugf("checkpoint of %s: %d bytes, more than the holder keeps", hp.id, len(block))
		return
	}
	if err := m.Held.Client.Checkpoint(hp.id, pos, block); err != nil {
		Debugf("checkpoint of %s: %v", hp.id, err)
		return
	}
	sent[hp.id] = pos
}
