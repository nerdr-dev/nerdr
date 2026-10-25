package term

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// waitScreen waits for the pane's screen to contain s.
func waitScreen(t *testing.T, p *Pane, s string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sc := p.Snapshot().Screen; strings.Contains(sc, s) {
			return sc
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("screen never showed %q:\n%s", s, p.Snapshot().Screen)
	return ""
}

func TestMouseReachesOnlyAProgramThatAskedForIt(t *testing.T) {
	// The program turns on presses and releases, SGR encoded, then shows
	// what it reads.
	p, err := Start([]string{"sh", "-c", `stty -echo; printf 'ready\033[?1000h\033[?1006h\n'; od -c`}, t.TempDir(), os.Environ(), 60, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitScreen(t, p, "ready")
	if !p.WantsMouse() || p.WantsAllMotion() {
		t.Fatalf("wants mouse %v, all motion %v", p.WantsMouse(), p.WantsAllMotion())
	}
	// Motion is not what mode 1000 asked for: it is dropped.
	p.SendMouse(MouseEvent{Kind: MouseMotion, Button: ansi.MouseLeft, X: 1, Y: 1})
	p.SendMouse(MouseEvent{Kind: MousePress, Button: ansi.MouseLeft, X: 4, Y: 2})
	p.SendMouse(MouseEvent{Kind: MouseRelease, Button: ansi.MouseLeft, X: 4, Y: 2})
	p.Write([]byte("\x04\x04")) // end the line, then the input
	sc := waitScreen(t, p, "0000022")
	var flat string
	for _, f := range strings.Fields(sc) {
		if len(f) != 7 || strings.Trim(f, "0123456789") != "" { // not od's offsets
			flat += f
		}
	}
	if !strings.Contains(flat, "033[<0;5;3M033[<0;5;3m") {
		t.Fatalf("program read:\n%s", sc)
	}
	if strings.Contains(flat, "<32;") {
		t.Fatalf("motion got through:\n%s", sc)
	}
}

func TestNoMouseForAProgramThatDidNotAsk(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `echo ready; sleep 5`}, t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitScreen(t, p, "ready")
	if p.SendMouse(MouseEvent{Kind: MousePress, Button: ansi.MouseLeft}) {
		t.Fatal("SendMouse says the program wants the mouse")
	}
}

func TestSelectedText(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf 'first line\nsecond line\nthird\n'; sleep 5`}, t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitScreen(t, p, "third")
	// Backwards, from the "o" of "second" to the "l" of "line" above it.
	p.Select(&Selection{From: p.PointAt(3, 1, 0), To: p.PointAt(6, 0, 0)})
	if got := p.SelectedText(); got != "line\nseco" {
		t.Fatalf("selected %q", got)
	}
	if lines := p.Render(0, false); !strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("selection not drawn: %q", lines[0])
	}
}
