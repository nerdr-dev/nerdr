// Package api is nerdr's API socket: how a program of the user's own, such
// as the connector of a phone app or a script, reads what goes on in a
// folder's nerdr and asks things of it, while nerdr runs as it does, with
// or without its TUI.
//
// It can do much of what the user can, answering what nerdr asks among
// it, so it is guarded as the client socket is (see internal/attach): it
// lives in the client sockets' folder, a client must send the key in a
// file beside it, made anew each time a core starts, and the core refuses
// a connection from a program it runs, an agent above all. Several
// programs may be connected at once.
//
// What goes either way is JSON, one object a line. A client says hello
// first, with the key; then it sends requests, each answered by a line
// with the request's id, and once it has subscribed, the core sends it
// events as they happen, lines with no id. See the docs (API) for each.
package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// MaxLine is the longest line either side may send.
const MaxLine = 4 << 20

// helloTimeout is how long a client has to say hello, and callTimeout how
// long a call waits for the core's loop.
const (
	helloTimeout = 5 * time.Second
	callTimeout  = 30 * time.Second
)

// pollEvery is how often the state is looked at for events while a client
// is subscribed.
var pollEvery = 500 * time.Millisecond

// Hello is what a client says first.
type Hello struct {
	Key string `json:"key"`
	// API is the version of the API the client was written for; 0 is
	// taken as 1. A core of an older one refuses it.
	API int `json:"api"`
}

// Welcome is the core's answer to a hello it takes.
type Welcome struct {
	API     int    `json:"api"`
	Version string `json:"version"`
	Root    string `json:"root"`
}

// Request is a line a client sends after hello: Op, what to do, and the
// parameters it takes. ID is any JSON value, sent back with the answer.
type Request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Op     string          `json:"op"`
	Agent  string          `json:"agent,omitempty"`
	Target string          `json:"target,omitempty"`
	Tab    string          `json:"tab,omitempty"`
	Text   string          `json:"text,omitempty"`
	Keys   []string        `json:"keys,omitempty"`
	Lines  int             `json:"lines,omitempty"`
	// Question and Yes are answer's.
	Question int  `json:"question,omitempty"`
	Yes      bool `json:"yes,omitempty"`
	// The new tab's.
	Repos  []string `json:"repos,omitempty"`
	Branch string   `json:"branch,omitempty"`
	Model  string   `json:"model,omitempty"`
	Task   string   `json:"task,omitempty"`
}

// Answer is the core's answer to a request: Result, or Error.
type Answer struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ReadResult is read's result: what the agent or terminal shows.
type ReadResult struct {
	Text string `json:"text"`
}

// NewTabResult is new_tab's result: the branch of the tab being made.
type NewTabResult struct {
	Branch string `json:"branch"`
}

// Config is what the API socket goes by.
type Config struct {
	// Version is nerdr's, Root the folder its core runs for.
	Version, Root string
	// Check is why a connection from peer p is refused, "" when it is
	// not; asked before anything is read. Required.
	Check func(p hooks.Peer) string
	// Post gives the core's loop a call to run; it may drop it once the
	// loop has stopped. Required.
	Post func(c core.Call)
}

// Server is a core's API socket.
type Server struct {
	cfg  Config
	ln   net.Listener
	sock string
	keyf string
	key  string
	done chan struct{}
	once sync.Once

	mu    sync.Mutex
	conns map[*conn]bool
	wg    sync.WaitGroup
	// subs are the connections subscribed to events, and last the state
	// the events were last told from.
	subs map[*conn]bool
	last *core.APIState
	poll chan struct{}
}

// Listen opens the API socket at sock, with a new key in keyFile. The
// core already has the folder's session, and the client socket, which is
// what keeps a second one from here: one left by a core that died is
// removed.
func Listen(sock, keyFile string, cfg Config) (*Server, error) {
	if cfg.Check == nil || cfg.Post == nil {
		return nil, errors.New("api: no check of who connects, or no loop")
	}
	if filepath.Dir(sock) != filepath.Dir(keyFile) {
		return nil, errors.New("api: socket and key must share a folder")
	}
	if err := attach.EnsureDir(filepath.Dir(sock)); err != nil {
		return nil, err
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	key, err := attach.WriteKey(keyFile)
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(sock)
		return nil, err
	}
	s := &Server{cfg: cfg, ln: ln, sock: sock, keyf: keyFile, key: key, done: make(chan struct{}),
		conns: map[*conn]bool{}, subs: map[*conn]bool{}, poll: make(chan struct{}, 1)}
	s.wg.Add(2)
	go s.accept()
	go s.watch()
	return s, nil
}

// Close stops listening, closes every connection, and removes the socket
// and the key.
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.ln.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.close()
		}
		s.mu.Unlock()
		s.wg.Wait()
		_ = os.Remove(s.sock)
		_ = os.Remove(s.keyf)
	})
}

// Observe is for core.Core.Observe: what the client is told that the
// API's subscribers are to hear too. Called with the core's Mu held; it
// does not wait.
func (s *Server) Observe(ev core.Event) {
	var out *Event
	switch ev := ev.(type) {
	case core.ToastMsg:
		out = &Event{Event: "notice", Text: string(ev)}
	case core.NoticeMsg:
		out = &Event{Event: "notice", Text: string(ev), Important: true}
	case core.AskMsg, core.AnsweredMsg, core.PanesMsg:
		// Looked at at once, rather than at the next poll.
		select {
		case s.poll <- struct{}{}:
		default:
		}
	}
	if out == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.subs {
		c.event(*out)
	}
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		c := newConn(s, nc)
		s.mu.Lock()
		select {
		case <-s.done:
			s.mu.Unlock()
			_ = nc.Close()
			return
		default:
		}
		s.conns[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			c.serve()
			s.mu.Lock()
			delete(s.conns, c)
			delete(s.subs, c)
			s.mu.Unlock()
		}()
	}
}

// call runs f on the core's loop and waits for what it gives.
func (s *Server) call(changes bool, f func(m *core.Core) (any, core.Cmd, error)) (any, error) {
	type result struct {
		v   any
		err error
	}
	got := make(chan result, 1)
	s.cfg.Post(core.Call{Changes: changes, F: func(m *core.Core) core.Cmd {
		v, cmd, err := f(m)
		got <- result{v, err}
		return cmd
	}})
	select {
	case r := <-got:
		return r.v, r.err
	case <-time.After(callTimeout):
		return nil, errors.New("nerdr's core did not answer in time")
	case <-s.done:
		return nil, core.ErrAPIQuitting
	}
}

// repoPlaces are where the core looks for repositories, from the loop: the
// looking, on the disk, is done here, away from it.
func (s *Server) repoPlaces() (core.RepoPlaces, error) {
	v, err := s.call(false, func(m *core.Core) (any, core.Cmd, error) { return m.RepoPlaces(), nil, nil })
	if err != nil {
		return core.RepoPlaces{}, err
	}
	return v.(core.RepoPlaces), nil
}

// state is the state now, from the loop.
func (s *Server) state() (core.APIState, error) {
	v, err := s.call(false, func(m *core.Core) (any, core.Cmd, error) { return m.APIState(), nil, nil })
	if err != nil {
		return core.APIState{}, err
	}
	return v.(core.APIState), nil
}

// subscribe has c told the events from now on.
func (s *Server) subscribe(c *conn) error {
	st, err := s.state()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subs) == 0 || s.last == nil {
		s.last = &st
	}
	s.subs[c] = true
	return nil
}

// watch looks at the state while a client is subscribed, and tells them
// what changed.
func (s *Server) watch() {
	defer s.wg.Done()
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		case <-s.poll:
		}
		s.mu.Lock()
		n := len(s.subs)
		s.mu.Unlock()
		if n == 0 {
			continue
		}
		st, err := s.state()
		if err != nil {
			continue
		}
		s.mu.Lock()
		var evs []Event
		if s.last != nil {
			evs = Diff(*s.last, st)
		}
		s.last = &st
		for c := range s.subs {
			for _, ev := range evs {
				c.event(ev)
			}
		}
		s.mu.Unlock()
	}
}

// conn is one client's connection.
type conn struct {
	s  *Server
	nc net.Conn

	wmu sync.Mutex // one line at a time
	// events wait here for the writer, which drops them while the client
	// is too slow, and then says so once (lost).
	emu    sync.Mutex
	events []Event
	lost   bool
	wake   chan struct{}
	closed chan struct{}
	once   sync.Once
}

// maxQueued is how many events wait for a slow client before the rest
// are dropped.
const maxQueued = 1024

func newConn(s *Server, nc net.Conn) *conn {
	return &conn{s: s, nc: nc, wake: make(chan struct{}, 1), closed: make(chan struct{})}
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.nc.Close()
	})
}

func (c *conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.nc.SetWriteDeadline(time.Now().Add(callTimeout))
	_, err = c.nc.Write(append(b, '\n'))
	return err
}

// event queues ev for the client.
func (c *conn) event(ev Event) {
	c.emu.Lock()
	if len(c.events) >= maxQueued {
		c.lost = true
	} else {
		c.events = append(c.events, ev)
	}
	c.emu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// sendEvents writes the queued events until the connection closes.
func (c *conn) sendEvents() {
	for {
		select {
		case <-c.closed:
			return
		case <-c.wake:
		}
		c.emu.Lock()
		evs, lost := c.events, c.lost
		c.events, c.lost = nil, false
		c.emu.Unlock()
		for _, ev := range evs {
			if c.write(ev) != nil {
				c.close()
				return
			}
		}
		if lost && c.write(Event{Event: "lost"}) != nil {
			c.close()
			return
		}
	}
}

// serve lets the client in, or refuses it, and answers its requests until
// it goes.
func (c *conn) serve() {
	defer c.close()
	refuse := func(why string) {
		_ = c.write(Answer{Error: why})
	}
	if why := c.s.cfg.Check(hooks.PeerOf(c.nc)); why != "" {
		refuse(why)
		return
	}
	_ = c.nc.SetReadDeadline(time.Now().Add(helloTimeout))
	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 64<<10), MaxLine)
	if !sc.Scan() {
		return
	}
	var h Hello
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil || !attach.SameKey(h.Key, c.s.key) {
		refuse("wrong key for this core: read it from the key file beside the socket, or use nerdr api")
		return
	}
	if h.API > core.APIVersion {
		refuse(fmt.Sprintf("this nerdr has API %d, and the client wants %d: update nerdr", core.APIVersion, h.API))
		return
	}
	_ = c.nc.SetReadDeadline(time.Time{})
	if c.write(Welcome{API: core.APIVersion, Version: c.s.cfg.Version, Root: c.s.cfg.Root}) != nil {
		return
	}
	go c.sendEvents()
	for sc.Scan() {
		var r Request
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			if c.write(Answer{Error: "not a request: " + err.Error()}) != nil {
				return
			}
			continue
		}
		v, err := c.do(r)
		a := Answer{ID: r.ID, Result: v}
		if err != nil {
			a = Answer{ID: r.ID, Error: err.Error()}
		} else if v == nil {
			a.Result = struct{}{}
		}
		if c.write(a) != nil {
			return
		}
	}
	if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		_ = c.write(Answer{Error: fmt.Sprintf("a line is at most %d bytes", MaxLine)})
		return
	} else if err != nil {
		return
	}
	// No more requests: a subscriber is told the events still, until it
	// goes, or the core does.
	c.s.mu.Lock()
	sub := c.s.subs[c]
	c.s.mu.Unlock()
	if sub {
		select {
		case <-c.closed:
		case <-c.s.done:
		}
	}
}

// do is what request r gives.
func (c *conn) do(r Request) (any, error) {
	s := c.s
	switch r.Op {
	case "state":
		st, err := s.state()
		return st, err
	case "subscribe":
		return nil, s.subscribe(c)
	case "read":
		id := r.Target
		if id == "" {
			id = r.Agent
		}
		return s.call(false, func(m *core.Core) (any, core.Cmd, error) {
			text, err := m.APIRead(id, r.Lines)
			return ReadResult{Text: text}, nil, err
		})
	case "send":
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			return nil, nil, m.APISend(r.Agent, r.Text)
		})
	case "type":
		id := r.Target
		if id == "" {
			id = r.Agent
		}
		if r.Text == "" && len(r.Keys) == 0 {
			return nil, errors.New("give text, keys or both")
		}
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			return nil, nil, m.APIType(id, r.Text, r.Keys)
		})
	case "answer":
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			cmd, err := m.APIAnswer(r.Question, r.Yes)
			return nil, cmd, err
		})
	case "seen":
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			return nil, nil, m.APISeen(r.Agent)
		})
	case "wake":
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			cmd, err := m.APIWake(r.Tab)
			return nil, cmd, err
		})
	case "repos":
		places, err := s.repoPlaces()
		if err != nil {
			return nil, err
		}
		return core.APIRepos(places.Scan()), nil
	case "new_tab":
		q := core.APINewTab{Repos: r.Repos, Branch: r.Branch, Agent: r.Agent, Model: r.Model, Task: r.Task}
		places, err := s.repoPlaces()
		if err != nil {
			return nil, err
		}
		all := places.Scan()
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			cmd, err := m.APIMakeTab(q, all)
			if err != nil {
				return nil, nil, err
			}
			return NewTabResult{Branch: q.Branch}, cmd, nil
		})
	case "quit":
		// As the user quitting in the TUI: every program stops, and the
		// core ends, the tabs kept for next time. The answer comes before
		// the connection closes, as a rule; a client sees the core gone
		// either way.
		return s.call(true, func(m *core.Core) (any, core.Cmd, error) {
			if m.Quitting {
				return nil, nil, core.ErrAPIQuitting
			}
			return nil, core.QuitAll{}.Do(m), nil
		})
	case "":
		return nil, errors.New("a request needs an op")
	}
	return nil, fmt.Errorf("no op %q; the ops are state, subscribe, read, send, type, answer, seen, wake, repos, new_tab and quit", r.Op)
}
