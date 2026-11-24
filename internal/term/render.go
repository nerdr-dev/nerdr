package term

import (
	"bytes"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// frame counts the screens nerdr has drawn; see NextFrame.
var frame atomic.Uint64

// NextFrame starts drawing a new screen. A pane drawn on the last one
// wakes the UI when its program draws again; one left off it, a tab out
// of sight, does not, so a busy agent there does not redraw the screen.
// Its exit still does.
func NextFrame() { frame.Add(1) }

// drawnElsewhere is set in a process that draws no screen itself; see
// DrawnElsewhere.
var drawnElsewhere atomic.Bool

// DrawnElsewhere says this process draws no screen itself: a core of its
// own, whose client draws them in another process. No frame is ever
// started here, so no pane counts as drawn on the last one: only a pane
// a client is sent (Watch) wakes the process when its program draws, and a
// busy agent out of the client's sight does not.
func DrawnElsewhere() { drawnElsewhere.Store(true) }

// ScrollbackLen is how many lines there are to scroll back through.
func (s *Screen) ScrollbackLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emu.IsAltScreen() {
		return 0
	}
	return s.emu.ScrollbackLen()
}

// Close stops the program and releases the emulator.
//
// The emulator's Close is not safe to call while replyLoop is inside its
// Read (both touch an unguarded flag), so the reply pipe is closed first:
// that ends replyLoop, and only then is the emulator closed.
func (p *Pane) Close() {
	p.Stop(2 * time.Second)
	// The terminal let go of first, so the reader stops feeding the
	// screen before the emulator is closed under it.
	if p.proc != nil {
		_ = p.proc.Close()
		select {
		case <-p.readDone:
		case <-time.After(time.Second):
		}
	}
	p.CloseReplies()
	select {
	case <-p.replyDone:
	case <-time.After(2 * time.Second):
		return // better to leak the emulator than to race on it
	}
	p.Screen.Close()
}

// cellText is what is drawn for a cell of content, width columns wide. A
// character of several code points — an emoji with a skin tone, a family
// joined by zero-width joiners, one with a variation selector — is one
// cell to the emulator, but a terminal that draws it code point by code
// point makes it wider or narrower: the row then wraps, or its right edge
// moves in. Such a character is drawn as its first code point, padded to
// the cell's width; the others are drawn as they are.
func cellText(content string, width int) string {
	if len(content) <= 1 {
		return content
	}
	w := 0
	for _, r := range content {
		w += wcwidth.RuneWidth(r)
	}
	if w == width {
		return content
	}
	r, _ := utf8.DecodeRuneInString(content)
	rw := wcwidth.RuneWidth(r)
	if rw == 0 || rw > width {
		return strings.Repeat(" ", width)
	}
	return string(r) + strings.Repeat(" ", width-rw)
}

// wcwidth is how wide a terminal that draws code point by code point
// makes each one, with East Asian ambiguous characters narrow, as on most
// terminals.
var wcwidth = func() *runewidth.Condition {
	c := runewidth.NewCondition()
	c.EastAsianWidth = false
	return c
}()

// Text is the last n lines of what the program shows, its scrollback
// first, as plain text, the blank lines at the end left out.
func (s *Screen) Text(n int) string {
	text, _, _ := s.TextSized(n)
	return text
}

// TextSized is Text, and the size of the screen it is read from, in one
// look: the columns the lines are laid out for, and the rows.
func (s *Screen) TextSized(n int) (string, int, int) {
	l := s.TextLook(n)
	return l.Text, l.Cols, l.Rows
}

// Look is what a program shows at one moment, as TextLook reads it.
type Look struct {
	// Text is the last lines shown, as Text has them.
	Text string
	// Cols and Rows are the size of the screen the lines are laid out for.
	Cols, Rows int
	// Alt is the program being on the alternate screen, which keeps no
	// scrollback: what scrolled off it only the program has.
	Alt bool
	// Wheel is the program on the alternate screen and told of the wheel
	// (see wheelReport), so it scrolls its own history when turned.
	Wheel bool
}

// wheelEvent is the wheel turned a step, up (to older text) or down, in
// the middle of a screen of w×h.
func wheelEvent(up bool, w, h int) MouseEvent {
	btn := ansi.MouseWheelDown
	if up {
		btn = ansi.MouseWheelUp
	}
	return MouseEvent{Kind: MousePress, Button: btn, X: w / 2, Y: h / 2}
}

// maxLegacy is the furthest cell the legacy mouse encoding can say.
const maxLegacy = 222

// wheelReport is what tells the program of a step of the wheel, up or
// down, if it is on the alternate screen and told of the wheel; nil when
// not. Screen.mu is held.
func (s *Screen) wheelReport(up bool) []byte {
	if s.exited || !s.emu.IsAltScreen() {
		return nil
	}
	ev := wheelEvent(up, s.emu.Width(), s.emu.Height())
	if !s.mouseSgr {
		// As near the middle as the legacy encoding can say: the step
		// goes at any size.
		ev.X, ev.Y = min(ev.X, maxLegacy), min(ev.Y, maxLegacy)
	}
	return mouseReport(ev, s.mouseLevel(), s.mouseSgr)
}

// Scrolls is TextLook's Alt and Wheel, without the text.
func (s *Screen) Scrolls() (alt, wheel bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.IsAltScreen(), s.wheelReport(true) != nil
}

// Wheel turns the wheel n steps, up or down, over the program, if it is
// on the alternate screen and told of the wheel, as TextLook's Wheel says;
// it reports whether it was, and sends nothing when not. What it is, and
// the steps going to the program, are one look: the program cannot leave
// the screen or let the mouse go between.
func (p *Pane) Wheel(up bool, n int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.wheelReport(up)
	if r == nil {
		return false
	}
	p.queue(bytes.Repeat(r, max(n, 0)), -1) // queue does not take mu
	return true
}

// TextLook is Text, the size of the screen, and whether the program
// scrolls itself, all in one look.
func (s *Screen) TextLook(n int) Look {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, h := s.emu.Width(), s.emu.Height()
	alt := s.emu.IsAltScreen()
	wheel := s.wheelReport(true) != nil
	sb := s.emu.ScrollbackLen()
	if alt {
		sb = 0
	}
	var lines []string
	var b strings.Builder
	for idx := 0; idx < sb+h; idx++ {
		b.Reset()
		for x := 0; x < w; x++ {
			c := s.cellAt(x, idx, sb)
			switch {
			case c == nil || c.Content == "" && c.Width != 0:
				b.WriteByte(' ')
			case c.Width == 0:
				b.WriteString(c.Content) // a combining mark, or nothing
			default:
				b.WriteString(c.Content)
				x += max(c.Width, 1) - 1
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return Look{Text: strings.Join(lines, "\n"), Cols: w, Rows: h, Alt: alt, Wheel: wheel}
}
