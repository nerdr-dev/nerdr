package core

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// echoer is a program that writes 40 lines, then what it reads back.
func echoer(t *testing.T) *term.Pane {
	t.Helper()
	p, err := term.Start([]string{"sh", "-c", `stty -echo; i=0; while [ $i -lt 40 ]; do echo "line $i"; i=$((i+1)); done; while read l; do echo "got $l"; done`},
		t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// screensOver is core c serving its screens to a client over a pair of
// connections, the loop's ticks played by the test.
func screensOver(t *testing.T, c *Core) *ScreenClient {
	t.Helper()
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(""); b.Close("") })
	c.Screens = NewScreenServer(a, func(r Request) {
		c.Mu.Lock()
		r.Do(c)
		c.Mu.Unlock()
	})
	stop := make(chan struct{})
	done := make(chan struct{})
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
	return NewScreenClient(b, func() {})
}

// draw draws p scroll lines back as the TUI does, and tells the core.
func draw(cl *ScreenClient, p Program, scroll int) term.Frame {
	term.NextFrame()
	f := p.Frame(scroll)
	cl.Shown()
	return f
}

// until waits for ok, drawing p scroll back meanwhile.
func until(t *testing.T, what string, cl *ScreenClient, p Program, scroll int, ok func(term.Frame) bool) term.Frame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f := draw(cl, p, scroll)
		if ok(f) {
			return f
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: never came; shown:\n%s", what, strings.Join(textOf(f), "\n"))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func textOf(f term.Frame) []string {
	var out []string
	for _, row := range f.Rows {
		var b strings.Builder
		for _, c := range row {
			b.WriteString(c.Text)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func shows(f term.Frame, s string) bool { return strings.Contains(strings.Join(textOf(f), "\n"), s) }

// TestScreensOverAConnection: a client in another process sees the screen
// of a program core runs, types into it, scrolls it back and reads lines
// beyond what it shows, through its copy; a program started again in the
// pane is sent afresh.
func TestScreensOverAConnection(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	tb.Agent = agents.Codex
	c := &Core{Tabs: []*Tab{tb}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
	cl := screensOver(t, c)
	cp := cl.Program(ref)

	until(t, "the program's lines", cl, cp, 0, func(f term.Frame) bool { return shows(f, "line 39") })
	cp.Input([]byte("hello there\r"))
	until(t, "what was typed", cl, cp, 0, func(f term.Frame) bool { return shows(f, "got hello there") })
	c.Mu.Lock()
	if tb.Typed == nil || tb.Typed.Take() != "hello there" {
		t.Error("what was typed to the agent was not followed in core")
	}
	c.Mu.Unlock()

	want := p.Frame(5)
	f := until(t, "scrolled back", cl, cp, 5, func(f term.Frame) bool { return f.Top == want.Top })
	if strings.Join(textOf(f), "\n") != strings.Join(textOf(want), "\n") {
		t.Fatalf("scrolled back:\n%s\nwant\n%s", strings.Join(textOf(f), "\n"), strings.Join(textOf(want), "\n"))
	}
	first, rows := cp.Rows(0, 2)
	if first != 0 || len(rows) != 3 || textOf(term.Frame{Rows: rows})[1] != "line 1" {
		t.Fatalf("lines asked for: %d %q", first, textOf(term.Frame{Rows: rows}))
	}

	cp.Resize(30, 6)
	until(t, "resized", cl, cp, 0, func(f term.Frame) bool { return f.W == 30 && f.H == 6 })

	again, err := term.Start([]string{"sh", "-c", "echo started again; sleep 30"}, t.TempDir(), os.Environ(), 30, 6, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(again.Close)
	c.Mu.Lock()
	tb.Pane = again
	c.Mu.Unlock()
	until(t, "the program started again", cl, cp, 0, func(f term.Frame) bool {
		return shows(f, "started again") && !shows(f, "line")
	})
}

// TestScreensSentOnlyWhenShown: a screen not drawn is not sent, nor
// watched; one drawn again is.
func TestScreensSentOnlyWhenShown(t *testing.T) {
	p := echoer(t)
	tb := &Tab{Pane: p}
	c := &Core{Tabs: []*Tab{tb}}
	ref := ProgramRef{Pane: c.PaneRef(tb.OwnPane())}
	cl := screensOver(t, c)
	cp := cl.Program(ref)
	until(t, "the program's lines", cl, cp, 0, func(f term.Frame) bool { return shows(f, "line 39") })

	term.NextFrame()
	cl.Shown() // nothing drawn
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.Screens.mu.Lock()
		n := len(c.Screens.shown)
		c.Screens.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a screen not shown is still sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	gen := cp.DrawGen()
	p.Input([]byte("unseen\r"))
	time.Sleep(100 * time.Millisecond)
	if cp.DrawGen() != gen {
		t.Fatal("a screen not shown was sent")
	}
	until(t, "drawn again", cl, cp, 0, func(f term.Frame) bool { return shows(f, "got unseen") })
}
