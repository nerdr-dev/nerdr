package term

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Every test draws a pane anew where Render gives its last drawing
// again, and fails if the two differ; see checkReuse.
func init() { checkReuse = true }

// Render gives its last drawing again only while nothing it draws has
// changed: output, a selection, a resize, another scroll or cursor each
// draw anew. checkReuse fails the test if a drawing given again is stale.
func TestRenderReusedUntilChanged(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf 'one\n'; read x; printf 'two\n'; sleep 5`}, t.TempDir(), os.Environ(), 20, 4, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitFor := func(s string) []string {
		t.Helper()
		for range 100 {
			if l := p.Render(0, true); strings.Contains(strings.Join(l, "\n"), s) {
				return l
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%q never drawn", s)
		return nil
	}
	first := waitFor("one")
	first[0] = "scribbled on by the caller"
	if again := p.Render(0, true); again[0] == first[0] {
		t.Fatal("a caller's change to the lines reached the pane's drawing")
	}
	p.Input([]byte("\n"))
	waitFor("two")
	p.Select(&Selection{From: Point{Line: 0, Col: 0}, To: Point{Line: 0, Col: 2}})
	if l := p.Render(0, true); !strings.Contains(l[0], "\x1b[") {
		t.Fatalf("selection not drawn: %q", l[0])
	}
	p.Select(nil)
	p.Render(0, false)
	p.Resize(30, 6)
	if l := p.Render(0, false); len(l) != 6 {
		t.Fatalf("resize not drawn: %d lines", len(l))
	}
}
