package core

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// ChangedMsg tells a client that core has changed, and to draw again.
type ChangedMsg struct{}

// RedrawMsg is ChangedMsg after core's look at the agents, which leaves what
// a client takes long to draw as it was; see the TUI's reviewReuse.
type RedrawMsg struct{}

// PanicMsg is a panic on core's goroutines, for a client to raise on its own,
// where it can put the terminal back as it was.
type PanicMsg struct {
	Value any
	Stack []byte
}

func (p PanicMsg) String() string { return fmt.Sprintf("%v\n%s", p.Value, p.Stack) }

// frameEv, detectEv and infoEv are the loop's own: a program has drawn, it is
// time to look at the agents, and at what goes on in the background.
type (
	frameEv  struct{}
	detectEv struct{}
	infoEv   struct{}
)

// Loop runs core on a goroutine of its own, so that it goes on whether or not
// a client is there and however slow one is. All of core's state is guarded by
// Core.Mu: the loop holds it for each event, and a client must while it reads
// or changes anything of core's.
type Loop struct {
	c *Core
	// forward takes what is not core's to handle. It may block, and is called
	// with Mu not held.
	forward func(Event)

	in   chan Event
	stop chan struct{}
	once sync.Once

	outMu  sync.Mutex
	out    []Event
	draw   map[Event]bool // the draw-only events waiting, each once; see send
	outNew chan struct{}
}

// NewLoop is a loop for c, handing on what is not core's to forward. A nil
// forward is no client: such events are dropped.
func NewLoop(c *Core, forward func(Event)) *Loop {
	if forward == nil {
		forward = func(Event) {}
	}
	return &Loop{
		c:       c,
		forward: forward,
		in:      make(chan Event, 256),
		stop:    make(chan struct{}),
		outNew:  make(chan struct{}, 1),
		draw:    map[Event]bool{},
	}
}

// Start runs the loop until Stop.
func (l *Loop) Start() {
	l.StartSteps()
	l.every(DetectEvery, detectEv{})
	l.every(InfoTickN, infoEv{})
}

// StartSteps runs the loop until Stop, with no looks at the agents or at
// what goes on in the background of its own: the events posted to it, and
// a frame when a program has drawn.
func (l *Loop) StartSteps() {
	l.guarded(l.run)
	l.guarded(l.deliver)
	l.guarded(func() {
		for {
			select {
			case <-l.stop:
				return
			case <-l.c.Wake:
			}
			// As WaitFrame: a program drawing many times is one frame.
			time.Sleep(16 * time.Millisecond)
			l.Post(frameEv{})
		}
	})
}

// every posts ev every d until Stop.
func (l *Loop) every(d time.Duration, ev Event) {
	l.guarded(func() {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-t.C:
				l.Post(ev)
			}
		}
	})
}

// Stop ends the loop. Events posted after it are dropped.
func (l *Loop) Stop() { l.once.Do(func() { close(l.stop) }) }

// Post gives the loop an event. It waits while the loop is too far behind, and
// never once the loop has stopped.
func (l *Loop) Post(ev Event) {
	select {
	case l.in <- ev:
	case <-l.stop:
	}
}

// guarded runs f on a goroutine, a panic going to the client.
func (l *Loop) guarded(f func()) {
	go func() {
		defer func() {
			if v := recover(); v != nil {
				l.send(PanicMsg{Value: v, Stack: debug.Stack()})
			}
		}()
		f()
	}()
}

func (l *Loop) run() {
	for {
		select {
		case <-l.stop:
			return
		case ev := <-l.in:
			l.step(ev)
		}
	}
}

// step handles one event, and starts the work it leads to with Mu let go.
func (l *Loop) step(ev Event) {
	cmd, tell := l.handle(ev)
	if tell != nil {
		l.send(tell)
	}
	l.runCmd(cmd)
}

// handle is loopEvent with Mu held, and let go even if it panics: the client
// takes Mu to put the terminal back, and to stop the agents.
func (l *Loop) handle(ev Event) (Cmd, Event) {
	l.c.Mu.Lock()
	defer l.c.Mu.Unlock()
	cmd, tell := l.c.loopEvent(ev)
	l.c.FlushClient()
	return cmd, tell
}

// Run runs cmd away from the loop; what comes of it comes back to Post.
func (l *Loop) Run(cmd Cmd) { l.runCmd(cmd) }

// runCmd runs cmd away from the loop. What comes of it comes back to Post.
func (l *Loop) runCmd(cmd Cmd) {
	if cmd == nil {
		return
	}
	l.guarded(func() {
		ev := cmd()
		switch ev := ev.(type) {
		case nil:
		case BatchEvent:
			for _, c := range ev {
				l.runCmd(c)
			}
		default:
			l.Post(ev)
		}
	})
}

// send queues ev for the client: however slow it is, core does not wait for
// it. What says only to draw again waits at most once of each kind, so a busy
// agent cannot pile up frames ahead of the user's keys.
func (l *Loop) send(ev Event) {
	l.outMu.Lock()
	if drawOnly(ev) {
		l.draw[ev] = true
	} else {
		l.out = append(l.out, ev)
	}
	l.outMu.Unlock()
	select {
	case l.outNew <- struct{}{}:
	default:
	}
}

func drawOnly(ev Event) bool {
	switch ev.(type) {
	case ChangedMsg, RedrawMsg, FrameMsg:
		return true
	}
	return false
}

func (l *Loop) deliver() {
	for {
		select {
		case <-l.stop:
			return
		case <-l.outNew:
		}
		for {
			ev, ok := l.next()
			if !ok {
				break
			}
			l.forward(ev)
		}
	}
}

// next is the next event for the client: those queued first, in order, then
// the draw-only ones, a change first: drawing it draws all.
func (l *Loop) next() (Event, bool) {
	l.outMu.Lock()
	defer l.outMu.Unlock()
	if len(l.out) > 0 {
		ev := l.out[0]
		l.out = l.out[1:]
		return ev, true
	}
	if l.draw[ChangedMsg{}] {
		delete(l.draw, ChangedMsg{})
		delete(l.draw, RedrawMsg{})
		return ChangedMsg{}, true
	}
	for _, ev := range []Event{RedrawMsg{}, FrameMsg{}} {
		if l.draw[ev] {
			delete(l.draw, ev)
			return ev, true
		}
	}
	return nil, false
}

// loopEvent is one event on the loop: the work it leads to, and what to tell
// the client. Mu is held.
func (m *Core) loopEvent(ev Event) (Cmd, Event) {
	switch ev.(type) {
	case frameEv:
		var cmd Cmd
		if !m.Quitting {
			m.CheckShells()
			m.CheckPluginUIs()
			cmd = m.ExpireAsks(time.Now())
		}
		if m.Screens != nil {
			m.Screens.Tick(m, false)
		}
		return cmd, FrameMsg{}
	case detectEv:
		if m.Screens != nil {
			m.Screens.Tick(m, true)
		}
		if m.Quitting {
			return nil, nil
		}
		return m.DetectAll(time.Now()), RedrawMsg{}
	case infoEv:
		if m.Quitting {
			return nil, nil
		}
		now := time.Now()
		// Also here: with no client, nothing may draw for a frame.
		return Batch(m.InfoTick(now), m.ExpireAsks(now)), InfoTickMsg{}
	}
	if cmd, ok := m.Handle(ev); ok {
		m.Gen++
		return cmd, ChangedMsg{}
	}
	return nil, ev
}

// DetectAll looks at every agent. The client says which tabs are shown, and
// does its part at the start and the end; with none, nothing is shown.
func (m *Core) DetectAll(now time.Time) Cmd {
	cmds := []Cmd{m.Tell(DetectStartMsg{})}
	for _, t := range m.Tabs {
		shown := m.Client != nil && m.Client.TabShown(m.Ref(t))
		if shown {
			t.ShownAt = now
		}
		cmds = append(cmds, m.DetectAgent(t, shown, now))
		for _, a := range t.ExtraAgents() {
			cmds = append(cmds, m.DetectAgent(a, shown, now))
		}
		m.ReportWhenBegun(t, now)
	}
	cmds = append(cmds, m.Tell(DetectDoneMsg{Now: now}))
	if !m.HasClient() {
		// A client does it in its turn at the end, with its user's last
		// input: see DetectDoneMsg.
		m.KeepAwake(now, nil)
	}
	return Batch(cmds...)
}

// Handle is what core does with ev, when ev is core's to handle. It runs
// wherever the event arrives, with Mu held.
func (m *Core) Handle(ev Event) (Cmd, bool) {
	if m.Quitting {
		return m.handleQuitting(ev)
	}
	switch ev := ev.(type) {
	case RequestEv:
		return ev.Run(m), true
	case Reply:
		return m.Tell(ev.Ev), true
	case watchStarted:
		return m.onWatchStarted(ev), true
	case watchFired:
		return m.onWatchFired(ev), true
	case agentsFoundMsg:
		m.onAgentsFound(ev)
		return nil, true
	case PRAllMsg:
		return m.OnPRAll(ev), true
	case repoPRsMsg:
		return m.onRepoPRs(ev), true
	case setupProgress:
		return m.onSetupProgress(ev), true
	case repoMadeMsg:
		return m.onRepoMade(ev), true
	case RepoScanMsg:
		return m.OnRepoScan(ev), true
	case GhMsg:
		m.NoGh = !ev.Has
		return nil, true
	case ReposChangedMsg:
		return m.OnReposChanged(ev), true
	case IssueAllMsg:
		m.OnIssueAll(ev)
		return nil, true
	case FocusFilesMsg:
		return m.OnFocusFiles(ev), true
	case SpinoffMsg:
		return m.SpinoffMade(ev), true
	case TabClosedMsg:
		if ev.Err != "" {
			m.Toast(ev.Err)
		} else if ev.Note != "" {
			m.Toast(ev.Note)
		}
		return nil, true
	case hooks.Report:
		return m.OnReport(ev), true
	case TabMadeMsg:
		return m.TabMade(ev), true
	case TreeMsg:
		return m.OnTree(ev), true
	case PRWatchMsg:
		m.OnPRWatch(ev)
		return nil, true
	case IssueWatchMsg:
		m.OnIssueWatch(ev)
		return nil, true
	case InfoMsg:
		m.SetInfo(ev)
		return m.RefreshInfo(), true
	case HeadsMsg:
		m.SetHeads(ev)
		return nil, true
	case SummaryMsg:
		m.OnSummary(ev.S)
		return nil, true
	case ClaimMsg:
		m.OnClaim(ev)
		return nil, true
	case GoalMsg:
		ev.T.GoalLooking = false
		m.SetGoal(ev.T, ev.Goal)
		m.SetLatest(ev.T, ev.Latest)
		return nil, true
	case SkillUseMsg:
		m.OnSkillUse(ev)
		return nil, true
	case RecapMsg:
		if !m.Live(ev.T) {
			return nil, true
		}
		return m.LookUpGoal(ev.T), true
	case ModelsMsg:
		return m.OnModels(ev), true
	case LaunchMsg:
		if !m.Live(ev.T) || ev.Kind != ev.T.Agent {
			return nil, true // closed or switched while its conversation was being looked up
		}
		if ev.Handoff != "" {
			// First, ahead of what was waiting for the agent before.
			ev.T.Messages = append([]state.Message{{Text: ev.Handoff, Auto: true, Sandboxed: ev.Sandboxed}}, ev.T.Messages...)
			m.SaveSession()
		}
		cmd := m.Spawn(ev.T, ev.L, ev.Resume)
		if ev.T.ReviewOf != nil && ev.T.Pane == nil && ev.T.Light == StExited {
			m.ReviewerNotStarted(ev.T)
		}
		return cmd, true
	case SpawnReady:
		return m.OnSpawnReady(ev), true
	case SkillsAppliedMsg:
		m.OnSkillsApplied(ev)
		return nil, true
	case SendEnter:
		m.SendEnter(ev)
		return nil, true
	}
	return nil, false
}

// handleQuitting is Handle once the agents are being stopped: requests get no
// answer but that, and only what is needed to resume the agents next time is
// kept.
func (m *Core) handleQuitting(ev Event) (Cmd, bool) {
	switch ev := ev.(type) {
	case hooks.Report:
		if ev.Reply != nil {
			Answerer(ev)(hooks.Answer{Err: "nerdr is quitting"})
			return nil, true
		}
		if m.SandboxRefusal(m.Vouch(ev)) != "" {
			return nil, true
		}
		switch ev.Method {
		case hooks.ReportAgent, hooks.ReportAgentSession:
			m.HookSession(ev)
		}
		return nil, true
	case ClaimMsg:
		m.OnClaim(ev)
		return nil, true
	case RequestEv:
		if ev.Done != nil {
			ev.Done()
		}
		return nil, true // nothing changes now but what quitting does
	}
	return nil, false
}
