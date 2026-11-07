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

	done chan struct{}
}

type pending struct {
	key  string
	body []byte
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
	if p := c.keyed[key]; p != nil {
		c.bytes += len(body) - len(p.body)
		p.body = body
		c.mu.Unlock()
		return true
	}
	if len(c.queue) > 0 && c.bytes+len(body) > QueueLimit {
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
	p := &pending{key: key, body: body}
	c.queue = append(c.queue, p)
	if key != "" {
		c.keyed[key] = p
	}
	c.bytes += len(body)
	c.mu.Unlock()
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
		_ = c.c.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := writeFrame(c.c, frameMsg, p.body); err != nil {
			c.shut()
			return
		}
	}
}
