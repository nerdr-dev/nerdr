package term

import (
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// checkReuse, set by NERDR_CHECK_REUSE and in the tests, draws a pane
// anew each time Render would give its last drawing again, and panics if
// the two differ: a change that did not set dirty.
var checkReuse = os.Getenv("NERDR_CHECK_REUSE") != ""

// frame counts the screens nerdr has drawn; see NextFrame.
var frame atomic.Uint64

// NextFrame starts drawing a new screen. A pane drawn on the last one
// wakes the UI when its program draws again; one left off it, a tab out
// of sight, does not, so a busy agent there does not redraw the screen.
// Its exit still does.
func NextFrame() { frame.Add(1) }

// Render draws the screen as w×h lines of styled text. scroll > 0 shows
// that many lines back into the scrollback. The cursor is drawn as a
// reverse-video cell when cursor is true and the program has it visible:
// the real terminal cursor belongs to nerdr's own UI.
func (p *Pane) Render(scroll int, cursor bool) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drawnOn.Store(frame.Load())
	if !p.dirty && p.drawn.lines != nil && p.drawn.scroll == scroll && p.drawn.cursor == cursor {
		if checkReuse {
			if now := p.render(scroll, cursor); strings.Join(now, "\n") != strings.Join(p.drawn.lines, "\n") {
				panic("pane drawn again from its last drawing, but it has changed:\n--- reused\n" +
					ansi.Strip(strings.Join(p.drawn.lines, "\n")) + "\n--- now\n" + ansi.Strip(strings.Join(now, "\n")))
			}
		}
		return append([]string(nil), p.drawn.lines...)
	}
	lines := p.render(scroll, cursor)
	p.dirty = false
	p.drawn.lines, p.drawn.scroll, p.drawn.cursor = lines, scroll, cursor
	return append([]string(nil), lines...)
}

func (p *Pane) render(scroll int, cursor bool) []string {
	w, h := p.emu.Width(), p.emu.Height()
	sb := p.emu.ScrollbackLen()
	if p.emu.IsAltScreen() {
		sb, scroll = 0, 0
	}
	scroll = min(max(scroll, 0), sb)
	top := sb - scroll // index into scrollback+screen of the first row shown

	cur := p.emu.CursorPosition()
	showCursor := cursor && p.cursorOn && scroll == 0 && !p.exited

	lines := make([]string, h)
	var b strings.Builder
	for y := 0; y < h; y++ {
		b.Reset()
		idx := top + y
		var prev uv.Style
		styled := false
		for x := 0; x < w; x++ {
			c := p.cellAt(x, idx, sb)
			content, width := " ", 1
			var st uv.Style
			if c != nil {
				if c.Width == 0 {
					continue // the tail of a wide character drawn before it
				}
				st = c.Style
				if c.Content != "" {
					content, width = cellText(c.Content, c.Width), c.Width
				}
			}
			if showCursor && x == cur.X && idx-sb == cur.Y {
				st.Attrs ^= uv.AttrReverse
			}
			if p.sel != nil && p.sel.contains(idx, x) {
				st.Attrs ^= uv.AttrReverse
			}
			if !st.Equal(&prev) || !styled {
				if st.IsZero() {
					if styled {
						b.WriteString(ansi.ResetStyle)
					}
				} else {
					b.WriteString(ansi.ResetStyle)
					b.WriteString(st.String())
				}
				prev, styled = st, !st.IsZero()
			}
			b.WriteString(content)
			x += width - 1
		}
		if styled {
			b.WriteString(ansi.ResetStyle)
		}
		lines[y] = b.String()
	}
	return lines
}

// cellAt is the cell at column x of line idx, counting the sb lines of
// scrollback first.
func (p *Pane) cellAt(x, idx, sb int) *uv.Cell {
	if idx < sb {
		return p.emu.ScrollbackCellAt(x, idx)
	}
	return p.emu.CellAt(x, idx-sb)
}

// ScrollbackLen is how many lines there are to scroll back through.
func (p *Pane) ScrollbackLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.emu.IsAltScreen() {
		return 0
	}
	return p.emu.ScrollbackLen()
}

// Close stops the program and releases the emulator.
//
// The emulator's Close is not safe to call while replyLoop is inside its
// Read (both touch an unguarded flag), so the reply pipe is closed first:
// that ends replyLoop, and only then is the emulator closed.
func (p *Pane) Close() {
	p.Stop(2 * time.Second)
	if c, ok := p.emu.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
	select {
	case <-p.replyDone:
	case <-time.After(2 * time.Second):
		return // better to leak the emulator than to race on it
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.emu.Close()
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
func (p *Pane) Text(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, h := p.emu.Width(), p.emu.Height()
	sb := p.emu.ScrollbackLen()
	if p.emu.IsAltScreen() {
		sb = 0
	}
	var lines []string
	var b strings.Builder
	for idx := 0; idx < sb+h; idx++ {
		b.Reset()
		for x := 0; x < w; x++ {
			c := p.cellAt(x, idx, sb)
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
