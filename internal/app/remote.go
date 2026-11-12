package app

import (
	"reflect"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/reviewsource"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// The TUI as a client in a process of its own: each machine's core is a
// copy of the core at the other end of a connection, kept by
// core.CoreConn; what it asks of core (m.do) goes there, and it waits for
// it to be done, as it would for core in its own process; and it says
// what it shows, which core goes by (core.ClientSays).

// NewRemote builds the model for the core at the other end of conn, for
// the session rooted at root, laid out as core says the last client left
// it: the session is core's, where core runs, and not read here. tell
// gives the program what comes of the connection (see core.Connect), as
// fleet.Told by the machine. It takes in core's state before it returns.
func NewRemote(cfg config.Config, root string, router *input.Router, conn *attach.Conn, tell func(any)) (*Model, error) {
	m := New(cfg, root, router, state.Session{})
	m.tellFn = tell
	mc := m.home()
	mc.Conn = core.Connect(conn, &mc.Core, mc.Tell(tell))
	mc.Screens, mc.Version = mc.Conn.Screens, conn.Version()
	// The repositories are where core is, which may be another machine:
	// that of the tab the panel shows (reviewOn).
	m.reviewOn.Store(mc)
	m.reviewAsk = reviewsource.NewAsker(func(job string, q reviewsource.Query) bool {
		on := m.reviewOn.Load()
		return on.Conn != nil && on.Conn.Send(core.ReadForReview{Job: job, Q: q})
	})
	m.review = m.review.SetSource(m.reviewAsk)
	m.fleet.Lock()
	defer m.fleet.Unlock()
	// Asked before the client says what it shows, which core keeps as
	// the layout from then on.
	saved, err := mc.Conn.Run(core.SavedLayout{})
	if err != nil {
		return nil, err
	}
	var layout core.Layout
	saved = slices.DeleteFunc(saved, func(ev core.Event) bool {
		l, ok := ev.(core.SavedLayoutMsg)
		if ok {
			layout = l.Layout
		}
		return ok
	})
	evs, err := mc.Conn.Run(core.ClientSays{State: m.clientState(mc)})
	if err != nil {
		return nil, err
	}
	m.restoreLayout(layout)
	mc.Pending = append(saved, evs...)
	return m, nil
}

// addMachine has the TUI show the tabs of one more machine's core, at the
// other end of conn, after those it shows: its copy is named name, and
// tell gives the program what comes of the connection, as for NewRemote.
// It is called with the fleet not locked, and returns what came of the
// core telling the TUI as it connected, for the program to run. Before F4
// no user has a way to it: tests show several machines so.
func (m *Model) addMachine(name, host string, conn *attach.Conn, redial func() (*attach.Conn, error), tell func(any)) (tea.Cmd, error) {
	if m.tellFn == nil {
		m.tellFn = tell
	}
	mc := newMachine(name, host, *m.cfg())
	mc.Redial = redial
	m.fleet.Lock()
	defer m.fleet.Unlock()
	return m.joinMachine(mc, conn)
}

// newMachine is a machine named name, on host, of a core in another
// process not yet connected to; cfg has the TUI's own settings.
func newMachine(name, host string, cfg config.Config) *fleet.Machine {
	mc := &fleet.Machine{Name: name, Host: host,
		Core: core.Core{ID: name, Cfg: cfg, Wake: make(chan struct{}, 1), Copy: true}}
	return mc
}

// joinMachine has mc be the fleet's, its tabs shown after the others',
// and goes on over conn to its core: nil leaves it grey, for its Gone. A
// core that does not take the TUI in leaves it grey too, saying why. The
// fleet is locked, and mc then with it.
func (m *Model) joinMachine(mc *fleet.Machine, conn *attach.Conn) (tea.Cmd, error) {
	mc.Core.Client = m
	mc.Core.Mu.Lock()
	m.fleet.Add(mc) // unlocked with the fleet from now on
	m.fleet.Bump()
	if conn == nil {
		return nil, nil
	}
	mc.Conn = core.Connect(conn, &mc.Core, mc.Tell(m.tellFn))
	mc.Screens, mc.Version = mc.Conn.Screens, conn.Version()
	// Its copy has no tabs yet, to say it shows: they come as it is done.
	evs, err := mc.Conn.Run(core.ClientSays{State: m.clientState(mc)})
	if err != nil {
		mc.Conn.Close("")
		mc.Gone = err.Error()
		return nil, err
	}
	return m.told(mc, evs), nil
}

// SetCoreHost says the core NewRemote connected to runs on host, a machine
// reached over ssh: what opens files or the worktree of its tabs is then
// made to reach them there. Each machine has its own (see addMachine).
func (m *Model) SetCoreHost(host string) { m.home().Host = host }

// Remote reports whether the TUI is a client of a core in another
// process.
func (m *Model) Remote() bool { return m.home().Conn != nil }

// doRemote has the core of mc, at the other end of the connection, do r,
// and takes in what came of it: the TUI then sees core as it would have r
// done in its own process.
func (m *Model) doRemote(mc *fleet.Machine, r core.Request) tea.Cmd {
	if mc.Away || mc.Gone != "" {
		m.toast(m.goneToast(mc))
		return nil
	}
	m.sayState(mc)
	evs, err := mc.Conn.Run(r)
	cmd := m.told(mc, evs)
	if err != nil {
		m.toast(err.Error())
	}
	return cmd
}

// tellCore has core do r, waiting for nothing: what the TUI need not see
// done before it goes on. What r leaves to do away from Mu, core's loop
// runs; what comes of it comes to the TUI as core tells it. It goes to the
// machine of the tab r names (fleet.For).
func (m *Model) tellCore(r core.Request) { m.tellOn(m.forOf(r), r) }

// tellOn has the core of mc do r, as tellCore.
func (m *Model) tellOn(mc *fleet.Machine, r core.Request) {
	if mc.Conn == nil && mc.Core.Copy {
		return // a server never connected to
	}
	if mc.Conn != nil {
		mc.Conn.Send(r)
		return
	}
	m.runCore(r.Do(&mc.Core))
}

// runCore runs c, of a request the TUI made in core's own process, on
// core's loop, away from Mu. Only a test's model has no loop: the test
// runs it, by runCoreNow.
func (m *Model) runCore(c core.Cmd) {
	switch {
	case c == nil:
	case m.Loop != nil:
		m.Loop.Run(c)
	case runCoreNow != nil:
		runCoreNow(m, c)
	}
}

// runCoreNow is how a test runs what a model with no loop leaves to core's.
var runCoreNow func(m *Model, c core.Cmd)

// told takes what the core of mc told the TUI, as it would have been told
// it in core's own process; what the loop hands it is put to Update, as
// the machine's.
func (m *Model) told(mc *fleet.Machine, evs []core.Event) tea.Cmd {
	var cmds []tea.Cmd
	for _, ev := range evs {
		if passedOn(ev) {
			cmds = append(cmds, func() tea.Msg { return fleet.Told{Machine: mc.Name, Msg: ev} })
		} else {
			was := m.telling
			m.telling = mc
			cmds = append(cmds, fromCore(m.Tell(ev)))
			m.telling = was
		}
	}
	return tea.Batch(cmds...)
}

// passedOn reports whether told passes ev on to Update as a message of its
// own, rather than having the TUI take it there and then.
func passedOn(ev core.Event) bool {
	switch ev.(type) {
	case core.InfoTickMsg, core.WirePanic, core.ChangedMsg, core.RedrawMsg, core.FrameMsg:
		return true
	}
	return false
}

// leavesPanel reports whether ev, of a core in another process, leaves the
// review panel as it was drawn: what is passed on, drawn by its own rules,
// and the start and end of core's look at the agents, which come three
// times a second.
func leavesPanel(ev core.Event) bool {
	switch ev.(type) {
	case core.DetectStartMsg, core.DetectDoneMsg:
		return true
	}
	return passedOn(ev)
}

// clientState is what the TUI shows of mc's tabs, as its core asks it; see
// core.Client.
func (m *Model) clientState(mc *fleet.Machine) core.ClientState {
	// Of what the TUI shows, the core of mc is told of its own tabs.
	own := func(r core.TabRef) core.TabRef {
		if r.Core != mc.Name {
			return core.TabRef{}
		}
		return r
	}
	sight := m.Sight()
	sight.Keyboard, sight.Beside = own(sight.Keyboard), own(sight.Beside)
	s := core.ClientState{Front: own(m.Front()), DialogClosed: m.DialogClosed(), Layout: m.layoutOn(mc), Sight: sight}
	// As a new tab's agent would be shown, in a tab made for the asking.
	fresh := &core.Tab{}
	s.Fresh.W, s.Fresh.H = m.paneSize(fresh)
	delete(m.tabLooks, fresh)
	for _, p := range fresh.PaneList {
		delete(m.paneLooks, p)
	}
	for _, t := range mc.Core.Tabs {
		tr := m.fleet.Ref(t)
		if m.TabShown(tr) {
			s.Shown = append(s.Shown, tr)
		}
		for _, p := range t.Panes() {
			switch {
			case p.Kind == core.PaneShell:
				pr := m.fleet.PaneRef(p)
				w, h := m.ShellSize(tr, pr)
				s.Sizes = append(s.Sizes, core.PaneSize{T: tr, P: pr, W: w, H: h})
			case p.A != nil:
				ar := m.fleet.Ref(p.A)
				if m.AgentVisible(ar) {
					s.Visible = append(s.Visible, ar)
				}
				w, h := m.TabSize(ar)
				s.Sizes = append(s.Sizes, core.PaneSize{A: ar, W: w, H: h})
			}
		}
	}
	return s
}

// sayState tells the core of mc what the TUI shows, when that has changed
// since it last did.
func (m *Model) sayState(mc *fleet.Machine) {
	if mc.Away || mc.Gone != "" {
		return
	}
	s := m.clientState(mc)
	if reflect.DeepEqual(s, mc.Said) {
		return
	}
	mc.Said = s
	mc.Conn.Send(core.ClientSays{State: s})
}

// sayStates has the TUI say what it shows to each machine's core in
// another process; see sayState.
func (m *Model) sayStates() {
	for _, mc := range m.fleet.Machines() {
		if mc.Conn != nil {
			m.sayState(mc)
		}
	}
}

// wakeLocal has the TUI's own programs, the editor's, draw: in core's
// process the loop does it for all of them (see core.Loop).
func (m *Model) wakeLocal(send func(tea.Msg), stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-m.home().Core.Wake:
		}
		time.Sleep(16 * time.Millisecond) // a program drawing many times is one frame
		send(core.FrameMsg{})
	}
}

// StartLocal starts what the TUI runs itself as a client in a process of
// its own, sending to p, until stop is closed.
func (m *Model) StartLocal(send func(tea.Msg), stop <-chan struct{}) {
	go m.wakeLocal(send, stop)
}

// initRemote is Init as a client of a core in another process: core has
// started the agents and keeps them; the TUI takes in what core told it
// while it connected, and asks for a first tab when there is none.
func (m *Model) initRemote() tea.Cmd {
	title := "nerdr"
	mc := m.home()
	if mc.Host != "" {
		title += " — " + mc.Host
	}
	cmds := []tea.Cmd{tea.SetWindowTitle(title), m.review.Init(), m.told(mc, mc.Pending)}
	mc.Pending = nil
	switch {
	case m.home().Core.Cfg.Install == "":
		var then func(m *Model) tea.Cmd
		if len(m.fleet.Tabs()) == 0 {
			then = (*Model).openWizard
		}
		cmds = append(cmds, m.openSetup(then))
	case len(m.fleet.Tabs()) == 0:
		cmds = append(cmds, m.openWizard())
	default:
		cmds = append(cmds, m.pinReview())
		if t := m.Cur(); t != nil && t.Spinoff != nil && m.dialog == nil {
			cmds = append(cmds, m.openSpinoff(t))
		}
	}
	m.syncInput()
	return tea.Batch(cmds...)
}

// coreGone is the connection to the core of mc closed, why being what core
// said:
// core having crashed or been ended, its holder starts it again, the
// agents living on, and the TUI connects to it again when it can. Without
// a way to (SetRedial) it is gone for good; and so when another nerdr took
// over, as the two would take it from each other without end, or the
// holder is gone, when no core comes back. A machine gone for good is
// grey while another is left (goneFor); the TUI ends when none is.
func (m *Model) coreGone(mc *fleet.Machine, why string) tea.Cmd {
	mc.Conn.Close("")
	if m.closing || mc.Gone != "" {
		return nil
	}

	if why == attach.ByeTakenOver || why == attach.ByeHolderGone {
		return m.goneFor(mc, why, why)
	}
	if mc.Redial == nil {
		return m.goneFor(mc, "the connection to nerdr's core has closed", "the connection to nerdr's core has closed")
	}
	// What the review panel asked will not be answered.
	m.reviewAsk.FailAll("the connection to nerdr's core has closed")
	mc.Away, mc.Err = true, ""
	m.fleet.Bump()
	m.syncInput()
	m.toast(m.goneToast(mc))
	return m.redialCore(mc)
}

// redialCore tries to connect to core again, for coreGoneWait. A core on
// another machine is tried again after that, for as long as the TUI runs:
// a network that is down is no reason to end, and the agents work on.
func (m *Model) redialCore(mc *fleet.Machine) tea.Cmd {
	redial, name := mc.Redial, mc.Name
	try := mc.Tries.Add(1)
	return func() tea.Msg {
		wait := 100 * time.Millisecond
		deadline := time.Now().Add(coreGoneWait)
		for {
			if mc.Tries.Load() != try {
				return nil // given up: disconnected, removed, or tried anew
			}
			conn, err := redial()
			if err == nil {
				if mc.Tries.Load() != try {
					conn.Close("")
					return nil
				}
				return fleet.Told{Machine: name, Msg: coreBackMsg{conn, try}}
			}
			core.Debugf("core: connecting again: %v", err)
			if time.Now().After(deadline) {
				return fleet.Told{Machine: name, Msg: coreEndedMsg{err, try}}
			}
			time.Sleep(wait)
			wait = min(2*wait, 2*time.Second)
		}
	}
}

// coreGoneWait is how long the TUI tries to connect to a core again
// before it gives it up: a core that crashed is started again by its
// holder at once, but one whose holder has ended too never comes back.
var coreGoneWait = 20 * time.Second

// coreEndedMsg is core not back within coreGoneWait, err the last try.
type coreEndedMsg struct {
	err error
	try int64 // see fleet.Machine.Tries
}

// onCoreEnded gives the core of mc up when it is not back: grey while
// another machine is left, else the TUI ends. When it is on another
// machine, it says why and tries again.
func (m *Model) onCoreEnded(mc *fleet.Machine, msg coreEndedMsg) tea.Cmd {
	if !mc.Away || msg.try != mc.Tries.Load() {
		return nil // given up meanwhile
	}
	if mc.Host == "" {
		return m.goneFor(mc, "nerdr's core has ended and did not come back ("+msg.err.Error()+")",
			"nerdr's core has ended and did not come back ("+msg.err.Error()+"): start nerdr again")
	}
	mc.Err = msg.err.Error()
	m.fleet.Bump()
	m.toast(mc.Host + ": " + msg.err.Error() + " — trying again")
	return m.redialCore(mc)
}

// coreBackMsg is a connection to core again, once it was gone.
type coreBackMsg struct {
	conn *attach.Conn
	try  int64 // see fleet.Machine.Tries
}

// coreBack goes on over conn to the core of mc: core sends all of its
// state afresh, into the same copy, and is told what the TUI shows.
func (m *Model) coreBack(mc *fleet.Machine, conn *attach.Conn, try int64) tea.Cmd {
	if !mc.Away || try != mc.Tries.Load() {
		// Given up meanwhile: the user connects again, when they will.
		conn.Close("")
		return nil
	}
	if mc.Conn == nil {
		// A server not connected to before.
		mc.Conn = core.Connect(conn, &mc.Core, mc.Tell(m.tellFn))
	} else {
		mc.Conn.Reconnect(conn)
	}
	// A core started again may be of another version: one updated.
	mc.Screens, mc.Version = mc.Conn.Screens, conn.Version()
	m.drawnPanes = nil
	mc.Away, mc.Err = false, ""
	m.syncInput()
	mc.Said, m.keysTo = core.ClientState{}, keysTarget{}
	cmd := m.doOn(mc, core.ClientSays{State: m.clientState(mc)})
	// A core started again took up the programs at sizes it could not
	// know: they get the TUI's again.
	m.resizePanes()
	if len(m.fleet.Machines()) == 1 {
		m.toast("connected to nerdr's core again")
	} else {
		m.toast("connected to " + machineLabel(mc) + " again")
	}
	return cmd
}

// SetRedial gives the TUI a way to connect again to the core NewRemote
// connected to, should the connection close. Each machine has its own
// (see addMachine).
func (m *Model) SetRedial(f func() (*attach.Conn, error)) { m.home().Redial = f }

// GoneErr is why the TUI ended with its core gone, "" when it did not.
func (m *Model) GoneErr() string { return m.goneErr }

// Close closes the connections to the machines' cores in other
// processes: they work on.
func (m *Model) Close() {
	for _, mc := range m.fleet.Machines() {
		if mc.Conn != nil {
			mc.Conn.Close("")
		}
	}
}

// closeOrQuit asks, as a client of a core in another process, whether to
// close the client, the agents working on, or to quit, stopping them.
type closeOrQuit struct{}

func (closeOrQuit) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "c", "C", "enter":
		m.dialog = nil
		return m.closeClient()
	case "q", "Q":
		m.dialog = nil
		return m.quitAll()
	case "esc", "n", "N", "ctrl+c":
		m.dialog = nil
	}
	return nil
}

func (closeOrQuit) view(m *Model, w, _ int) string {
	lines := []string{
		"Close: the agents work on; nerdr in " + m.home().Core.Root,
		"shows them again as they are.",
		"Quit: the agents are stopped. Their tabs and conversations",
		"are restored the next time you start nerdr there.",
	}
	if live := m.fleet.Live(); len(m.fleet.Machines()) > 1 {
		var names []string
		for _, mc := range live {
			names = append(names, machineLabel(mc))
		}
		lines = []string{
			"Close: the agents work on, and nerdr shows them",
			"again as they are.",
			"Quit: the agents are stopped, on " + strings.Join(names, ", ") + ".",
			"Their tabs and conversations are restored the next",
			"time you start nerdr there.",
		}
	}
	lines = append(lines, "", m.button("c", "close")+"   "+m.button("q", "quit")+"   "+m.button("esc", "cancel"))
	return box("Close or quit nerdr?", lines, w)
}

// closeClient ends the TUI, the agents working on in core.
func (m *Model) closeClient() tea.Cmd {
	m.closing = true
	return tea.Quit
}

// quitAll has the core of every machine still there stop every program
// and end, then ends the TUI.
func (m *Model) quitAll() tea.Cmd {
	m.toast("stopping agents…")
	m.closing = true
	// Waited for: core has it before the connection closes.
	cmds := []tea.Cmd{}
	for _, mc := range m.fleet.Live() {
		if !mc.Away {
			cmds = append(cmds, m.doOn(mc, core.QuitAll{}))
		}
	}
	return tea.Batch(append(cmds, tea.Quit)...)
}
