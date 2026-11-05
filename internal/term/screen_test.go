package term

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// screenOf is a screen given what a program wrote, cut up as given, and
// a function for the next of its replies.
func screenOf(t *testing.T, chunks ...string) (*Screen, func() string) {
	t.Helper()
	s := NewScreen(20, 4)
	replies := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 256)
		for {
			n, err := s.ReadReply(buf)
			if n > 0 {
				replies <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		s.CloseReplies()
		<-done
		s.Close()
	})
	for _, c := range chunks {
		s.Feed([]byte(c))
	}
	return s, func() string {
		select {
		case r := <-replies:
			return r
		case <-time.After(2 * time.Second):
			return ""
		}
	}
}

// TestScreenNeedsNoProgram: everything a tab reads of a screen comes from
// the bytes alone, with no program or pseudo-terminal behind them.
func TestScreenNeedsNoProgram(t *testing.T) {
	s, _ := screenOf(t,
		"\x1b]0;my title\x07",
		"\x1b]9;4;1;50\x07",
		"\x1b]52;c;aGVsbG8=\x07",
		"\x1b[31mred\x1b[0m plain\r\nnext",
		"\x1b[?1h\x1b[?2004h\x1b[?1004h",
	)
	if s.Title() != "my title" {
		t.Errorf("title %q", s.Title())
	}
	in := s.Snapshot()
	if in.Progress != "4;1;50" || !strings.Contains(in.Screen, "red plain\nnext") {
		t.Errorf("snapshot %+v", in)
	}
	if c := s.TakeCopied(); c != "hello" || s.TakeCopied() != "" {
		t.Errorf("copied %q", c)
	}
	if !s.appCursor || !s.bracketedPaste || !s.focusEvents {
		t.Errorf("modes: cursor %v, paste %v, focus %v", s.appCursor, s.bracketedPaste, s.focusEvents)
	}
	lines := s.Render(0, false)
	if !strings.Contains(lines[0], "red") || lines[0] == ansi.Strip(lines[0]) {
		t.Errorf("the first line is not drawn in colour: %q", lines[0])
	}
	if w, h := s.Size(); w != 20 || h != 4 {
		t.Errorf("size %d×%d", w, h)
	}
}

// TestScreenAnswersQueries: what a program asks its terminal is answered
// by the screen, for whoever carries it back to the program.
func TestScreenAnswersQueries(t *testing.T) {
	_, reply := screenOf(t, "ab\x1b[6n")
	if r := reply(); r != "\x1b[1;3R" {
		t.Fatalf("cursor position reply %q", r)
	}
}

// TestScreenIsTheSameHoweverOutputIsCut: the pieces a read happens to
// return must not matter, whatever they cut through: an escape sequence,
// an OSC, a character.
func TestScreenIsTheSameHoweverOutputIsCut(t *testing.T) {
	out := "\x1b]0;a title\x07\x1b[1;32mgrønt\x1b[0m\r\nline two\r\n\x1b[?1049hfull\x1b[?1049lback"
	whole, _ := screenOf(t, out)
	var bytes []string
	for i := range len(out) {
		bytes = append(bytes, out[i:i+1])
	}
	cut, _ := screenOf(t, bytes...)
	if a, b := whole.Render(0, true), cut.Render(0, true); !slices.Equal(a, b) {
		t.Errorf("whole:\n%q\ncut:\n%q", a, b)
	}
	if a, b := whole.Title(), cut.Title(); a != b {
		t.Errorf("title %q and %q", a, b)
	}
}

// TestScreenKeepsScrollbackAndAltScreen: what has scrolled off is kept
// and can be read, and the alternate screen has none, as in a terminal.
func TestScreenKeepsScrollbackAndAltScreen(t *testing.T) {
	var out strings.Builder
	for i := range 10 {
		out.WriteString("line " + string(rune('0'+i)) + "\r\n")
	}
	s, _ := screenOf(t, out.String())
	if n := s.ScrollbackLen(); n == 0 {
		t.Fatal("nothing in the scrollback")
	}
	if text := s.Text(0); !strings.Contains(text, "line 0") || !strings.Contains(text, "line 9") {
		t.Errorf("text %q", text)
	}
	s.Feed([]byte("\x1b[?1049h"))
	if !s.AltScreen() || s.ScrollbackLen() != 0 {
		t.Errorf("on the alternate screen: alt %v, scrollback %d", s.AltScreen(), s.ScrollbackLen())
	}
	s.Feed([]byte("\x1b[?1049l"))
	if s.AltScreen() || !strings.Contains(s.Text(0), "line 0") {
		t.Errorf("back: alt %v, text %q", s.AltScreen(), s.Text(0))
	}
}

// TestScreenResize: the snapshot follows a resize, and a resize to the
// same size changes nothing.
func TestScreenResize(t *testing.T) {
	s, _ := screenOf(t, "x")
	_, gen := s.SnapshotGen()
	s.Resize(20, 4)
	if _, g := s.SnapshotGen(); g != gen {
		t.Error("the same size counted as a change")
	}
	s.Resize(30, 6)
	if w, h := s.Size(); w != 30 || h != 6 {
		t.Errorf("size %d×%d", w, h)
	}
	if _, g := s.SnapshotGen(); g == gen {
		t.Error("a resize not counted as a change")
	}
	s.Resize(1, 1)
	if w, h := s.Size(); w != 10 || h != 3 {
		t.Errorf("below the smallest: %d×%d", w, h)
	}
}
