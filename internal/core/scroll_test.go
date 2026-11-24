package core

import (
	"bytes"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// writtenProc keeps what is written to its program.
type writtenProc struct {
	spawn.Proc
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (p writtenProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.b.Write(b)
	p.mu.Unlock()
	return p.Proc.Write(b)
}

type writtenSpawner struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *writtenSpawner) Spawn(sp spawn.Spec) (spawn.Proc, error) {
	p, err := spawn.Local{}.Spawn(sp)
	if err != nil {
		return nil, err
	}
	return writtenProc{p, &s.mu, &s.b}, nil
}

// written is what has been written to the program so far.
func (s *writtenSpawner) written() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// wait waits for what has been written to the program to be want; what
// is written goes in order, by the pane's writer, a while after.
func (s *writtenSpawner) wait(t *testing.T, what, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.written() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s: sent %q, want %q", what, s.written(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// scrollCore is a core whose agent, t1, runs cat at 40×10, what is
// written to it kept.
func scrollCore(t *testing.T) (*Core, *term.Pane, *writtenSpawner) {
	t.Helper()
	sp := &writtenSpawner{}
	p, err := term.StartWith(sp, spawn.Spec{Argv: []string{"cat"}, Dir: t.TempDir(), Env: os.Environ(), PTY: true, W: 40, H: 10}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	tab := &Tab{}
	tab.Tab = state.Tab{ID: "t1", Name: "fix-login", Dir: "/w/fix-login", Agent: agents.Claude}
	tab.Pane, tab.Light = p, StIdle
	return &Core{Root: t.TempDir(), Tabs: []*Tab{tab}, Agents: []agents.Kind{agents.Claude}, AgentsAt: time.Now()}, p, sp
}

const (
	altOn   = "\x1b[?1049h"
	mouseOn = "\x1b[?1000h\x1b[?1006h" // presses and releases, in SGR
)

func TestAPIReadLookSaysWhetherItScrolls(t *testing.T) {
	m, p, _ := scrollCore(t)
	look := func(alt, wheel bool) {
		t.Helper()
		l, err := m.APIReadLook("t1", 0)
		if err != nil {
			t.Fatal(err)
		}
		if l.Alt != alt || l.Wheel != wheel || l.Cols != 40 || l.Rows != 10 {
			t.Fatalf("read %+v, want alt %v, wheel %v at 40×10", l, alt, wheel)
		}
	}
	look(false, false)
	p.Feed([]byte(mouseOn)) // the mouse, on the main screen: nerdr scrolls that
	look(false, false)
	p.Feed([]byte("\x1b[?1000l\x1b[?1006l" + altOn))
	look(true, false)
	p.Feed([]byte(mouseOn))
	look(true, true)
	p.Feed([]byte("\x1b[?1049l"))
	look(false, false)
}

func TestAPIScrollTurnsTheWheel(t *testing.T) {
	m, p, sp := scrollCore(t)
	p.Feed([]byte(altOn + mouseOn))
	up, down := "\x1b[<64;21;6M", "\x1b[<65;21;6M" // the middle of 40×10, 1-based
	if err := m.APIScroll("", "t1", 3); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(up, 3)
	sp.wait(t, "up 3", want)
	if err := m.APIScroll("", "t1", -2); err != nil {
		t.Fatal(err)
	}
	want += strings.Repeat(down, 2)
	sp.wait(t, "down 2", want)
	// 0 sends nothing: what comes next follows at once.
	if err := m.APIScroll("", "t1", 0); err != nil {
		t.Fatal(err)
	}
	// Brought within MaxScroll.
	if err := m.APIScroll("", "t1", -1000); err != nil {
		t.Fatal(err)
	}
	want += strings.Repeat(down, MaxScroll)
	sp.wait(t, "0, then down 1000", want)
	if err := m.APIScroll("", "t1", math.MinInt); err != nil {
		t.Fatal(err)
	}
	want += strings.Repeat(down, MaxScroll)
	sp.wait(t, "down the most an int says", want)
	if err := m.APIScroll("", "t1", math.MaxInt); err != nil {
		t.Fatal(err)
	}
	want += strings.Repeat(up, MaxScroll)
	sp.wait(t, "up the most an int says", want)
	if err := m.APIScroll("", "nope", 1); err == nil {
		t.Fatal("scrolled no program")
	}
}

func TestAPIScrollRefusedWhereItDoesNotScroll(t *testing.T) {
	m, p, sp := scrollCore(t)
	refused := func(what string) {
		t.Helper()
		err := m.APIScroll("api:1", "t1", 5)
		if err == nil || !strings.Contains(err.Error(), "does not scroll") {
			t.Fatalf("%s: %v", what, err)
		}
	}
	refused("the main screen")
	p.Feed([]byte(mouseOn))
	refused("the main screen, with the mouse")
	p.Feed([]byte("\x1b[?1000l" + altOn))
	refused("the alternate screen, no mouse")
	p.Feed([]byte(mouseOn))
	if err := m.APIScroll("api:1", "t1", 1); err != nil {
		t.Fatal(err)
	}
	// What is written goes in order: only this step went.
	sp.wait(t, "refused thrice, then a step", "\x1b[<64;21;6M")
}

// TestAPIScrollIsActivity: scrolling, as typing, takes the program's size
// back for the viewer that scrolls, before the wheel turns at its middle.
func TestAPIScrollIsActivity(t *testing.T) {
	m, p, sp := scrollCore(t)
	p.Feed([]byte(altOn + mouseOn))
	if _, _, err := m.APIView("api:1", "t1", 40, 20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.APIView("api:2", "t1", 100, 30); err != nil {
		t.Fatal(err)
	}
	if w, h := p.Size(); w != 100 || h != 30 {
		t.Fatalf("%d×%d, want the last viewer's 100×30", w, h)
	}
	// Refused: no size taken.
	p.Feed([]byte("\x1b[?1049l"))
	if err := m.APIScroll("api:1", "t1", 1); err == nil {
		t.Fatal("scrolled the main screen")
	}
	if w, h := p.Size(); w != 100 || h != 30 {
		t.Fatalf("%d×%d after a refused scroll, want 100×30", w, h)
	}
	p.Feed([]byte(altOn))
	if err := m.APIScroll("api:1", "t1", 1); err != nil {
		t.Fatal(err)
	}
	if w, h := p.Size(); w != 40 || h != 20 {
		t.Fatalf("%d×%d after scrolling, want the scroller's 40×20", w, h)
	}
	sp.wait(t, "a step up at the middle of 40×20", "\x1b[<64;21;11M")
}

// TestAPIScrollLegacyAtAnySize: in the legacy mouse encoding, which says
// no cell past 222, the wheel turns at any size a viewer takes, wider or
// narrower than the program was: the step goes as near the middle as it
// can say.
func TestAPIScrollLegacyAtAnySize(t *testing.T) {
	m, p, sp := scrollCore(t)
	p.Feed([]byte(altOn + "\x1b[?1000h")) // no SGR
	step := func(btn ansi.MouseButton, x, y int) string {
		return ansi.MouseX10(ansi.EncodeMouseButton(btn, false, false, false, false), x, y)
	}
	if _, _, err := m.APIView("api:1", "t1", 100, 30); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.APIView("api:2", "t1", 500, 40); err != nil {
		t.Fatal(err)
	}
	if w, _ := p.Size(); w != 500 {
		t.Fatalf("%d wide, want the last viewer's 500", w)
	}
	// From 500 to 100: the middle, 50.
	if err := m.APIScroll("api:1", "t1", 2); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(step(ansi.MouseWheelUp, 50, 15), 2)
	sp.wait(t, "up 2 at 100×30", want)
	// From 100 to 500: as near its middle, 250, as can be said.
	if err := m.APIScroll("api:2", "t1", -1); err != nil {
		t.Fatal(err)
	}
	if w, _ := p.Size(); w != 500 {
		t.Fatalf("%d wide after scrolling, want the scroller's 500", w)
	}
	want += step(ansi.MouseWheelDown, 222, 20)
	sp.wait(t, "down 1 at 500×40", want)
}

// TestAPIScrollSGRAtTheMiddle: in SGR, which says any cell, the step goes
// at the very middle of a wide screen, not where the legacy encoding
// would have to stop.
func TestAPIScrollSGRAtTheMiddle(t *testing.T) {
	m, p, sp := scrollCore(t)
	p.Feed([]byte(altOn + mouseOn))
	if _, _, err := m.APIView("api:1", "t1", 500, 40); err != nil {
		t.Fatal(err)
	}
	if err := m.APIScroll("api:1", "t1", 1); err != nil {
		t.Fatal(err)
	}
	sp.wait(t, "up 1 at 500×40", "\x1b[<64;251;21M")
}
