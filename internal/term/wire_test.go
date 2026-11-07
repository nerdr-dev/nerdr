package term

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// noOut is a CopyOut that drops what is sent, and counts the scrolls asked
// for.
type noOut struct{ scrolls []int }

func (*noOut) Input([]byte)                                  {}
func (*noOut) Write([]byte)                                  {}
func (*noOut) Mouse(MouseEvent)                              {}
func (*noOut) Focus(bool)                                    {}
func (*noOut) Resize(int, int)                               {}
func (o *noOut) Scroll(n int)                                { o.scrolls = append(o.scrolls, n) }
func (*noOut) AskRows(from, _ int, done func(int, [][]Cell)) { done(from, nil) }

// sendTo has snd make the next message of s at scroll, and c take it as
// it comes out of JSON; it is the message's size.
func sendTo(t *testing.T, s *Screen, snd *RowsSender, c *Copy, scroll int) int {
	t.Helper()
	v := snd.View()
	s.FrameInto(&v.Frame, scroll)
	v.Mouse, v.Scroll = -1, scroll
	b, err := snd.Next()
	if err != nil {
		t.Fatal(err)
	}
	var msg ScreenMsg
	if err := json.Unmarshal(b, &msg); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(&msg); err != nil {
		t.Fatalf("apply: %v\n%s", err, b)
	}
	return len(b)
}

// sameAs fails unless c shows what s does at scroll, cell for cell and as
// drawn.
func sameAs(t *testing.T, what string, s *Screen, c *Copy, scroll int) {
	t.Helper()
	want, got := s.Frame(scroll), c.Frame(scroll)
	if want.W != got.W || want.H != got.H || want.Top != got.Top || want.Scrollback != got.Scrollback ||
		want.CursorX != got.CursorX || want.CursorY != got.CursorY || want.Cursor != got.Cursor ||
		want.Alt != got.Alt || want.Title != got.Title {
		t.Fatalf("%s: frame differs:\nwant %+v\ngot  %+v", what, head(want), head(got))
	}
	for y := range want.Rows {
		if !sameRow(want.Rows[y], got.Rows[y]) {
			t.Fatalf("%s: row %d differs:\nwant %q\ngot  %q", what, y, ansi.Strip(Draw(want, false, nil)[y]), ansi.Strip(Draw(got, false, nil)[y]))
		}
	}
	w, g := Draw(want, true, nil), Draw(got, true, nil)
	if strings.Join(w, "\n") != strings.Join(g, "\n") {
		t.Fatalf("%s: drawn differently:\nwant %q\ngot  %q", what, w, g)
	}
}

func head(f Frame) Frame { f.Rows = nil; return f }

func TestCopyIsTheScreen(t *testing.T) {
	s, _ := screenOf(t)
	s.Resize(30, 6)
	c := NewCopy(30, 6, &noOut{})
	var snd RowsSender
	step := func(what, out string) int {
		t.Helper()
		s.Feed([]byte(out))
		n := sendTo(t, s, &snd, c, 0)
		sameAs(t, what, s, c, 0)
		return n
	}
	step("empty", "")
	step("text", "hello \x1b[1;31mbold red\x1b[0m and \x1b[38;5;200mindexed\x1b[0m")
	step("true colour", "\r\n\x1b[38;2;10;20;30;48;2;200;100;50mrgb\x1b[0m \x1b[4:3;58;2;1;2;3mcurly\x1b[0m")
	step("wide and combining", "\r\n日本語 é 🙂 x")
	step("title", "\x1b]0;my title\x07")
	step("cursor hidden", "\x1b[?25l")
	step("cursor shown", "\x1b[?25h")
	for i := range 20 {
		step(fmt.Sprintf("scrolling %d", i), fmt.Sprintf("\r\nline %d \x1b[32mgreen\x1b[0m", i))
	}
	step("alt screen", "\x1b[?1049h\x1b[2;3Hin alt")
	step("alt cleared", "\x1b[2J\x1b[H\x1b[7mreverse\x1b[0m")
	step("main again", "\x1b[?1049l")
	s.Resize(40, 8)
	step("resized", "")
	s.Resize(25, 4)
	step("smaller", "after")
	step("cleared", "\x1b[2J\x1b[3J\x1b[H")
}

// TestCopyScrolledBack: a frame scroll lines back is the screen's, and
// the copy asks for the scroll once.
func TestCopyScrolledBack(t *testing.T) {
	s, _ := screenOf(t)
	for i := range 30 {
		s.Feed(fmt.Appendf(nil, "line %d\r\n", i))
	}
	out := &noOut{}
	c := NewCopy(20, 4, out)
	var snd RowsSender
	sendTo(t, s, &snd, c, 0)
	sameAs(t, "bottom", s, c, 0)
	_ = c.Frame(5)
	_ = c.Frame(5)
	if len(out.scrolls) != 1 || out.scrolls[0] != 5 {
		t.Fatalf("scrolls asked for: %v, want [5]", out.scrolls)
	}
	sendTo(t, s, &snd, c, 5)
	sameAs(t, "5 back", s, c, 5)
	if got, want := c.TopAt(5), s.TopAt(5); got != want {
		t.Fatalf("TopAt(5) = %d, want %d", got, want)
	}
	first, rows := c.Rows(c.TopAt(5), c.TopAt(5)+1)
	wf, wrows := s.Rows(s.TopAt(5), s.TopAt(5)+1)
	if first != wf || len(rows) != 2 || !sameRow(rows[0], wrows[0]) || !sameRow(rows[1], wrows[1]) {
		t.Fatalf("rows of the copy differ from the screen's")
	}
}

// TestRowsSentAreTheChanged: once the first message has every row, one
// sends only the rows that changed, and output that scrolls moves the rows
// kept rather than sending them again.
func TestRowsSentAreTheChanged(t *testing.T) {
	s := NewScreen(80, 24)
	c := NewCopy(80, 24, &noOut{})
	var snd RowsSender
	for i := range 24 {
		s.Feed(fmt.Appendf(nil, "\x1b[3%dmline %d %s\x1b[0m\r\n", i%8, i, strings.Repeat("w", 40)))
	}
	first := sendTo(t, s, &snd, c, 0)
	s.Feed([]byte("x"))
	one := sendTo(t, s, &snd, c, 0)
	sameAs(t, "one more", s, c, 0)
	if one*10 > first {
		t.Fatalf("a character typed sent %d bytes, the whole screen %d", one, first)
	}
	s.Feed([]byte("\r\nnew line\r\n"))
	scrolled := sendTo(t, s, &snd, c, 0)
	sameAs(t, "scrolled", s, c, 0)
	if scrolled*4 > first {
		t.Fatalf("a screen scrolled two lines sent %d bytes, the whole screen %d", scrolled, first)
	}
	unchanged := sendTo(t, s, &snd, c, 0)
	if unchanged*10 > first {
		t.Fatalf("an unchanged screen sent %d bytes", unchanged)
	}
	snd.Reset()
	if again := sendTo(t, s, &snd, c, 0); again*2 < first {
		t.Fatalf("after Reset %d bytes were sent, the whole screen is %d", again, first)
	}
	sameAs(t, "after reset", s, c, 0)
}

// TestCopyScrollbackFull: when the scrollback is full the top stays where
// it is, and rows are still moved rather than sent again.
func TestCopyScrollbackFull(t *testing.T) {
	s := NewScreen(40, 10)
	s.emu.SetScrollbackSize(20)
	c := NewCopy(40, 10, &noOut{})
	var snd RowsSender
	for i := range 40 {
		s.Feed(fmt.Appendf(nil, "line %d %s\r\n", i, strings.Repeat("z", 30)))
	}
	first := sendTo(t, s, &snd, c, 0)
	s.Feed([]byte("next\r\n"))
	n := sendTo(t, s, &snd, c, 0)
	sameAs(t, "full scrollback", s, c, 0)
	if n*3 > first {
		t.Fatalf("one line more sent %d bytes, the whole screen %d", n, first)
	}
}

// TestCopyRefusesBadRows: a message that does not fit is an error, never
// a panic, and leaves a screen to draw.
func TestCopyRefusesBadRows(t *testing.T) {
	c := NewCopy(10, 3, &noOut{})
	for _, msg := range []ScreenMsg{
		{W: 0, H: 3},
		{W: 1 << 20, H: 1 << 20},
		{W: 10, H: 3, Rows: []WireRow{{Y: 5}}},
		{W: 11, H: 3},
		{W: 10, H: 3, Full: true, Rows: []WireRow{{Y: 0, Runs: []WireRun{{T: strings.Repeat("x", 11)}}}}},
		{W: 10, H: 3, Full: true, Rows: []WireRow{{Y: 0, Runs: []WireRun{{S: 3, T: "x"}}}}},
		{W: 10, H: 3, Full: true, Styles: []WireStyle{{Fg: "q"}}},
		{W: 10, H: 3, Full: true, Rows: []WireRow{{Y: 0, Runs: []WireRun{{C: []string{"a", "b"}, W: []int{1}}}}}},
	} {
		if err := c.Apply(&msg); err == nil {
			t.Errorf("%+v taken", msg)
		}
		_ = Draw(c.Frame(0), true, nil)
	}
}

func TestCopyKeepsExitAndMouse(t *testing.T) {
	c := NewCopy(10, 3, &noOut{})
	if c.WantsMouse() || c.SendMouse(MouseEvent{}) {
		t.Fatal("an empty copy takes the mouse")
	}
	msg := ScreenMsg{W: 10, H: 3, Full: true, Mouse: 3, Exited: true, ExitErr: "exit status 2", ExitAfter: 1500, Busy: true, Copied: "clip"}
	if err := c.Apply(&msg); err != nil {
		t.Fatal(err)
	}
	if !c.WantsAllMotion() || !c.SendMouse(MouseEvent{}) {
		t.Fatal("mouse mode lost")
	}
	ex, err, after := c.Exited()
	if !ex || err == nil || err.Error() != "exit status 2" || after < 1500*time.Millisecond {
		t.Fatalf("Exited = %v %v %v", ex, err, after)
	}
	if !c.Busy() || c.TakeCopied() != "clip" || c.TakeCopied() != "" {
		t.Fatal("busy or copied lost")
	}
}

// BenchmarkRowsBusy is BenchmarkDrawBusy through a copy: the message of a
// changed row made, sent as JSON, taken, and drawn.
func BenchmarkRowsBusy(b *testing.B) {
	s := NewScreen(200, 50)
	var sb strings.Builder
	for i := range 80 {
		fmt.Fprintf(&sb, "\x1b[3%dmline %d \x1b[1msome bold\x1b[0m text that fills the row with words and more words %s\r\n", i%8, i, strings.Repeat("x", 90))
	}
	s.Feed([]byte(sb.String()))
	c := NewCopy(200, 50, &noOut{})
	var snd RowsSender
	var f Frame
	var bytes int
	for b.Loop() {
		s.Feed([]byte("y"))
		v := snd.View()
		s.FrameInto(&v.Frame, 0)
		body, _ := snd.Next()
		bytes += len(body)
		var msg ScreenMsg
		_ = json.Unmarshal(body, &msg)
		_ = c.Apply(&msg)
		c.FrameInto(&f, 0)
		_ = Draw(f, true, nil)
	}
	b.ReportMetric(float64(bytes)/float64(b.N), "bytes/msg")
}

// BenchmarkRowsFull is the first message of a busy 200×50 screen.
func BenchmarkRowsFull(b *testing.B) {
	s := NewScreen(200, 50)
	var sb strings.Builder
	for i := range 80 {
		fmt.Fprintf(&sb, "\x1b[3%dmline %d \x1b[1msome bold\x1b[0m text that fills the row with words and more words %s\r\n", i%8, i, strings.Repeat("x", 90))
	}
	s.Feed([]byte(sb.String()))
	c := NewCopy(200, 50, &noOut{})
	var snd RowsSender
	var bytes int
	for b.Loop() {
		snd.Reset()
		v := snd.View()
		s.FrameInto(&v.Frame, 0)
		body, _ := snd.Next()
		bytes += len(body)
		var msg ScreenMsg
		_ = json.Unmarshal(body, &msg)
		_ = c.Apply(&msg)
	}
	b.ReportMetric(float64(bytes)/float64(b.N), "bytes/msg")
}
