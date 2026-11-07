package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// CoreConn is a client's connection to a core in another process: it keeps
// the copy of core's state (a Mirror into C), the copies of the screens
// (Screens), and sends core requests. What core sends waits until the
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
}

// InboxMsg tells the client core has sent what is to be taken in (Take).
type InboxMsg struct{}

// CoreGoneMsg tells the client the connection to core has closed.
type CoreGoneMsg struct{}

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
	cc.Screens = newScreenClient(conn, cc.tell, func(b []byte) { cc.got(gen, b) })
}

// got takes a message of core's into the inbox; nil is the connection
// closed. One from a connection before the last is let go.
func (cc *CoreConn) got(gen int, b []byte) {
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
		cc.tell(CoreGoneMsg{})
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
	b, err := EncodeRequest(r)
	if err != nil {
		return nil, err
	}
	cc.mu.Lock()
	cc.lastID++
	id := cc.lastID
	cc.mu.Unlock()
	if !cc.conn.Send("", wire(wireAsked, askedWire{ID: id, Req: b})) {
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
// and what the client shows.
func (cc *CoreConn) Send(r Request) {
	b, err := EncodeRequest(r)
	if err != nil {
		Debugf("core: %v", err)
		return
	}
	cc.conn.Send("", append([]byte{wireRequest}, b...))
}

// takeUntil takes in the inbox, and reports whether the request of id is
// done; up to then, when it is.
func (cc *CoreConn) takeUntil(id int64) ([]Event, bool) {
	var evs []Event
	for {
		cc.mu.Lock()
		if len(cc.inbox) == 0 {
			cc.mu.Unlock()
			return evs, false
		}
		b := cc.inbox[0]
		cc.inbox[0] = nil
		cc.inbox = cc.inbox[1:]
		cc.mu.Unlock()
		switch b[0] {
		case wireParts:
			var parts []Part
			if err := json.Unmarshal(b[1:], &parts); err != nil {
				Debugf("core: parts: %v", err)
				continue
			}
			for _, p := range parts {
				if err := cc.mirror.Apply(p); err != nil {
					Debugf("core: part %s: %v", p.Key, err)
				}
			}
		case wireEvent:
			ev, err := DecodeEvent(b[1:])
			if err != nil {
				Debugf("core: %v", err)
				continue
			}
			evs = append(evs, ev)
		case wireDone:
			var d doneWire
			if json.Unmarshal(b[1:], &d) == nil && id != 0 && d.ID == id {
				return evs, true
			}
		case wireBehind:
			// What core told meanwhile is lost, a done among it maybe:
			// what is waited for counts as done, and all comes afresh.
			if id != 0 {
				return evs, true
			}
		}
	}
}

// Started reports whether a program came, since it was last asked, that
// the copy did not know: one the client is to size. C.Mu is held.
func (cc *CoreConn) Started() bool {
	s := cc.mirror.started
	cc.mirror.started = false
	return s
}

// Close closes the connection.
func (cc *CoreConn) Close(reason string) { cc.conn.Close(reason) }
