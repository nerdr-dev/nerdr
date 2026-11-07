package core

import (
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// The screens of core's programs as a client in a process of its own is
// sent them: the client says which it shows, and how far back (ShowScreens);
// core sends each, as rows, when it changes (ScreenServer); the client keeps
// a copy of each to draw (ScreenClient), and sends what the user does in
// them as requests. Each message is a byte for its kind, then JSON.

const (
	wireScreen  byte = 'S' // core: a screen, screenWire
	wireRows    byte = 'A' // core: lines asked for, rowsWire
	wireRequest byte = 'Q' // client: a Request, as EncodeRequest makes it
	wireAsk     byte = 'r' // client: lines of a screen, askWire
)

type screenWire struct {
	Ref ProgramRef `json:"ref"`
	term.ScreenMsg
}

type askWire struct {
	ID       int        `json:"id"`
	Ref      ProgramRef `json:"ref"`
	From, To int
}

type rowsWire struct {
	ID int `json:"id"`
	term.RowsMsg
}

// maxAskRows is the most lines a client is sent for one ask.
const maxAskRows = 10000

func wire(kind byte, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return append([]byte{kind}, b...)
}

// key is r as the key of its screen's messages.
func (r ProgramRef) key() string {
	return "screen:" + strconv.Quote(r.Pane.Core) + strconv.Quote(r.Pane.Pane) + strconv.Quote(r.Plugin)
}

// ScreenView is a screen a client shows, Scroll lines back.
type ScreenView struct {
	Ref    ProgramRef
	Scroll int
}

// ScreensMsg tells a client that a screen it shows has changed.
type ScreensMsg struct{}

func init() { registerRequests(ShowScreens{}) }

// ShowScreens is the screens the client shows now: core sends them, and
// only them, as they change. Reset are those to send every row of again,
// the client's copy being off.
type ShowScreens struct {
	Views []ScreenView
	Reset []ProgramRef
}

func (q ShowScreens) Do(m *Core) Cmd {
	if s := m.Screens; s != nil {
		s.show(q.Views, q.Reset)
		s.Tick(m, false)
	}
	return nil
}

// ScreenServer sends a client the screens it shows. Its requests are
// posted to core's loop, but for what the user does in a screen shown:
// the keys, the mouse, focus and size go to its program there and then,
// with Mu not held, so typing never waits for the loop. Lines asked for
// are answered from the screens shown the same way.
type ScreenServer struct {
	conn *attach.Conn
	post func(RequestEv)
	// posted counts the requests of what the user does in a screen still
	// on their way through the loop: while there are any, the next go that
	// way too, to keep their order.
	posted atomic.Int64

	mu    sync.Mutex
	views map[ProgramRef]int
	reset map[ProgramRef]bool
	shown map[ProgramRef]*shownScreen
}

// shownScreen is a screen a client is sent. The server's mu guards what
// Tick changes; build has the rest.
type shownScreen struct {
	ref  ProgramRef
	pane *term.Pane
	// typed follows what is typed into it, when core does; see KeysTo.
	typed *TypedLine
	gen   uint64 // the DrawGen last sent for
	// scroll and reset are what build goes by, set by Tick.
	scroll atomic.Int64
	reset  atomic.Bool

	mu  sync.Mutex
	snd term.RowsSender
}

// NewScreenServer serves conn the screens it asks for, posting the
// requests it sends with post to be done on core's loop, and reading
// until the connection closes.
func NewScreenServer(conn *attach.Conn, post func(RequestEv)) *ScreenServer {
	s := &ScreenServer{
		conn:  conn,
		post:  post,
		views: map[ProgramRef]int{},
		reset: map[ProgramRef]bool{},
		shown: map[ProgramRef]*shownScreen{},
	}
	conn.OnBehind(s.behind)
	go s.read()
	return s
}

func (s *ScreenServer) read() {
	defer s.stop()
	for {
		b, err := s.conn.Recv()
		if err != nil {
			return
		}
		if len(b) == 0 {
			continue
		}
		switch b[0] {
		case wireRequest:
			r, err := DecodeRequest(b[1:])
			if err != nil {
				Debugf("client: %v", err)
				continue
			}
			if !s.direct(r) {
				s.post(RequestEv{R: r})
			}
		case wireAsk:
			var a askWire
			if json.Unmarshal(b[1:], &a) == nil {
				s.answer(a)
			}
		}
	}
}

// direct does what the user did in a screen shown, there and then, and
// reports whether it did: when not, it is for the loop, and counted while
// it is on its way.
func (s *ScreenServer) direct(r Request) bool {
	var ref ProgramRef
	switch q := r.(type) {
	case Type:
		ref = q.Ref
	case WriteRaw:
		ref = q.Ref
	case Mouse:
		ref = q.Ref
	case FocusProgram:
		ref = q.Ref
	case ResizeProgram:
		ref = q.Ref
	default:
		return false
	}
	s.mu.Lock()
	sc := s.shown[ref]
	s.mu.Unlock()
	if sc == nil || s.posted.Load() > 0 {
		s.posted.Add(1)
		s.post(RequestEv{R: r, Done: func() { s.posted.Add(-1) }})
		return true
	}
	switch q := r.(type) {
	case Type:
		if sc.typed != nil {
			sc.typed.Feed(q.B)
		}
		sc.pane.Input(q.B)
	case WriteRaw:
		sc.pane.Write(q.B)
	case Mouse:
		sc.pane.SendMouse(q.Ev)
	case FocusProgram:
		sc.pane.Focus(q.In)
	case ResizeProgram:
		sc.pane.Resize(q.W, q.H)
	}
	return true
}

// stop lets go of the screens once the client has gone.
func (s *ScreenServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ref, sc := range s.shown {
		sc.pane.Watch(false)
		delete(s.shown, ref)
	}
	clear(s.views)
}

func (s *ScreenServer) show(views []ScreenView, reset []ProgramRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.views)
	for _, v := range views {
		s.views[v.Ref] = v.Scroll
	}
	for _, r := range reset {
		s.reset[r] = true
	}
}

// Tick sends the screens shown that have changed since they were last
// sent; with all, each shown, for what changes with nothing drawn, as
// whether a shell runs a command. Mu is held: it waits for no screen to be
// made.
func (s *ScreenServer) Tick(m *Core, all bool) {
	s.mu.Lock()
	var send []*shownScreen
	for ref, scroll := range s.views {
		tp, _ := m.termOf(ref)
		sc := s.shown[ref]
		if sc != nil && sc.pane != tp {
			sc.pane.Watch(false)
			delete(s.shown, ref)
			sc = nil
		}
		if tp == nil {
			continue
		}
		dirty := all
		if sc == nil {
			// What is typed into it is followed from now on, as when it
			// is given the keyboard: it is typed into there and then.
			KeysTo{ref}.Do(m)
			_, typed := m.termOf(ref)
			sc = &shownScreen{ref: ref, pane: tp, typed: typed}
			s.shown[ref] = sc
			tp.Watch(true)
			dirty = true
		}
		if s.reset[ref] {
			sc.reset.Store(true)
			dirty = true
		}
		if int64(scroll) != sc.scroll.Load() {
			sc.scroll.Store(int64(scroll))
			dirty = true
		}
		if g := tp.DrawGen(); g != sc.gen {
			sc.gen = g
			dirty = true
		}
		if dirty {
			send = append(send, sc)
		}
	}
	for ref, sc := range s.shown {
		if _, ok := s.views[ref]; !ok {
			sc.pane.Watch(false)
			delete(s.shown, ref)
		}
	}
	clear(s.reset)
	var unseen []*term.Pane
	if all {
		unseen = s.unseen(m)
	}
	s.mu.Unlock()
	for _, tp := range unseen {
		if text := tp.TakeCopied(); text != "" {
			Debugf("osc 52: %d bytes from a program not shown, not put on the clipboard", len(text))
		}
	}
	for _, sc := range send {
		s.conn.SendFunc(sc.ref.key(), s.build(sc))
	}
}

// unseen are the programs core runs that the client is not sent: what
// they put on the clipboard is let go, as a client does with what comes
// from a program not in use, so it is not taken for the user's once the
// program is shown. Mu and s.mu are held.
func (s *ScreenServer) unseen(m *Core) []*term.Pane {
	shown := map[*term.Pane]bool{}
	for _, sc := range s.shown {
		shown[sc.pane] = true
	}
	var out []*term.Pane
	add := func(tp *term.Pane) {
		if tp != nil && !shown[tp] {
			out = append(out, tp)
		}
	}
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			add(p.Term())
		}
	}
	for _, u := range m.PluginUIs {
		if u != nil {
			add(u.Pane)
		}
	}
	return out
}

// build makes sc's message as it is to be written: the rows changed since
// the last written.
func (s *ScreenServer) build(sc *shownScreen) func() []byte {
	return func() []byte {
		s.mu.Lock()
		still := s.shown[sc.ref] == sc
		s.mu.Unlock()
		if !still {
			return nil
		}
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if sc.reset.Swap(false) {
			sc.snd.Reset()
		}
		sc.pane.ViewInto(sc.snd.View(), int(sc.scroll.Load()))
		return wire(wireScreen, screenWire{Ref: sc.ref, ScreenMsg: sc.snd.Msg()})
	}
}

// behind sends every screen shown afresh, what waited for the client
// having been dropped.
func (s *ScreenServer) behind() {
	s.mu.Lock()
	var send []*shownScreen
	for _, sc := range s.shown {
		sc.reset.Store(true)
		send = append(send, sc)
	}
	s.mu.Unlock()
	for _, sc := range send {
		s.conn.SendFunc(sc.ref.key(), s.build(sc))
	}
}

// answer sends the lines a asks for, of a screen shown; none of one not.
func (s *ScreenServer) answer(a askWire) {
	s.mu.Lock()
	sc := s.shown[a.Ref]
	s.mu.Unlock()
	out := rowsWire{ID: a.ID}
	if sc != nil && a.To >= a.From {
		to := min(a.To, a.From+maxAskRows-1)
		first, rows := sc.pane.Rows(a.From, to)
		out.RowsMsg = term.RowsMsgOf(first, rows)
	}
	s.conn.Send("", wire(wireRows, out))
}

// ScreenClient keeps copies of the screens of a core in another process:
// those drawn are the ones the core is told to send (see Shown), and what
// the user does in them is sent as requests.
type ScreenClient struct {
	conn *attach.Conn
	tell func(any)

	mu     sync.Mutex
	copies map[ProgramRef]*term.Copy
	sent   []ScreenView
	reset  map[ProgramRef]bool
	asks   map[int]func(int, [][]term.Cell)
	askN   int
}

// NewScreenClient keeps copies of the screens conn sends. It tells the
// client ScreensMsg when one has changed, by tell, from a goroutine of its
// own: tell must not wait for the client to take it.
func NewScreenClient(conn *attach.Conn, tell func(any)) *ScreenClient {
	c := &ScreenClient{
		conn:   conn,
		tell:   tell,
		copies: map[ProgramRef]*term.Copy{},
		reset:  map[ProgramRef]bool{},
		asks:   map[int]func(int, [][]term.Cell){},
	}
	go c.read()
	return c
}

// Tell gives the client msg as the client's own messages are given it:
// for what comes of the screens, from the goroutine that reads them.
func (c *ScreenClient) Tell(msg any) { c.tell(msg) }

// Program is the copy of the screen of the program r names.
func (c *ScreenClient) Program(r ProgramRef) Program {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.copies[r]
	if cp == nil {
		cp = term.NewCopy(80, 24, copyOut{c, r})
		c.copies[r] = cp
	}
	return cp
}

// Drop lets go of the copies of programs that are not live.
func (c *ScreenClient) Drop(live func(ProgramRef) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for r := range c.copies {
		if !live(r) {
			delete(c.copies, r)
		}
	}
}

// Shown tells the core which screens were drawn on the frame just drawn,
// and how far back, when that has changed.
func (c *ScreenClient) Shown() {
	c.mu.Lock()
	var views []ScreenView
	for r, cp := range c.copies {
		if drawn, scroll := cp.Drawn(); drawn {
			views = append(views, ScreenView{Ref: r, Scroll: scroll})
		}
	}
	slices.SortFunc(views, func(a, b ScreenView) int {
		switch {
		case a.Ref.key() < b.Ref.key():
			return -1
		case a.Ref.key() > b.Ref.key():
			return 1
		}
		return 0
	})
	if slices.Equal(views, c.sent) && len(c.reset) == 0 {
		c.mu.Unlock()
		return
	}
	c.sent = views
	q := ShowScreens{Views: views}
	for r := range c.reset {
		q.Reset = append(q.Reset, r)
	}
	clear(c.reset)
	c.mu.Unlock()
	c.send(q)
}

func (c *ScreenClient) send(r Request) {
	b, err := EncodeRequest(r)
	if err != nil {
		Debugf("client: %v", err)
		return
	}
	c.conn.Send("", append([]byte{wireRequest}, b...))
}

func (c *ScreenClient) read() {
	defer func() {
		c.mu.Lock()
		asks := c.asks
		c.asks = map[int]func(int, [][]term.Cell){}
		c.mu.Unlock()
		for _, done := range asks {
			done(0, nil)
		}
	}()
	for {
		b, err := c.conn.Recv()
		if err != nil {
			return
		}
		if len(b) == 0 {
			continue
		}
		switch b[0] {
		case wireScreen:
			var w screenWire
			if err := json.Unmarshal(b[1:], &w); err != nil {
				Debugf("core: screen: %v", err)
				continue
			}
			c.mu.Lock()
			cp := c.copies[w.Ref]
			c.mu.Unlock()
			if cp == nil {
				continue
			}
			if err := cp.Apply(&w.ScreenMsg); err != nil {
				Debugf("core: screen: %v", err)
				c.mu.Lock()
				c.reset[w.Ref] = true
				c.mu.Unlock()
				c.Shown()
			}
			c.tell(ScreensMsg{})
		case wireRows:
			var w rowsWire
			if json.Unmarshal(b[1:], &w) != nil {
				continue
			}
			c.mu.Lock()
			done := c.asks[w.ID]
			delete(c.asks, w.ID)
			c.mu.Unlock()
			if done == nil {
				continue
			}
			first, rows, err := w.Cells()
			if err != nil {
				Debugf("core: rows: %v", err)
				first, rows = w.First, nil
			}
			done(first, rows)
		}
	}
}

// askRows asks the core for lines from to to of r's screen, for done.
func (c *ScreenClient) askRows(r ProgramRef, from, to int, done func(int, [][]term.Cell)) {
	c.mu.Lock()
	c.askN++
	id := c.askN
	c.asks[id] = done
	c.mu.Unlock()
	if !c.conn.Send("", wire(wireAsk, askWire{ID: id, Ref: r, From: from, To: to})) {
		c.mu.Lock()
		_, still := c.asks[id]
		delete(c.asks, id)
		c.mu.Unlock()
		if still {
			done(from, nil)
		}
	}
}

// copyOut sends what the user does in the copy of r's screen.
type copyOut struct {
	c   *ScreenClient
	ref ProgramRef
}

func (o copyOut) Input(b []byte) { o.c.send(Type{Ref: o.ref, B: slices.Clone(b)}) }
func (o copyOut) Write(b []byte) { o.c.send(WriteRaw{Ref: o.ref, B: slices.Clone(b)}) }
func (o copyOut) Mouse(ev term.MouseEvent) {
	o.c.send(Mouse{Ref: o.ref, Ev: ev})
}
func (o copyOut) Focus(in bool)     { o.c.send(FocusProgram{Ref: o.ref, In: in}) }
func (o copyOut) Resize(w, h int)   { o.c.send(ResizeProgram{Ref: o.ref, W: w, H: h}) }
func (o copyOut) Scroll(scroll int) {} // see Shown
func (o copyOut) AskRows(from, to int, done func(int, [][]term.Cell)) {
	o.c.askRows(o.ref, from, to, done)
}

var _ Program = (*term.Copy)(nil)
