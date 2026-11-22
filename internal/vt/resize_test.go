package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestResizeKeepsSavedCursorOn: a cursor saved (DECSC) before the screen
// is made smaller is restored (DECRC) on it, not off it.
func TestResizeKeepsSavedCursorOn(t *testing.T) {
	term := newTestTerminal(t, 20, 10)
	term.Write([]byte("\x1b[9;18H\x1b7")) // to (17, 8), saved
	term.Write([]byte("\x1b[1;1H"))
	term.Resize(5, 3)
	term.Write([]byte("\x1b8"))
	if pos := term.CursorPosition(); pos != uv.Pos(4, 2) {
		t.Fatalf("restored cursor at %v, want (4, 2)", pos)
	}
	term.Write([]byte("X")) // must not write off the screen
}

// TestResizeKeepsOtherScreensCursorOn: the main screen's cursor, while the
// alternate one is shown, is kept on it when the screen is made smaller.
func TestResizeKeepsOtherScreensCursorOn(t *testing.T) {
	term := newTestTerminal(t, 20, 10)
	term.Write([]byte("\x1b[9;18H"))  // main screen's cursor at (17, 8)
	term.Write([]byte("\x1b[?1049h")) // alternate screen
	term.Resize(5, 3)
	for i := range term.scrs {
		for _, c := range []Cursor{term.scrs[i].cur, term.scrs[i].saved} {
			if c.X < 0 || c.X >= 5 || c.Y < 0 || c.Y >= 3 {
				t.Errorf("screen %d has a cursor at %v, off the 5x3 screen", i, c.Position)
			}
		}
	}
	term.Write([]byte("\x1b[?1049l"))
	if pos := term.CursorPosition(); pos.X >= 5 || pos.Y >= 3 {
		t.Fatalf("main screen's cursor at %v, off the 5x3 screen", pos)
	}
	term.Write([]byte("X"))
}
