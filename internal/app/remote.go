package app

import (
	"reflect"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/reviewsource"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// The TUI as a client in a process of its own: its core is a copy of the
// core at the other end of a connection, kept by core.CoreConn; what it
// asks of core (m.do) goes there, and it waits for it to be done, as it
// would for core in its own process; and it says what it shows, which
// core goes by (core.ClientSays).

// NewRemote builds the model for the core at the other end of conn, for
// the session rooted at root, laid out as sess was. tell gives the program
// what comes of the connection (see core.Connect). It takes in core's
// state before it returns.
func NewRemote(cfg config.Config, root string, router *input.Router, sess state.Session, conn *attach.Conn, tell func(any)) (*Model, error) {
	m := New(cfg, root, router, state.Session{})
	m.remote = core.Connect(conn, &m.Core, tell)
	m.screens = m.remote.Screens
	// The repositories are where core is, which may be another machine.
	remote := m.remote
	m.reviewAsk = reviewsource.NewAsker(func(job string, q reviewsource.Query) bool {
		return remote.Send(core.ReadForReview{Job: job, Q: q})
	})
	m.review = m.review.SetSource(m.reviewAsk)
	m.Mu.Lock()
	defer m.Mu.Unlock()
	evs, err := m.remote.Run(core.ClientSays{State: m.clientState()})
	if err != nil {
		return nil, err
	}
	// Laid out as the session was left, as core saved it with what the
	// last client showed.
	m.restoreLayout(sess)
	m.pending = evs
	return m, nil
}

// Remote reports whether the TUI is a client of a core in another
// process.
func (m *Model) Remote() bool { return m.remote != nil }

// doRemote has the core at the other end of the connection do r, and takes
// in what came of it: the TUI then sees core as it would have r done in its
// own process.
func (m *Model) doRemote(r core.Request) tea.Cmd {
	if m.coreAway {
		m.toast("nerdr's core is gone: connecting again…")
		return nil
	}
	m.sayState()
	evs, err := m.remote.Run(r)
	cmd := m.told(evs)
	if err != nil {
		m.toast(err.Error())
	}
	return cmd
}

// tellCore has core do r, waiting for nothing: what the TUI need not see
// done before it goes on.
func (m *Model) tellCore(r core.Request) {
	if m.remote == nil {
		m.do(r)
		return
	}
	m.remote.Send(r)
}

// told takes what core told the TUI, as it would have been told it in
// core's own process; what the loop hands it is put to Update.
func (m *Model) told(evs []core.Event) tea.Cmd {
	var cmds []tea.Cmd
	for _, ev := range evs {
		switch ev.(type) {
		case core.InfoTickMsg, core.WirePanic, core.ChangedMsg, core.RedrawMsg, core.FrameMsg:
			cmds = append(cmds, func() tea.Msg { return ev })
		default:
			cmds = append(cmds, fromCore(m.Tell(ev)))
		}
	}
	return tea.Batch(cmds...)
}

// clientState is what the TUI shows, as core asks it; see core.Client.
func (m *Model) clientState() core.ClientState {
	s := core.ClientState{Front: m.Front(), DialogClosed: m.DialogClosed(), Layout: m.Layout(), Sight: m.Sight()}
	// As a new tab's agent would be shown, in a tab made for the asking.
	fresh := &core.Tab{}
	s.Fresh.W, s.Fresh.H = m.paneSize(fresh)
	delete(m.tabLooks, fresh)
	for _, p := range fresh.PaneList {
		delete(m.paneLooks, p)
	}
	for _, t := range m.Tabs {
		tr := m.Ref(t)
		if m.TabShown(tr) {
			s.Shown = append(s.Shown, tr)
		}
		for _, p := range t.Panes() {
			switch {
			case p.Kind == core.PaneShell:
				pr := m.PaneRef(p)
				w, h := m.ShellSize(tr, pr)
				s.Sizes = append(s.Sizes, core.PaneSize{T: tr, P: pr, W: w, H: h})
			case p.A != nil:
				ar := m.Ref(p.A)
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

// sayState tells core what the TUI shows, when that has changed since it
// last did.
func (m *Model) sayState() {
	if m.coreAway {
		return
	}
	s := m.clientState()
	if reflect.DeepEqual(s, m.said) {
		return
	}
	m.said = s
	m.remote.Send(core.ClientSays{State: s})
}

// wakeLocal has the TUI's own programs, the editor's, draw: in core's
// process the loop does it for all of them (see core.Loop).
func (m *Model) wakeLocal(send func(tea.Msg), stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-m.Wake:
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
	cmds := []tea.Cmd{tea.SetWindowTitle("nerdr"), m.review.Init(), m.told(m.pending)}
	m.pending = nil
	switch {
	case m.Cfg.Install == "":
		var then func(m *Model) tea.Cmd
		if len(m.Tabs) == 0 {
			then = (*Model).openWizard
		}
		cmds = append(cmds, m.openSetup(then))
	case len(m.Tabs) == 0:
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

// coreGone is the connection to core closed, why being what core said:
// core having crashed or been ended, its holder starts it again, the
// agents living on, and the TUI connects to it again when it can. Without
// a way to (SetRedial) it ends; and when another nerdr took over, as the
// two would take it from each other without end, or the holder is gone,
// when no core comes back.
func (m *Model) coreGone(why string) tea.Cmd {
	m.remote.Close("")
	// What the review panel asked will not be answered.
	m.reviewAsk.FailAll("the connection to nerdr's core has closed")
	if m.closing {
		return nil
	}
	if why == attach.ByeTakenOver || why == attach.ByeHolderGone {
		m.goneErr = why
		return tea.Quit
	}
	if m.redial == nil {
		m.goneErr = "the connection to nerdr's core has closed"
		return tea.Quit
	}
	m.coreAway = true
	m.toast("nerdr's core is gone: connecting again…")
	redial := m.redial
	return func() tea.Msg {
		wait := 100 * time.Millisecond
		deadline := time.Now().Add(coreGoneWait)
		for {
			conn, err := redial()
			if err == nil {
				return coreBackMsg{conn}
			}
			core.Debugf("core: connecting again: %v", err)
			if time.Now().After(deadline) {
				return coreEndedMsg{err}
			}
			time.Sleep(wait)
			wait = min(2*wait, 2*time.Second)
		}
	}
}

// coreGoneWait is how long the TUI tries to connect to its core again
// before it ends: a core that crashed is started again by its holder at
// once, but one whose holder has ended too never comes back.
var coreGoneWait = 20 * time.Second

// coreEndedMsg is core not back within coreGoneWait, err the last try.
type coreEndedMsg struct{ err error }

// coreBackMsg is a connection to core again, once it was gone.
type coreBackMsg struct{ conn *attach.Conn }

// coreBack goes on over conn: core sends all of its state afresh, into the
// same copy, and is told what the TUI shows.
func (m *Model) coreBack(conn *attach.Conn) tea.Cmd {
	m.remote.Reconnect(conn)
	m.screens = m.remote.Screens
	m.drawnPanes = nil
	m.coreAway = false
	m.said, m.keysTo = core.ClientState{}, keysTarget{}
	cmd := m.do(core.ClientSays{State: m.clientState()})
	// A core started again took up the programs at sizes it could not
	// know: they get the TUI's again.
	m.resizePanes()
	m.toast("connected to nerdr's core again")
	return cmd
}

// SetRedial gives the TUI a way to connect to its core again, should the
// connection close.
func (m *Model) SetRedial(f func() (*attach.Conn, error)) { m.redial = f }

// GoneErr is why the TUI ended with its core gone, "" when it did not.
func (m *Model) GoneErr() string { return m.goneErr }

// Close closes the connection to core, when the TUI is a client of one in
// another process: core works on.
func (m *Model) Close() {
	if m.remote != nil {
		m.remote.Close("")
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
	return box("Close or quit nerdr?", []string{
		"Close: the agents work on; nerdr in " + m.Root,
		"shows them again as they are.",
		"Quit: the agents are stopped. Their tabs and conversations",
		"are restored the next time you start nerdr there.",
		"",
		m.button("c", "close") + "   " + m.button("q", "quit") + "   " + m.button("esc", "cancel"),
	}, w)
}

// closeClient ends the TUI, the agents working on in core.
func (m *Model) closeClient() tea.Cmd {
	m.closing = true
	return tea.Quit
}

// quitAll has core stop every program and end, then ends the TUI.
func (m *Model) quitAll() tea.Cmd {
	m.toast("stopping agents…")
	m.closing = true
	// Waited for: core has it before the connection closes.
	return tea.Batch(m.do(core.QuitAll{}), tea.Quit)
}
