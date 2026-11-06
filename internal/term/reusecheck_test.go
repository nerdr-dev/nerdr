package term

import (
	"os"
	"strings"
	"testing"
	"time"
)

// DrawGen changes with whatever changes what a frame shows, output, a resize
// and the exit, and with nothing else: a client draws a frame again from its
// last drawing while it is the same.
func TestDrawGenCountsWhatIsShown(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf 'one\n'; read x; printf 'two\n'; read y`}, t.TempDir(), os.Environ(), 20, 4, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitFor := func(s string) Frame {
		t.Helper()
		for range 100 {
			if f := p.Frame(0); strings.Contains(strings.Join(Draw(f, true, nil), "\n"), s) {
				return f
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%q never drawn", s)
		return Frame{}
	}
	first := waitFor("one")
	time.Sleep(100 * time.Millisecond) // the shell's echo of nothing, if any
	first = p.Frame(0)
	if again := p.Frame(1); again.Gen != first.Gen || p.DrawGen() != first.Gen {
		t.Fatal("taking a frame, or one scrolled, changed what is shown")
	}
	p.Input([]byte("\n"))
	if f := waitFor("two"); f.Gen == first.Gen {
		t.Fatal("output did not count")
	}
	before := p.DrawGen()
	p.Resize(30, 6)
	if f := p.Frame(0); f.Gen == before || len(Draw(f, false, nil)) != 6 {
		t.Fatalf("resize not counted or drawn: gen %d, %d lines", f.Gen, len(Draw(f, false, nil)))
	}
	before = p.DrawGen()
	p.Input([]byte("\n"))
	<-p.Done()
	if p.DrawGen() == before || p.Frame(0).Cursor {
		t.Fatal("the exit did not count, or the cursor is still shown")
	}
}
