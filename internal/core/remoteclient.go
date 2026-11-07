package core

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// A client in a process of its own is sent core's state as Parts, what core
// tells it as events, and the screens it shows, on one connection; it sends
// requests, and what it shows (ClientState). Each message is a byte for its
// kind, then JSON; the screens' kinds are in screens.go.
//
// After each event of the loop, what changed of core's state goes first,
// then what core told the client meanwhile, in order: the client reads an
// event against the state it was told in. A request the client waits for
// (wireAsked) is answered by wireDone after both.

const (
	wireParts  byte = 'P' // core: []Part
	wireEvent  byte = 'E' // core: an event, as EncodeEvent makes it
	wireDone   byte = 'D' // core: the request of an id is done, doneWire
	wireBehind byte = 'B' // core: what waited was dropped, all came afresh; behindWire
	wireAsked  byte = 'q' // client: a request to be told when done, askedWire
)

type askedWire struct {
	ID  int64           `json:"id"`
	Req json.RawMessage `json:"req"`
}

type doneWire struct {
	ID int64 `json:"id"`
}

// behindWire follows the parts sent afresh to a client that fell behind:
// Done is the last of its requests core has done, whose done may have
// been among what was dropped.
type behindWire struct {
	Done int64 `json:"done"`
}

// ClientState is what a client in a process of its own shows, as
// core.Client asks it: Front, the tabs on screen (Shown) and the agents
// being looked at (Visible), whether a dialog is open, the size of each
// pane's program, the layout and Sight.
type ClientState struct {
	Front        TabRef
	Shown        []TabRef
	Visible      []TabRef
	DialogClosed bool
	Sizes        []PaneSize
	// Fresh is the size of the program of a tab's agent the client has
	// not shown yet, as a new tab's: core asks it as it starts one.
	Fresh  PaneSize
	Layout Layout
	Sight  Sight
}

// PaneSize is the size a program gets: agent A's, or with P, the program
// in T's terminal pane P.
type PaneSize struct {
	A    TabRef  `json:",omitempty"`
	T    TabRef  `json:",omitempty"`
	P    PaneRef `json:",omitempty"`
	W, H int
}

func init() {
	registerRequests(ClientSays{}, clientGone{}, noRequest{}, StartWhenSized{}, StartWaiting{}, flushParts{})
}

// ClientSays is what the client shows now.
type ClientSays struct{ State ClientState }

func (q ClientSays) Do(m *Core) Cmd {
	if rc, ok := m.Client.(*RemoteClient); ok {
		rc.state = q.State
	}
	if q.State.sized() {
		return StartWaiting{}.Do(m)
	}
	return nil
}

// sized reports whether s says how large the client shows the agents.
func (s ClientState) sized() bool { return s.Fresh.W > 0 || len(s.Sizes) > 0 }

// StartWhenSized is StartRestored in a core of its own, which no client
// may have connected to yet: the agents start once one says how large it
// shows them, or at StartWaiting, for none to start at a size made up.
type StartWhenSized StartRestored

func (q StartWhenSized) Do(m *Core) Cmd {
	r := StartRestored(q)
	m.waitingStart = &r
	if rc, ok := m.Client.(*RemoteClient); ok && rc.state.sized() {
		return StartWaiting{}.Do(m) // a client said before this came
	}
	return nil
}

// StartWaiting starts the agents StartWhenSized has waiting, if they still
// are.
type StartWaiting struct{}

func (StartWaiting) Do(m *Core) Cmd {
	r := m.waitingStart
	if r == nil {
		return nil
	}
	m.waitingStart = nil
	return r.Do(m)
}

// RemoteClient is a client in a process of its own, as core sees it: what
// it shows, as it last said, and the connection to tell it on. Once the
// connection has closed it is still what core goes by of what the client
// showed, for the session, but tells nothing. Its fields are guarded by
// Core.Mu.
type RemoteClient struct {
	conn    *attach.Conn
	screens *ScreenServer
	state   ClientState
	sent    Sent
	// out is what core told it since the last flush, with the requests
	// done, to go after the parts.
	out [][]byte
	// gone is set once the connection has closed; behind when what waited
	// for it was dropped.
	gone   atomic.Bool
	behind atomic.Bool
	// notices are what core had to show with no client there, kept for
	// the next to connect.
	notices [][]byte
	// lastDone is the id of the last request asked with an id that core
	// has done.
	lastDone int64
	// partsErr is the last a part could not be sent for, logged once.
	partsErr string
	// post hands the loop a request; partsAt is when the parts were last
	// looked at, at Gen partsGen, and later is set while a flushParts is
	// on its way.
	post     func(RequestEv)
	partsAt  time.Time
	partsGen uint64
	later    bool
}

var _ Client = (*RemoteClient)(nil)

// AttachClient has the client on conn be core's client from now on: it is
// sent all of core's state, and what core tells it, from here; its requests
// go to post, to be done on core's loop. Mu is held.
func (m *Core) AttachClient(conn *attach.Conn, post func(RequestEv)) *RemoteClient {
	rc := &RemoteClient{conn: conn, post: post}
	if old, ok := m.Client.(*RemoteClient); ok {
		rc.state = old.state // what the last showed, until this one says
		old.gone.Store(true)
		rc.out, old.notices = old.notices, nil
	}
	parts := &clientParts{
		behind: func() { rc.behind.Store(true) },
		gone: func() {
			rc.gone.Store(true)
			post(RequestEv{R: clientGone{rc: rc}})
		},
	}
	parts.other = func(b []byte) { rc.read(b, post) }
	rc.screens = serveScreens(conn, post, parts)
	m.Client, m.Screens = rc, rc.screens
	rc.flush(m)
	return rc
}

// clientGone is the connection of rc closed: core sends no screens for it.
// It is core's own, never sent.
type clientGone struct{ rc *RemoteClient }

func (q clientGone) Do(m *Core) Cmd {
	if m.Client == q.rc {
		m.Screens = nil
	}
	return nil
}

// read takes a message the screens do not.
func (rc *RemoteClient) read(b []byte, post func(RequestEv)) {
	switch b[0] {
	case wireAsked:
		var a askedWire
		if err := json.Unmarshal(b[1:], &a); err != nil {
			Debugf("client: %v", err)
			return
		}
		done := wire(wireDone, doneWire{ID: a.ID})
		r, err := DecodeRequest(a.Req)
		if err != nil {
			Debugf("client: %v", err)
			r = noRequest{}
		}
		post(RequestEv{R: r, Done: func() {
			rc.out = append(rc.out, done)
			rc.lastDone = max(rc.lastDone, a.ID)
		}})
	}
}

// noRequest is a request that could not be read: it is done at once, so the
// client does not wait for it. Core's own, never sent.
type noRequest struct{}

func (noRequest) Do(*Core) Cmd { return nil }

// flush sends the client what changed of core's state since it was last
// sent, then what core told it meanwhile. Mu is held.
func (rc *RemoteClient) flush(m *Core) {
	if rc.gone.Load() {
		rc.out = nil
		return
	}
	behind := rc.behind.Swap(false)
	if behind {
		rc.sent.Reset()
	}
	// The parts are looked at again when core changed (Gen), when the
	// client is told something, or every PartsEvery: a program drawing
	// changes little else, and looking at every part each frame costs.
	now := time.Now()
	if !behind && m.Gen == rc.partsGen && len(rc.out) == 0 && now.Sub(rc.partsAt) < PartsEvery {
		if !rc.later && rc.post != nil {
			rc.later = true
			time.AfterFunc(PartsEvery-now.Sub(rc.partsAt), func() { rc.post(RequestEv{R: flushParts{rc: rc}}) })
		}
		return
	}
	rc.partsAt, rc.partsGen = now, m.Gen
	parts, err := m.Parts(&rc.sent)
	if why := fmt.Sprint(err); err != nil && why != rc.partsErr {
		Logf("client: parts left out: %s", why)
		rc.partsErr = why
	} else if err == nil {
		rc.partsErr = ""
	}
	if len(parts) > 0 {
		rc.conn.Send("", wire(wireParts, parts))
	}
	if behind {
		// After the parts: a request it says is done shows in them.
		rc.conn.Send("", wire(wireBehind, behindWire{Done: rc.lastDone}))
	}
	for _, b := range rc.out {
		rc.conn.Send("", b)
	}
	rc.out = nil
}

// PartsEvery is how long the parts of core's state may wait to be looked
// at again while only programs drew.
var PartsEvery = 100 * time.Millisecond

// flushParts is a client's parts due to be looked at again. Core's own,
// never sent.
type flushParts struct{ rc *RemoteClient }

// Done as a request, it moves Gen on, and the flush after looks.
func (q flushParts) Do(*Core) Cmd {
	q.rc.later = false
	return nil
}

// FlushClient sends a client in a process of its own what changed, after
// an event of the loop. Mu is held.
func (m *Core) FlushClient() {
	if rc, ok := m.Client.(*RemoteClient); ok {
		rc.flush(m)
	}
}

// There reports whether the client is still connected.
func (rc *RemoteClient) There() bool { return !rc.gone.Load() }

// Tell sends ev after the next parts; nothing comes back of it to do here.
// A notice told with no client there waits for the next.
func (rc *RemoteClient) Tell(ev Event) Cmd {
	_, notice := ev.(NoticeMsg)
	if rc.gone.Load() && !notice {
		return nil
	}
	b, err := EncodeEvent(ev)
	if err != nil {
		Debugf("client: %v", err)
		return nil
	}
	b = append([]byte{wireEvent}, b...)
	if rc.gone.Load() {
		rc.notices = append(rc.notices, b)
		return nil
	}
	rc.out = append(rc.out, b)
	return nil
}

// Forward sends ev, which the loop hands the client, as it comes. Called
// with Mu not held.
func (rc *RemoteClient) Forward(ev Event) {
	if rc.gone.Load() || !Sendable(ev) {
		return
	}
	if b, err := EncodeEvent(ev); err == nil {
		rc.conn.Send("", append([]byte{wireEvent}, b...))
	}
}

func (rc *RemoteClient) Front() TabRef { return rc.state.Front }
func (rc *RemoteClient) TabShown(t TabRef) bool {
	return !rc.gone.Load() && slices.Contains(rc.state.Shown, t)
}
func (rc *RemoteClient) AgentVisible(a TabRef) bool {
	return !rc.gone.Load() && slices.Contains(rc.state.Visible, a)
}
func (rc *RemoteClient) DialogClosed() bool { return !rc.gone.Load() && rc.state.DialogClosed }
func (rc *RemoteClient) TabSize(a TabRef) (int, int) {
	for _, s := range rc.state.Sizes {
		if s.A == a && s.P == (PaneRef{}) {
			return s.W, s.H
		}
	}
	return rc.state.Fresh.W, rc.state.Fresh.H
}
func (rc *RemoteClient) ShellSize(t TabRef, p PaneRef) (int, int) {
	for _, s := range rc.state.Sizes {
		if s.T == t && s.P == p {
			return s.W, s.H
		}
	}
	if rc.state.Fresh.W > 0 {
		return max(rc.state.Fresh.W-ShellMargin, 1), rc.state.Fresh.H
	}
	return 0, 0
}
func (rc *RemoteClient) Layout() Layout { return rc.state.Layout }
func (rc *RemoteClient) Sight() Sight   { return rc.state.Sight }
