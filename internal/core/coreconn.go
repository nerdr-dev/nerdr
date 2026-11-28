package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// CoreConn is a client's connection to a core in another process: it keeps
// the copy of core's state (a Mirror into C), the copies of the screens
// (Screens), and sends core requests. The copy's ID is the name the client
// gives the core, which keeps "" itself: what crosses the connection names
// the core's tabs and panes by the one on the client's side and by the
// other on core's. What core sends waits until the
// client takes it in (Take), or until a request it waits for is done (Run),
// both with C.Mu held: the copy changes only then.
type CoreConn struct {
	C       *Core
	Screens *ScreenClient
	conn    *attach.Conn
	mirror  *Mirror
	tell    func(any)

	mu     sync.Mutex
	inbox  [][]byte
	closed bool
	gen    int // of the connection, one more each Reconnect
	more   chan struct{}
	lastID int64

	// resync is the id of the ResendParts asked for when a patch could
	// not be taken, 0 when none is: until it is done, the copy is not
	// what core takes it to be, and what core told meanwhile (held), and
	// a request done meanwhile (doneSeen, the last of those), wait for it.
	// It was asked over the connection of resyncGen: a core started again
	// (Reconnect) sends every part whole, and none waits for it then.
	// Read and written by takeUntil alone.
	resync    int64
	resyncGen int
	held      []Event
	doneSeen  int64
}

// InboxMsg tells the client core has sent what is to be taken in (Take).
type InboxMsg struct{}

// CoreGoneMsg tells the client the connection to core has closed; Why is
// what core said as it closed it, "" when it said nothing, as when it
// crashed.
type CoreGoneMsg struct{ Why string }

// ErrCoreGone is what waiting for core comes to once the connection has
// closed.
var ErrCoreGone = errors.New("the connection to nerdr's core has closed")

// RunWait is how long Run waits for core to do a request.
var RunWait = 10 * time.Second

// Connect keeps a copy of the state of the core at the other end of conn
// in c, and of the screens it sends. tell gives the client ScreensMsg,
// InboxMsg and CoreGoneMsg, from a goroutine of the connection's: it must
// not wait for the client to take them.
func Connect(conn *attach.Conn, c *Core, tell func(any)) *CoreConn {
	c.Copy = true // see Core.Copy
	cc := &CoreConn{C: c, mirror: NewMirror(c), tell: tell, more: make(chan struct{}, 1)}
	cc.Reconnect(conn)
	return cc
}

// Reconnect has the client go on over conn, to a core started again: what
// it sends is taken into the same copy, whose tabs keep their pointers.
// The screens are copied afresh, in a new Screens. C.Mu is held, but for
// Connect.
func (cc *CoreConn) Reconnect(conn *attach.Conn) {
	cc.mu.Lock()
	cc.gen++
	gen := cc.gen
	cc.conn, cc.inbox, cc.closed = conn, nil, false
	cc.mu.Unlock()
	cc.Screens = newScreenClient(conn, cc.C.ID, cc.tell, func(b []byte) { cc.got(gen, conn, b) })
}

// got takes a message of core's over conn into the inbox; nil is the
// connection closed. One from a connection before the last is let go.
func (cc *CoreConn) got(gen int, conn *attach.Conn, b []byte) {
	cc.mu.Lock()
	if gen != cc.gen {
		cc.mu.Unlock()
		return
	}
	if b == nil {
		cc.closed = true
	} else {
		cc.inbox = append(cc.inbox, b)
	}
	first := len(cc.inbox) == 1 || b == nil
	cc.mu.Unlock()
	select {
	case cc.more <- struct{}{}:
	default:
	}
	if b == nil {
		cc.tell(CoreGoneMsg{Why: conn.Why()})
	} else if first {
		cc.tell(InboxMsg{})
	}
}

// Take takes in what core has sent: the copy is brought up to date, and
// what core told the client is returned, in order. C.Mu is held.
func (cc *CoreConn) Take() []Event {
	evs, _ := cc.takeUntil(0)
	return evs
}

// Run has core do r, and waits until it has, taking in what core sends
// meanwhile: the copy then shows what came of r, as a client in core's
// own process would see it. What core told the client is returned, in
// order. C.Mu is held.
func (cc *CoreConn) Run(r Request) ([]Event, error) {
	b, err := cc.encode(r)
	if err != nil {
		return nil, err
	}
	cc.mu.Lock()
	cc.lastID++
	id := cc.lastID
	conn := cc.conn // Reconnect replaces it
	cc.mu.Unlock()
	if conn == nil || !conn.Send("", wire(wireAsked, askedWire{ID: id, Req: b})) {
		return nil, ErrCoreGone
	}
	var evs []Event
	deadline := time.After(RunWait)
	for {
		more, done := cc.takeUntil(id)
		evs = append(evs, more...)
		if done {
			return evs, nil
		}
		cc.mu.Lock()
		closed := cc.closed && len(cc.inbox) == 0
		cc.mu.Unlock()
		if closed {
			return evs, ErrCoreGone
		}
		select {
		case <-cc.more:
		case <-deadline:
			return evs, fmt.Errorf("nerdr's core did not do %T in %v", r, RunWait)
		}
	}
}

// Send has core do r, waiting for nothing: what the user does in a screen,
// what the client shows, and what it asks from a goroutine of its own. It
// reports false when r could not go.
func (cc *CoreConn) Send(r Request) bool {
	b, err := cc.encode(r)
	if err != nil {
		Debugf("core: %v", err)
		return false
	}
	conn := cc.current()
	return conn != nil && conn.Send("", append([]byte{wireRequest}, b...))
}

// encode is r as it goes to core: the tabs and panes it names, the copy's
// by the copy's ID, are core's own, by "".
func (cc *CoreConn) encode(r Request) ([]byte, error) {
	r, err := renamedRequest(r, cc.C.ID, "")
	if err != nil {
		return nil, err
	}
	return EncodeRequest(r)
}

// takeUntil takes in the inbox, and reports whether the request of id is
// done; up to then, when it is.
func (cc *CoreConn) takeUntil(id int64) ([]Event, bool) {
	var evs []Event
	// done is what takeUntil comes to with request id done, or the
	// inbox empty: while a resync is on its way, nothing yet.
	done := func(ok bool) ([]Event, bool) {
		if cc.resync != 0 {
			cc.held = append(cc.held, evs...)
			if ok {
				cc.doneSeen = max(cc.doneSeen, id)
			}
			return nil, false
		}
		// What waited for a resync before a reconnect let it go, too.
		evs, cc.held = append(cc.held, evs...), nil
		return evs, ok
	}
	// resynced is the resync done, what waited for it taken in; it says
	// the last request done meanwhile.
	resynced := func() int64 {
		seen := cc.doneSeen
		cc.resync, cc.doneSeen = 0, 0
		evs, cc.held = append(cc.held, evs...), nil
		return seen
	}
	for {
		cc.mu.Lock()
		if cc.resync != 0 && cc.resyncGen != cc.gen {
			cc.resync, cc.doneSeen = 0, 0
		}
		if len(cc.inbox) == 0 {
			cc.mu.Unlock()
			return done(false)
		}
		b := cc.inbox[0]
		cc.inbox[0] = nil
		cc.inbox = cc.inbox[1:]
		if len(cc.inbox) == 0 {
			cc.inbox = nil // let go of the array, and what it held
		}
		cc.mu.Unlock()
		switch b[0] {
		case wireParts:
			var parts []Part
			if err := json.Unmarshal(b[1:], &parts); err != nil {
				Debugf("core: parts: %v", err)
				continue
			}
			resend := false
			for _, p := range parts {
				if err := cc.mirror.Apply(p); err != nil {
					Debugf("core: part %s: %v", p.Key, err)
					resend = resend || errors.Is(err, errPatch)
				}
			}
			if resend && cc.resync == 0 {
				cc.askResync()
			}
		case wireEvent:
			ev, err := DecodeEvent(b[1:])
			if err != nil {
				Debugf("core: %v", err)
				continue
			}
			if cc.C.ID != "" {
				v := reflect.New(reflect.TypeOf(ev)).Elem()
				v.Set(reflect.ValueOf(ev))
				renameRefs(v, "", cc.C.ID)
				ev = v.Interface()
			}
			evs = append(evs, ev)
		case wireDone:
			var d doneWire
			if json.Unmarshal(b[1:], &d) != nil {
				continue
			}
			if cc.resync != 0 && d.ID == cc.resync {
				if seen := resynced(); id != 0 && seen >= id {
					return evs, true
				}
			}
			if id != 0 && d.ID == id {
				return done(true)
			}
		case wireBehind:
			// What core told meanwhile is lost, a done among it maybe:
			// the parts before this came afresh, and it says which
			// requests core had done by then.
			var bw behindWire
			if json.Unmarshal(b[1:], &bw) != nil {
				continue
			}
			var seen int64
			if cc.resync != 0 {
				// The parts came whole: the resync is done, asked for
				// or not.
				seen = resynced()
			}
			if id != 0 && (bw.Done >= id || seen >= id) {
				return evs, true
			}
		}
	}
}

// askResync asks core for every part whole again, a patch of one not
// taken: what it tells from now on waits for them (see takeUntil).
func (cc *CoreConn) askResync() {
	b, err := cc.encode(ResendParts{})
	if err != nil {
		return
	}
	cc.mu.Lock()
	cc.lastID++
	id := cc.lastID
	conn, gen := cc.conn, cc.gen
	cc.mu.Unlock()
	if conn != nil && conn.Send("", wire(wireAsked, askedWire{ID: id, Req: b})) {
		cc.resync, cc.resyncGen = id, gen
	}
}

// Started reports whether a program came, since it was last asked, that
// the copy did not know: one the client is to size. C.Mu is held.
func (cc *CoreConn) Started() bool {
	s := cc.mirror.started
	cc.mirror.started = false
	return s
}

// current is the connection over which the client goes on now: Reconnect
// replaces it, from another goroutine than may send.
func (cc *CoreConn) current() *attach.Conn {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.conn
}

// Close closes the connection.
func (cc *CoreConn) Close(reason string) {
	if conn := cc.current(); conn != nil {
		conn.Close(reason)
	}
}
