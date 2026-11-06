package term

import (
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// frame counts the screens nerdr has drawn; see NextFrame.
var frame atomic.Uint64

// NextFrame starts drawing a new screen. A pane drawn on the last one
// wakes the UI when its program draws again; one left off it, a tab out
// of sight, does not, so a busy agent there does not redraw the screen.
// Its exit still does.
func NextFrame() { frame.Add(1) }

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
	s.mu.Lock()
	defer s.mu.Unlock()
	w, h := s.emu.Width(), s.emu.Height()
	sb := s.emu.ScrollbackLen()
	if s.emu.IsAltScreen() {
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
	return strings.Join(lines, "\n")
}
