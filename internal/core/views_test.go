package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

func sizeIs(t *testing.T, v *Views, p PaneRef, now time.Time, w, h int) {
	t.Helper()
	gw, gh, ok := v.Size(p, now)
	if !ok || gw != w || gh != h {
		t.Fatalf("size %d×%d (%v), want %d×%d", gw, gh, ok, w, h)
	}
}

// TestViewsLastActiveLeads: the viewer active in a program last sets its
// size; one renewing its view as it was does not take it back, one typing
// does; when it goes, the one active before it takes over.
func TestViewsLastActiveLeads(t *testing.T) {
	var v Views
	p := PaneRef{Pane: "p1"}
	now := time.Now()
	if _, _, ok := v.Size(p, now); ok {
		t.Fatal("a size with no views")
	}
	v.Show(p, "phone", 60, 30, ViewTTL, now)
	sizeIs(t, &v, p, now, 60, 30)
	v.Show(p, "tablet", 140, 50, ViewTTL, now)
	sizeIs(t, &v, p, now, 140, 50)

	if v.Show(p, "phone", 60, 30, ViewTTL, now) { // renewed as it was
		t.Fatal("a view renewed took the size from one active since")
	}
	sizeIs(t, &v, p, now, 140, 50)
	if !v.Touch(p, "phone", now) {
		t.Fatal("typing did not count")
	}
	sizeIs(t, &v, p, now, 60, 30)
	if v.Touch(p, "laptop", now) {
		t.Fatal("a viewer not showing the program was counted")
	}
	if !v.Show(p, "tablet", 150, 50, ViewTTL, now) { // its window changed size
		t.Fatal("a view resized did not take the size")
	}
	sizeIs(t, &v, p, now, 150, 50)

	if !v.Drop(p, "tablet") {
		t.Fatal("no view dropped")
	}
	sizeIs(t, &v, p, now, 60, 30)
	if got := v.DropViewer("phone"); len(got) != 1 || got[0].Pane != "p1" {
		t.Fatalf("dropped %v", got)
	}
	if _, _, ok := v.Size(p, now); ok {
		t.Fatal("a size with every view gone")
	}
}

// TestViewsRunOut: a view not renewed in ViewTTL counts no more, and the
// one active before it leads again; the TUI's lasts.
func TestViewsRunOut(t *testing.T) {
	var v Views
	p := PaneRef{Pane: "p1"}
	now := time.Now()
	v.Show(p, ClientViewer, 200, 60, 0, now)
	v.Show(p, "phone", 60, 30, ViewTTL, now.Add(time.Second))
	sizeIs(t, &v, p, now.Add(time.Second), 60, 30)
	v.Show(p, "phone", 60, 30, ViewTTL, now.Add(20*time.Second)) // renewed
	sizeIs(t, &v, p, now.Add(70*time.Second), 60, 30)

	later := now.Add(20*time.Second + ViewTTL)
	sizeIs(t, &v, p, later, 200, 60) // run out, even before Expire
	if v.Touch(p, "phone", later) {
		t.Fatal("a view run out was made active by typing")
	}
	if got := v.Expire(later, nil); len(got) != 1 {
		t.Fatalf("expired on %v", got)
	}
	if v.Shows(p, "phone", now) {
		t.Fatal("the view run out is still there")
	}
	sizeIs(t, &v, p, later.Add(time.Hour), 200, 60)

	// A view run out and made again counts as opened afresh.
	v.Show(p, "phone", 60, 30, ViewTTL, later)
	sizeIs(t, &v, p, later, 60, 30)

	// Panes gone take their views with them.
	v.Expire(later, func(string) bool { return false })
	if _, _, ok := v.Size(p, later); ok {
		t.Fatal("views kept for a pane gone")
	}
}

// TestAPIViewSizesTheProgram: an API connection's view gives the program
// its size, the last active of two connections leading; typing makes one
// active; one gone or unviewing leaves the other's size, and with none
// left the size stays.
func TestAPIViewSizesTheProgram(t *testing.T) {
	m := apiCore(t)
	prog := m.Tabs[0].Pane
	size := func(w, h int) {
		t.Helper()
		if gw, gh := prog.Size(); gw != w || gh != h {
			t.Fatalf("the program is %d×%d, want %d×%d", gw, gh, w, h)
		}
		if gw, gh := m.APISize("t1"); gw != w || gh != h {
			t.Fatalf("read's size %d×%d, want %d×%d", gw, gh, w, h)
		}
	}
	if w, h, err := m.APIView("api:1", "t1", 60, 20); err != nil || w != 60 || h != 20 {
		t.Fatalf("view: %d×%d, %v", w, h, err)
	}
	size(60, 20)
	if w, h, _ := m.APIView("api:2", "t1", 100, 30); w != 100 || h != 30 {
		t.Fatalf("view: %d×%d", w, h)
	}
	size(100, 30)
	// Renewed as it was: the size is the other's, and the answer says so.
	if w, h, _ := m.APIView("api:1", "t1", 60, 20); w != 100 || h != 30 {
		t.Fatalf("renewed view answered %d×%d, want the program's 100×30", w, h)
	}
	size(100, 30)
	m.APIActive("api:1", "t1")
	size(60, 20)
	m.APIViewerGone("api:1")
	size(100, 30)
	if err := m.APIUnview("api:2", "t1"); err != nil {
		t.Fatal(err)
	}
	size(100, 30) // no claim left: kept as it is

	// Brought within bounds.
	if w, h, _ := m.APIView("api:3", "t1", 5, 1000); w != MinViewCols || h != MaxViewRows {
		t.Fatalf("view clamped to %d×%d", w, h)
	}
	if _, _, err := m.APIView("api:3", "nope", 80, 24); err == nil {
		t.Fatal("a view of no program")
	}
	if _, _, err := m.APIView("api:3", "t1", 0, 24); err == nil {
		t.Fatal("a view of no size")
	}
}

// countingProc counts the times its terminal is resized.
type countingProc struct {
	spawn.Proc
	n *atomic.Int32
}

func (p countingProc) Resize(w, h int) error {
	p.n.Add(1)
	return p.Proc.Resize(w, h)
}

type countingSpawner struct {
	specs []spawn.Spec
	n     atomic.Int32
}

func (s *countingSpawner) Spawn(sp spawn.Spec) (spawn.Proc, error) {
	s.specs = append(s.specs, sp)
	p, err := spawn.Local{}.Spawn(sp)
	if err != nil {
		return nil, err
	}
	return countingProc{p, &s.n}, nil
}

// TestDefaultSizeWithNoClient: a program started with no client to say
// how large it is shown starts at 120×40, or as the config says, not at
// the smallest a screen can be.
func TestDefaultSizeWithNoClient(t *testing.T) {
	sp := &countingSpawner{}
	m := &Core{Client: NoClient{}, Spawner: sp}
	m.Cfg.Shell = "cat"
	tab := &Tab{}
	tab.Dir = t.TempDir()
	p, _ := m.NewShellIn(tab, true)
	if p == nil || p.Shell == nil {
		t.Fatal("the terminal did not start")
	}
	defer p.Shell.Close()
	if s := sp.specs[0]; s.W != DefaultCols || s.H != DefaultRows {
		t.Fatalf("started at %d×%d, want %d×%d", s.W, s.H, DefaultCols, DefaultRows)
	}
	if w, h := p.Shell.Size(); w != DefaultCols || h != DefaultRows {
		t.Fatalf("its screen is %d×%d", w, h)
	}

	m.Cfg.HeadlessCols, m.Cfg.HeadlessRows = 90, 30
	q, _ := m.NewShellIn(tab, true)
	defer q.Shell.Close()
	if s := sp.specs[1]; s.W != 90 || s.H != 30 {
		t.Fatalf("started at %d×%d, want the config's 90×30", s.W, s.H)
	}
}

// TestViewUnchangedSizeNoResize: a view at the size the program has, or
// renewed, does not resize its terminal; only a size of its own does.
func TestViewUnchangedSizeNoResize(t *testing.T) {
	sp := &countingSpawner{}
	m := &Core{Client: NoClient{}, Spawner: sp}
	m.Cfg.Shell = "cat"
	tab := &Tab{}
	tab.Dir = t.TempDir()
	m.Tabs = []*Tab{tab}
	p, _ := m.NewShellIn(tab, true)
	defer p.Shell.Close()
	id := m.PaneRef(p).Pane
	before := sp.n.Load()
	for _, who := range []string{"api:1", "api:1", "api:2"} {
		if _, _, err := m.APIView(who, id, DefaultCols, DefaultRows); err != nil {
			t.Fatal(err)
		}
	}
	m.APIActive("api:1", id)
	m.ExpireViews(time.Now())
	if n := sp.n.Load() - before; n != 0 {
		t.Fatalf("resized %d times at the size it had", n)
	}
	m.APIView("api:1", id, 80, 24)
	m.APIView("api:1", id, 80, 24)
	if n := sp.n.Load() - before; n != 1 {
		t.Fatalf("resized %d times, want once", n)
	}
}

// TestViewsRunOutOnTheLoop: ExpireViews gives the program the size of the
// view left once one runs out.
func TestViewsRunOutOnTheLoop(t *testing.T) {
	m := apiCore(t)
	ref := m.PaneRef(m.Tabs[0].PaneOf())
	now := time.Now()
	m.Views.Show(ref, "api:1", 70, 20, ViewTTL, now)
	m.Views.Show(ref, "api:2", 50, 15, time.Millisecond, now)
	m.applyView(ref)
	if w, _ := m.Tabs[0].Pane.Size(); w != 50 {
		t.Fatalf("width %d", w)
	}
	m.ExpireViews(now.Add(time.Second))
	if w, h := m.Tabs[0].Pane.Size(); w != 70 || h != 20 {
		t.Fatalf("%d×%d after the leader ran out, want 70×20", w, h)
	}
}

// TestClientAndAPIView: the TUI is a viewer like the others, over its
// connection: its size until a phone views the program, the phone's until
// the user types in the TUI, the TUI's again; and the phone's renewal
// does not take it back.
func TestClientAndAPIView(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	tb.Tab = state.Tab{ID: "t1", Agent: agents.Codex}
	c := &Core{Tabs: []*Tab{tb}, Client: NoClient{}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}

	cl := viewedOver(t, c, func(ev RequestEv) {
		c.Mu.Lock()
		ev.Run(c)
		c.Mu.Unlock()
	})
	cp := cl.Program(ref)
	// Shown: what the user does in it is done there and then, away from
	// the loop.
	until(t, "the program's lines", cl, cp, 0, func(f term.Frame) bool { return shows(f, "line 39") })
	waitSize := func(what string, w, h int) { t.Helper(); waitForSize(t, what, p, w, h) }

	cp.Resize(90, 25)
	waitSize("the TUI's size", 90, 25)

	c.Mu.Lock()
	w, h, err := c.APIView("api:1", "t1", 50, 15)
	c.Mu.Unlock()
	if err != nil || w != 50 || h != 15 {
		t.Fatalf("view: %d×%d, %v", w, h, err)
	}
	waitSize("the phone's size, viewed since", 50, 15)

	cp.Input([]byte("x"))
	waitSize("the TUI's again, typed in", 90, 25)

	c.Mu.Lock()
	w, h, _ = c.APIView("api:1", "t1", 50, 15)
	c.Mu.Unlock()
	if w != 90 || h != 25 {
		t.Fatalf("the renewed view answered %d×%d, want the TUI's 90×25", w, h)
	}
	time.Sleep(50 * time.Millisecond)
	waitSize("the TUI's, the view only renewed", 90, 25)

	c.Mu.Lock()
	c.APIActive("api:1", "t1")
	c.Mu.Unlock()
	waitSize("the phone's, typed in", 50, 15)

	// Focus said again, as the TUI does when a terminal opens beside, is
	// not the user moving there; focus coming back is.
	cp.Focus(true)
	waitSize("the TUI's, focused", 90, 25)
	c.Mu.Lock()
	c.APIActive("api:1", "t1")
	c.Mu.Unlock()
	waitSize("the phone's, typed in again", 50, 15)
	cp.Focus(true)
	time.Sleep(50 * time.Millisecond)
	waitSize("the phone's, focus only said again", 50, 15)
	cp.Focus(false)
	cp.Focus(true)
	waitSize("the TUI's, focus back", 90, 25)

	cp.Resize(100, 30) // the TUI's window changed size
	waitSize("the TUI's new size", 100, 30)

	// With the loop held, typing in the screen shown gives it the TUI's
	// size still: chosen and given there, under the views' lock.
	c.Mu.Lock()
	c.APIActive("api:1", "t1")
	c.Mu.Unlock()
	waitSize("the phone's, typed in once more", 50, 15)
	c.Mu.Lock()
	cp.Input([]byte("y"))
	waitSize("the TUI's, with the loop held", 100, 30)
	c.Mu.Unlock()

	c.Mu.Lock()
	c.APIActive("api:1", "t1")
	c.viewerGone(ClientViewer) // the TUI gone: the phone's holds
	c.Mu.Unlock()
	waitSize("the phone's, the TUI gone", 50, 15)
}

// viewedOver is core c serving its screens to a client whose sizes it
// counts, as AttachClient's, over a pair of connections, the loop's ticks
// played by the test and its requests given to post.
func viewedOver(t *testing.T, c *Core, post func(RequestEv)) *ScreenClient {
	t.Helper()
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(""); b.Close("") })
	c.Screens = serveScreens(a, post, &clientParts{sizes: &c.Views, viewer: ClientViewer,
		other: func([]byte) {}, behind: func() {}, gone: func() {}})
	stop, done := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(stop); <-done })
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				c.Mu.Lock()
				c.Screens.Tick(c, false)
				c.Mu.Unlock()
			}
		}
	}()
	return NewScreenClient(b, func(any) {})
}

func waitForSize(t *testing.T, what string, p *term.Pane, w, h int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if gw, gh := p.Size(); gw == w && gh == h {
			return
		}
		if time.Now().After(deadline) {
			gw, gh := p.Size()
			t.Fatalf("%s: %d×%d, want %d×%d", what, gw, gh, w, h)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestQueuedKeysCountInTurn: a key that waits for the loop, behind an API
// viewer's typing, counts as the TUI active there in its turn, not as it
// came: the API's typing lands at the API's size, the key at the TUI's.
func TestQueuedKeysCountInTurn(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	tb.Tab = state.Tab{ID: "t1", Agent: agents.Codex}
	c := &Core{Tabs: []*Tab{tb}, Client: NoClient{}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
	var mu sync.Mutex
	var held []RequestEv
	cl := viewedOver(t, c, func(ev RequestEv) {
		mu.Lock()
		held = append(held, ev)
		mu.Unlock()
	})
	runHeld := func() {
		mu.Lock()
		evs := held
		held = nil
		mu.Unlock()
		for _, ev := range evs {
			c.Mu.Lock()
			ev.Run(c)
			c.Mu.Unlock()
		}
	}
	cp := cl.Program(ref)
	cp.Resize(90, 25)
	deadline := time.Now().Add(5 * time.Second)
	for {
		runHeld()
		if w, _ := p.Size(); w == 90 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the TUI's size never came")
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.Mu.Lock()
	c.APIView("api:1", "t1", 50, 15)
	c.Mu.Unlock()

	cp.Input([]byte("x")) // not shown: for the loop, held
	time.Sleep(100 * time.Millisecond)
	if !c.Views.Shows(ref.Pane, ClientViewer, time.Now()) {
		t.Fatal("the TUI's view is gone")
	}
	if w, h, _ := c.Views.Size(ref.Pane, time.Now()); w != 50 || h != 15 {
		t.Fatalf("the key, still held, counted already: %d×%d leads", w, h)
	}
	runHeld()
	waitForSize(t, "the TUI's, its key done", p, 90, 25)
}

// TestNewWindowTakesOver: a window taking over from one whose connection
// has not closed yet lets go of the old one's sizes, and a program the new
// one does not show gets the size of the API's viewer left.
func TestNewWindowTakesOver(t *testing.T) {
	m := apiCore(t)
	m.Client = NoClient{}
	ref := m.PaneRef(m.Tabs[0].PaneOf())
	post := func(RequestEv) {}
	pairs := func() *attach.Conn {
		a, b, err := attach.Pair()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { a.Close(""); b.Close("") })
		return a
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	first := m.AttachClient(pairs(), post)
	m.APIView("api:1", "t1", 50, 15)
	resize := clientDid{who: first.viewer, r: ResizeProgram{Ref: ProgramRef{Pane: ref}, W: 90, H: 25}}
	resize.Do(m) // the first window's, since
	if w, h := m.Tabs[0].Pane.Size(); w != 90 || h != 25 {
		t.Fatalf("%d×%d, want the window's 90×25", w, h)
	}
	second := m.AttachClient(pairs(), post) // the first still connected
	if w, h := m.Tabs[0].Pane.Size(); w != 50 || h != 15 {
		t.Fatalf("%d×%d, want the phone's 50×15 once the first window let go", w, h)
	}
	if first.viewer == second.viewer {
		t.Fatal("two windows one viewer")
	}

	// What the first still says, on its way when the second came, counts
	// for nothing; and its going later takes nothing of the second's.
	inputs := m.Tabs[0].Pane.Inputs()
	clientDid{who: first.viewer, r: ResizeProgram{Ref: ProgramRef{Pane: ref}, W: 70, H: 22}}.Do(m)
	clientDid{who: first.viewer, r: Type{Ref: ProgramRef{Pane: ref}, B: []byte("x"), User: true}, act: true}.Do(m)
	if n := m.Tabs[0].Pane.Inputs() - inputs; n != 0 {
		t.Fatalf("%d inputs of the first window's went in after the second took over", n)
	}
	if w, h := m.Tabs[0].Pane.Size(); w != 50 || h != 15 {
		t.Fatalf("%d×%d after the first window's stale resize, want the phone's 50×15", w, h)
	}
	if m.Views.Shows(ref, first.viewer, time.Now()) {
		t.Fatal("the first window's view came back")
	}
	clientDid{who: second.viewer, r: ResizeProgram{Ref: ProgramRef{Pane: ref}, W: 100, H: 30}}.Do(m)
	clientGone{rc: first}.Do(m)
	if w, h := m.Tabs[0].Pane.Size(); w != 100 || h != 30 {
		t.Fatalf("%d×%d, want the second window's 100×30", w, h)
	}
	if m.Views.Gone(first.viewer) {
		t.Fatal("the first window kept among those gone once its connection closed")
	}
}

// TestDefaultSizeAfterTheClientWent: a client gone says no size for a
// program to start at: it starts at the default, not at what the client
// showed last.
func TestDefaultSizeAfterTheClientWent(t *testing.T) {
	sp := &countingSpawner{}
	rc := &RemoteClient{state: ClientState{Fresh: PaneSize{W: 200, H: 60}}}
	rc.gone.Store(true)
	m := &Core{Client: rc, Spawner: sp}
	m.Cfg.Shell = "cat"
	tab := &Tab{}
	tab.Dir = t.TempDir()
	p, _ := m.NewShellIn(tab, true)
	if p == nil || p.Shell == nil {
		t.Fatal("the terminal did not start")
	}
	defer p.Shell.Close()
	if s := sp.specs[0]; s.W != DefaultCols || s.H != DefaultRows {
		t.Fatalf("started at %d×%d, want %d×%d", s.W, s.H, DefaultCols, DefaultRows)
	}
}

// TestRefusedTypingTakesNoSize: typing the API refuses, a key it does not
// know, leaves the size with the viewer that had it.
func TestRefusedTypingTakesNoSize(t *testing.T) {
	m := apiCore(t)
	m.APIView("api:1", "t1", 60, 20)
	m.APIView("api:2", "t1", 100, 30)
	if err := m.APITypeBy("api:1", "t1", "", []string{"no-such-key"}); err == nil {
		t.Fatal("a key that is not one was typed")
	}
	if w, h := m.Tabs[0].Pane.Size(); w != 100 || h != 30 {
		t.Fatalf("%d×%d, want the other viewer's 100×30", w, h)
	}
	if err := m.APITypeBy("api:1", "t1", "", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if w, h := m.Tabs[0].Pane.Size(); w != 60 || h != 20 {
		t.Fatalf("%d×%d, want the typing viewer's 60×20", w, h)
	}
}

// fakeProg is a program's size, guarded, for Views to give it.
type fakeProg struct {
	mu   sync.Mutex
	w, h int
}

func (p *fakeProg) Size() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.w, p.h
}

func (p *fakeProg) Resize(w, h int) {
	p.mu.Lock()
	p.w, p.h = w, h
	p.mu.Unlock()
}

// TestActIsOneGo: what a viewer does in a program lands at its size, an
// API view coming in at the same time from another goroutine, as the
// loop's do while the TUI types in a screen shown, never between.
func TestActIsOneGo(t *testing.T) {
	var v Views
	pane := PaneRef{Pane: "p1"}
	prog := &fakeProg{}
	now := time.Now()
	v.ShowApply(pane, "client:1", 90, 25, 0, now, prog, false)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			v.ShowApply(pane, "api:1", 50+i%2, 15, ViewTTL, now, prog, false)
		}
	}()
	for range 2000 {
		v.Act(pane, "client:1", now, prog, func() {
			if w, h := prog.Size(); w != 90 || h != 25 {
				t.Errorf("typed at %d×%d, want the typing viewer's 90×25", w, h)
			}
		})
	}
	close(stop)
	<-done
}

// TestActAfterTheLeaderRanOut: once the view that led has run out, one
// typing gets its size there and then, though it led already by then,
// and the run-out view was not taken away yet.
func TestActAfterTheLeaderRanOut(t *testing.T) {
	var v Views
	pane := PaneRef{Pane: "p1"}
	prog := &fakeProg{}
	now := time.Now()
	v.ShowApply(pane, "client:1", 90, 25, 0, now, prog, false)
	v.ShowApply(pane, "api:1", 50, 15, ViewTTL, now.Add(time.Second), prog, false)
	if w, _ := prog.Size(); w != 50 {
		t.Fatalf("width %d, want the phone's", w)
	}
	v.Act(pane, "client:1", now.Add(time.Second+ViewTTL), prog, nil)
	if w, h := prog.Size(); w != 90 || h != 25 {
		t.Fatalf("%d×%d, want the TUI's 90×25 at its key", w, h)
	}
}

// TestGoneViewerCountsNoMore: a viewer gone, what it still says creates
// no view.
func TestGoneViewerCountsNoMore(t *testing.T) {
	var v Views
	pane := PaneRef{Pane: "p1"}
	now := time.Now()
	v.Show(pane, "api:1", 50, 15, ViewTTL, now)
	v.DropViewer("api:1")
	v.Show(pane, "api:1", 50, 15, ViewTTL, now)
	if v.Shows(pane, "api:1", now) {
		t.Fatal("a viewer gone made a view")
	}
}

// TestBackgroundTabResizedKeepsThePhonesSize: the TUI's window resized
// sizes the programs of the tabs in the background too; that is not the
// user active in them, and one a phone shows keeps the phone's size.
func TestBackgroundTabResizedKeepsThePhonesSize(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	tb.Tab = state.Tab{ID: "t1", Agent: agents.Codex}
	c := &Core{Tabs: []*Tab{tb}, Client: NoClient{}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
	cl := viewedOver(t, c, func(ev RequestEv) {
		c.Mu.Lock()
		ev.Run(c)
		c.Mu.Unlock()
	})
	cp := cl.Program(ref) // never drawn: in a tab in the background
	cp.Resize(90, 25)
	waitForSize(t, "the TUI's, the only one", p, 90, 25)
	c.Mu.Lock()
	c.APIView("api:1", "t1", 50, 15)
	c.Mu.Unlock()
	cp.Resize(100, 30) // the window resized
	time.Sleep(100 * time.Millisecond)
	waitForSize(t, "the phone's, the tab in the background", p, 50, 15)

	// Drawn, the tab in front: the window resized again is the user's.
	until(t, "the program's lines", cl, cp, 0, func(f term.Frame) bool { return shows(f, "line") })
	cp.Resize(110, 32)
	waitForSize(t, "the TUI's, on screen", p, 110, 32)
}

// TestKeysToHoldsTheKeysAfter: the keyboard given to a program, on its
// way to the loop, holds back the keys typed after it, in a screen shown,
// so they come after it, not before.
func TestKeysToHoldsTheKeysAfter(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	tb.Tab = state.Tab{ID: "t1", Agent: agents.Codex}
	c := &Core{Tabs: []*Tab{tb}, Client: NoClient{}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
	var mu sync.Mutex
	var held []RequestEv
	hold := false
	cl := viewedOver(t, c, func(ev RequestEv) {
		mu.Lock()
		if hold {
			held = append(held, ev)
			mu.Unlock()
			return
		}
		mu.Unlock()
		c.Mu.Lock()
		ev.Run(c)
		c.Mu.Unlock()
	})
	cp := cl.Program(ref)
	cp.Resize(90, 25)
	until(t, "the program's lines", cl, cp, 0, func(f term.Frame) bool { return shows(f, "line 39") })
	waitForSize(t, "the TUI's", p, 90, 25)

	mu.Lock()
	hold = true
	mu.Unlock()
	cl.send(KeysTo{Ref: ref})
	time.Sleep(50 * time.Millisecond)
	cp.Input([]byte("after\r"))
	time.Sleep(100 * time.Millisecond)
	if shows(p.Frame(0), "got after") {
		t.Fatal("a key went in before the keyboard given, held for the loop")
	}
	mu.Lock()
	evs := held
	held, hold = nil, false
	mu.Unlock()
	if len(evs) != 2 {
		t.Fatalf("%d held, want the keyboard and the key", len(evs))
	}
	for _, ev := range evs {
		c.Mu.Lock()
		ev.Run(c)
		c.Mu.Unlock()
	}
	until(t, "the key", cl, cp, 0, func(f term.Frame) bool { return shows(f, "got after") })
}

// TestCoreOwnRequestsFromTheWireDoNothing: clientDid and clientGone, core's
// own, come from the wire as nothing, and do nothing.
func TestCoreOwnRequestsFromTheWireDoNothing(t *testing.T) {
	m := apiCore(t)
	for _, op := range []string{"clientDid", "clientGone"} {
		r, err := DecodeRequest([]byte(`{"op":"` + op + `","req":{"who":"client:1","r":{},"rc":{}}}`))
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		r.Do(m)
	}
}

// TestAPIViewerGoneIsForgotten: an API connection gone leaves nothing
// behind among the viewers gone.
func TestAPIViewerGoneIsForgotten(t *testing.T) {
	m := apiCore(t)
	m.APIView("api:1", "t1", 50, 15)
	m.APIViewerGone("api:1")
	if m.Views.Gone("api:1") || len(m.Views.gone) != 0 {
		t.Fatal("an API viewer gone was kept")
	}
}

// TestKeysToAskedIsTheUserActive: the keyboard given to a program as the
// TUI asks for it, by an asked request (CoreConn.Run), is the TUI active
// there: it takes the size back from a phone, and is still answered.
func TestKeysToAskedIsTheUserActive(t *testing.T) {
	m := apiCore(t)
	ref := m.PaneRef(m.Tabs[0].PaneOf())
	rc := &RemoteClient{viewer: "client:1"}
	m.Views.ShowApply(ref, rc.viewer, 90, 25, 0, time.Now(), m.Tabs[0].Pane, false)
	m.APIView("api:1", "t1", 50, 15)
	req, err := EncodeRequest(KeysTo{Ref: ProgramRef{Pane: ref}})
	if err != nil {
		t.Fatal(err)
	}
	var posted []RequestEv
	rc.read(wire(wireAsked, askedWire{ID: 7, Req: req}), func(ev RequestEv) { posted = append(posted, ev) })
	if len(posted) != 1 {
		t.Fatalf("%d posted", len(posted))
	}
	posted[0].Run(m)
	if w, h := m.Tabs[0].Pane.Size(); w != 90 || h != 25 {
		t.Fatalf("%d×%d, want the TUI's 90×25", w, h)
	}
	if rc.lastDone != 7 || len(rc.out) != 1 {
		t.Fatalf("not answered: last done %d, %d out", rc.lastDone, len(rc.out))
	}
}

// TestGoneClientWritesNothing: what a client gone writes to a screen it
// shows, not counted as the user active, does not go in either.
func TestGoneClientWritesNothing(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	tb.Tab = state.Tab{ID: "t1", Agent: agents.Codex}
	c := &Core{Tabs: []*Tab{tb}, Client: NoClient{}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
	cl := viewedOver(t, c, func(ev RequestEv) {
		c.Mu.Lock()
		ev.Run(c)
		c.Mu.Unlock()
	})
	cp := cl.Program(ref)
	until(t, "the program's lines", cl, cp, 0, func(f term.Frame) bool { return shows(f, "line 39") })
	cl.send(WriteRaw{Ref: ref, B: []byte("before\r")})
	until(t, "what it wrote", cl, cp, 0, func(f term.Frame) bool { return shows(f, "got before") })
	c.Mu.Lock()
	c.viewerGone(ClientViewer)
	c.Mu.Unlock()
	cl.send(WriteRaw{Ref: ref, B: []byte("after\r")})
	time.Sleep(150 * time.Millisecond)
	if shows(p.Frame(0), "got after") {
		t.Fatal("a client gone wrote to the program")
	}
}

// TestReconnectLeavesNothingBehind: a client gone and seen to, the next
// to attach does not mark it gone again, for good.
func TestReconnectLeavesNothingBehind(t *testing.T) {
	m := apiCore(t)
	m.Client = NoClient{}
	conn := func() *attach.Conn {
		a, b, err := attach.Pair()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { a.Close(""); b.Close("") })
		return a
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for range 3 {
		rc := m.AttachClient(conn(), func(RequestEv) {})
		clientGone{rc: rc}.Do(m)
	}
	m.AttachClient(conn(), func(RequestEv) {})
	if len(m.Views.gone) != 0 {
		t.Fatalf("kept among those gone: %v", m.Views.gone)
	}
}
