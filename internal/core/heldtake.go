package core

import (
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// take takes from the programs not yet taken up the first, oldest first,
// whose label wanted says is it, so it is not taken again.
func (h *Held) take(wanted func(Label) bool) (holder.Prog, Label, bool) {
	if h == nil {
		return holder.Prog{}, Label{}, false
	}
	ids := make([]string, 0, len(h.Progs))
	for id := range h.Progs {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		return int(h.Progs[a].Started - h.Progs[b].Started)
	})
	for _, id := range ids {
		p := h.Progs[id]
		l, ok := ParseLabel(p.Label)
		if ok && wanted(l) {
			delete(h.Progs, id)
			return p, l, true
		}
	}
	return holder.Prog{}, Label{}, false
}

// taken is where program p's output is read from again, with the screen
// the holder kept of it: from its checkpoint, or from the oldest output
// the holder still has. The screen is incomplete when output it needs is
// gone: from before what the holder keeps, with no checkpoint, or between
// the checkpoint and it.
func (h *Held) taken(p holder.Prog) term.Taken {
	at := term.Taken{Pos: p.Start, Incomplete: p.Start > 0, Started: time.Unix(0, p.Started)}
	if ck, ok := h.Checkpoints[p.ID]; ok && ck.Pos >= 0 {
		at.Block = ck.Block
		at.Pos = max(ck.Pos, p.Start)
		at.Incomplete = ck.Pos < p.Start
	}
	delete(h.Checkpoints, p.ID)
	return at
}

// attachHeld is a pane of held program p, w×h, as it was.
func (m *Core) attachHeld(p holder.Prog, w, h int) *term.Pane {
	at := m.Held.taken(p)
	return term.Attach(m.Held.Client.Attach(p, at.Pos), w, h, at, m.Notify)
}

// takeUpAgent gives agent t its program in the holder, when one runs
// there from before the core was started again, and reports whether it
// did: the agent goes on as it was, with the secret its reports are known
// by, and as sandboxed as the holder says it was started.
func (m *Core) takeUpAgent(t *Tab) bool {
	p, _, ok := m.Held.take(func(l Label) bool {
		return l.Kind == HeldAgent && (l.Agent == t.ID || l.Agent == "" && l.Tab == t.ID)
	})
	if !ok {
		return false
	}
	w, h := m.Client.TabSize(m.Ref(t))
	t.Pane = m.attachHeld(p, w, h)
	t.HookID = p.Secret
	t.InSandbox = p.Sandboxed
	if t.InSandbox {
		t.Sandboxed = true
		t.Owner().Sandboxed = true
		t.ResumeArgv = nil
		m.MarkSandboxed(t)
	}
	t.Gen = lastGen.Add(1)
	t.StartedAt, t.Said, t.SessionUnsure = time.Unix(0, p.Started), nil, false
	t.Hook = HookAuthority{}
	t.Reports, t.Heard, t.ToldCutOff = false, false, false
	t.DetectAs = agents.DetectID(m.Cfg.Agents, t.Agent)
	t.Resumed = true
	t.Light = StStarting
	t.Raw = detect.Unknown
	t.Worked, t.Tasked = false, false
	t.Err = ""
	t.Changed = time.Now()
	Debugf("took up %s from the holder: pid %d", m.AgentName(t), p.Pid)
	m.Tell(StartedMsg{A: m.Ref(t)})
	return true
}

// TakeUpHeld gives the tabs back the terminals, plugin screens, plugin
// runs and reviews they had in the holder before the core was started
// again. Agents
// are taken up as they are started; see takeUpAgent. m.Mu is held.
func (m *Core) TakeUpHeld() {
	if m.Held == nil {
		return
	}
	for _, kind := range []string{HeldShell, HeldPluginUI, HeldRun} {
		for {
			p, l, ok := m.Held.take(func(l Label) bool { return l.Kind == kind && m.TabByID(l.Tab) != nil })
			if !ok {
				break
			}
			t := m.TabByID(l.Tab)
			switch kind {
			case HeldShell:
				m.takeUpShell(t, p, l)
			case HeldPluginUI:
				m.takeUpPluginUI(t, p, l)
			case HeldRun:
				m.takeUpRun(t, p, l)
			}
		}
	}
	m.takeUpReviews()
	// A check from inside a sandbox that the core before did not see the
	// end of is ended: nothing waits for its answer now.
	for {
		p, _, ok := m.Held.take(func(l Label) bool { return l.Kind == HeldCheck })
		if !ok {
			break
		}
		proc := m.Held.Client.Attach(p, p.Pos)
		go func() {
			_ = proc.Signal(proc.Pid(), syscall.SIGKILL)
			_ = proc.Close()
		}()
	}
}

func (m *Core) takeUpShell(t *Tab, p holder.Prog, l Label) {
	pt := &PaneTab{Kind: PaneShell, N: l.N, Opened: time.Now()}
	m.AddPane(t, pt, true)
	w, h := m.Client.ShellSize(m.Ref(t), m.PaneRef(pt))
	pt.Shell = m.attachHeld(p, w, h)
	m.Tell(TermsMsg{T: m.Ref(t)})
}

func (m *Core) takeUpPluginUI(t *Tab, p holder.Prog, l Label) {
	w, h := max(m.PluginUIW, 20), max(m.PluginUIH, 5)
	u := &PluginUI{T: t, Pane: m.attachHeld(p, w, h), Dir: l.Plugin, Key: l.Key}
	if l.Events != "" {
		// Its program reads the file by name: it is written on.
		if f, err := os.OpenFile(l.Events, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
			u.Events = f
		}
	}
	if m.PluginUIs == nil {
		m.PluginUIs = map[string]*PluginUI{}
	}
	m.PluginUIs[t.ID+"\x00"+l.Plugin] = u
}

func (m *Core) takeUpRun(t *Tab, p holder.Prog, l Label) {
	at := m.Held.taken(p)
	proc := WatchProc(m.Held.Client.Attach(p, at.Pos), int(at.Pos), m.Notify)
	from := m.AgentByID(l.From)
	if from == nil {
		from = t
	}
	started := time.Unix(0, p.Started)
	// What it printed before is read again, for F5 and reads, but was told
	// of by the core before: only what comes after it goes to the agent
	// watching and to the plugin's screen. What came in the moment
	// between the last the core told of and its crash is not told again.
	run := &PluginRun{Id: l.Run, T: t, Agent: m.AgentLabel(t, from), Dir: l.Plugin, Values: l.Values,
		Started: started, Proc: proc, From: from, ReadAt: time.Now(), Watch: l.Watch, Confined: l.Confined,
		Pumped: int(p.Pos)}
	v := &m.PluginRuns
	if v.Runs == nil {
		v.Runs, v.ByID = map[string]*PluginRun{}, map[string]*PluginRun{}
	}
	v.Runs[t.ID+"\x00"+l.Plugin], v.ByID[run.Id] = run, run
}

// HeldLeft is how many programs the holder runs that no tab took up: one
// of a tab since closed, or a reviewer of a review the session did not
// keep. They go on unseen until nerdr quits; nothing stops them before.
func (m *Core) HeldLeft() int {
	if m.Held == nil {
		return 0
	}
	n := 0
	for _, p := range m.Held.Progs {
		if l, ok := ParseLabel(p.Label); ok && l.Kind == HeldAgent && l.Agent == "" && m.AgentByID(l.Tab) != nil {
			continue // the agent takes it up when it starts: a tab's own, or one beside it
		}
		n++
	}
	return n
}

// HeldAgent reports whether an agent of tab t, its own or one beside it,
// has a program in the holder waiting to be taken up: the tab is not to
// sleep, which would leave it running unseen.
func (m *Core) HeldAgent(t *Tab) bool {
	if m.Held == nil {
		return false
	}
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if m.Held.has(func(l Label) bool { return l.Kind == HeldAgent && l.Agent == "" && l.Tab == a.ID }) {
			return true
		}
	}
	return false
}

var _ spawn.Proc = (*holder.Proc)(nil)

// TabByID is the tab called id, or nil.
func (m *Core) TabByID(id string) *Tab {
	for _, t := range m.Tabs {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// AgentByID is the agent called id, of any tab, or nil.
func (m *Core) AgentByID(id string) *Tab {
	for _, a := range m.AllAgents() {
		if a.ID == id {
			return a
		}
	}
	return nil
}
