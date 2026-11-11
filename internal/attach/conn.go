package attach

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// QueueLimit is how much a Conn holds of what waits to be written. A
// client that falls this far behind gets none of it, and the core sends it
// everything afresh instead (see Conn.OnBehind): what it sends grows no
// queue however slow the client, and the core never waits for one.
const QueueLimit = 8 << 20

// Timeouts: for the handshake, for one frame to be taken by the other end
// before it counts as gone, and for a bye. Variables for the tests.
var (
	handshakeTimeout = 5 * time.Second
	writeTimeout     = 30 * time.Second
	byeTimeout       = 2 * time.Second
)

// Conn is a connection between a client and a core, once in. Send never
// blocks: a goroutine of its own writes what waits.
type Conn struct {
	c net.Conn

	mu     sync.Mutex
	cond   *sync.Cond
	queue  []*pending
	keyed  map[string]*pending
	bytes  int
	closed bool
	bye    *Bye // to write once the queue is dropped, then close
	behind func()
	// why is what the other end said as it closed, "" if it said nothing.
	why string
	// version is the version of nerdr the other end said it is as the
	// two said hello; see Version.
	version string

	done chan struct{}
}

type pending struct {
	key  string
	body []byte
	// build makes the body just before it is written, when set; see
	// SendFunc.
	build func() []byte
}

func newConn(c net.Conn) *Conn {
	cn := &Conn{c: c, keyed: map[string]*pending{}, done: make(chan struct{})}
	cn.cond = sync.NewCond(&cn.mu)
	go cn.write()
	return cn
}

// Send queues body as a message. One sent with a key takes the place of
// one with the same key still waiting, where it waited: the newest of a
// screen or a tab is all a client needs. It reports false once the
// connection is closed.
func (c *Conn) Send(key string, body []byte) bool {
	c.mu.Lock()
	if c.closed || c.bye != nil {
		c.mu.Unlock()
		return false
	}
	p := c.keyed[key]
	// What would wait besides this message.
	others, rest := len(c.queue), c.bytes
	if p != nil {
		others, rest = others-1, rest-len(p.body)
	}
	if others > 0 && rest+len(body) > QueueLimit {
		// Too far behind: what waits is dropped, this with it, and
		// everything is sent afresh.
		c.queue, c.keyed, c.bytes = nil, map[string]*pending{}, 0
		behind := c.behind
		c.mu.Unlock()
		if behind != nil {
			behind()
		}
		return true
	}
	if p != nil {
		p.body, p.build, c.bytes = body, nil, rest+len(body)
		c.mu.Unlock()
		return true
	}
	p = &pending{key: key, body: body}
	c.queue = append(c.queue, p)
	if key != "" {
		c.keyed[key] = p
	}
	c.bytes += len(body)
	c.mu.Unlock()
	c.cond.Signal()
	return true
}

// SendFunc is Send of what build makes, when it comes to be written: what
// it makes is then as new as can be, and one that differs from the last
// one written need only say how. Called on the writer's goroutine, build
// must not wait on anything that waits for a Send; a nil body is not
// written. One waiting with the same key is replaced, by key as in Send.
func (c *Conn) SendFunc(key string, build func() []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.bye != nil {
		return false
	}
	if p := c.keyed[key]; p != nil {
		c.bytes -= len(p.body)
		p.body, p.build = nil, build
		return true
	}
	p := &pending{key: key, build: build}
	c.queue = append(c.queue, p)
	if key != "" {
		c.keyed[key] = p
	}
	c.cond.Signal()
	return true
}

// SendJSON is Send of v as JSON.
func (c *Conn) SendJSON(key string, v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return c.Send(key, b)
}

// OnBehind sets what is called, from Send, when the client fell too far
// behind and what waited was dropped: it is to send everything afresh.
func (c *Conn) OnBehind(f func()) {
	c.mu.Lock()
	c.behind = f
	c.mu.Unlock()
}

// Recv is the next message from the other end. Once the other end says
// bye it is a *ByeError.
func (c *Conn) Recv() ([]byte, error) {
	typ, body, err := readFrame(c.c)
	if err != nil {
		c.shut()
		return nil, err
	}
	switch typ {
	case frameMsg:
		return body, nil
	case frameBye:
		var b Bye
		_ = json.Unmarshal(body, &b)
		c.mu.Lock()
		c.why = b.Reason
		c.mu.Unlock()
		c.shut()
		return nil, &ByeError{b}
	}
	c.shut()
	return nil, fmt.Errorf("unexpected frame %q", typ)
}

// Close says bye, for reason, and closes: what waits to be written is
// dropped. With no reason it closes at once.
func (c *Conn) Close(reason string) {
	if reason == "" {
		c.shut()
		return
	}
	c.mu.Lock()
	if !c.closed && c.bye == nil {
		c.bye = &Bye{Reason: reason}
		c.queue, c.keyed, c.bytes = nil, map[string]*pending{}, 0
	}
	c.mu.Unlock()
	c.cond.Signal()
}

// Why is the reason the other end gave as it closed the connection: ""
// while it is open, or when it closed without one, as a crash does.
func (c *Conn) Why() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.why
}

// Version is the version of nerdr the other end said it is as the two
// said hello: the core's, to a client (Welcome), the client's, to a core
// (Hello). It is what the other end says, "" when it said none.
func (c *Conn) Version() string { return c.version }

// Done is closed once the connection is.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) shut() {
	c.mu.Lock()
	was := c.closed
	c.closed = true
	c.mu.Unlock()
	if was {
		return
	}
	_ = c.c.Close()
	c.cond.Broadcast()
	close(c.done)
}

func (c *Conn) write() {
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && c.bye == nil && !c.closed {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		if bye := c.bye; bye != nil {
			c.mu.Unlock()
			_ = c.c.SetWriteDeadline(time.Now().Add(byeTimeout))
			_ = writeJSON(c.c, frameBye, bye)
			c.shut()
			return
		}
		p := c.queue[0]
		c.queue[0] = nil
		c.queue = c.queue[1:]
		if p.key != "" {
			delete(c.keyed, p.key)
		}
		c.bytes -= len(p.body)
		c.mu.Unlock()
		body := p.body
		if p.build != nil {
			if body = p.build(); body == nil {
				continue
			}
		}
		_ = c.c.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := writeFrame(c.c, frameMsg, body); err != nil {
			c.shut()
			return
		}
	}
}
