package term

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// What a client is given of a screen to draw it: the rows shown, cell by
// cell, with the cursor and the modes that change how it is shown. The client
// draws them its own way (Draw is one), chooses text on them and copies it;
// the emulator stays with the program.

// Cell is one cell of a row: its text, how many columns it takes, 0 for the
// tail of a wide character in the cell before, and its style.
type Cell struct {
	Text  string
	Width int
	Style uv.Style
}

// Frame is a screen as it shows scroll lines back into its scrollback.
type Frame struct {
	W, H int
	// Rows are the H rows shown, of W cells. Top is the first's index in the
	// scrollback and the screen together, as Points count lines.
	Rows [][]Cell
	Top  int
	// CursorX and CursorY are the cursor's cell among Rows; Cursor is set
	// when the program shows it there: at the bottom of the scrollback, and
	// with the program still running.
	CursorX, CursorY int
	Cursor           bool
	// Alt is set while the program is on the alternate screen, which has no
	// scrollback; Scrollback is how many lines there are to scroll back
	// through.
	Alt        bool
	Scrollback int
	Title      string
	// Incomplete is set when the screen may be missing some of what the
	// program drew; see Screen.Incomplete.
	Incomplete bool
	// Gen is the screen's DrawGen when the frame was taken.
	Gen uint64
}

// DrawGen counts the changes to what a frame of the screen shows: output, a
// resize, the program's exit. A client that drew a frame at one DrawGen, at
// the same scroll, need not take another while it is the same. It marks the
// screen as drawn on this frame; see NextFrame.
func (s *Screen) DrawGen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drawnOn.Store(frame.Load())
	return s.drawGen
}

// Frame is the screen as it shows scroll lines back. It marks the screen as
// drawn on this frame; see NextFrame.
func (s *Screen) Frame(scroll int) Frame {
	var f Frame
	s.FrameInto(&f, scroll)
	return f
}

// FrameInto is Frame, into f: its rows are used again for the new ones, so a
// client drawing a busy program many times a second makes no new ones.
func (s *Screen) FrameInto(f *Frame, scroll int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drawnOn.Store(frame.Load())
	w, h := s.emu.Width(), s.emu.Height()
	sb := s.emu.ScrollbackLen()
	alt := s.emu.IsAltScreen()
	if alt {
		sb, scroll = 0, 0
	}
	scroll = min(max(scroll, 0), sb)
	rows := f.Rows
	*f = Frame{W: w, H: h, Top: sb - scroll, Alt: alt, Scrollback: sb, Title: s.title, Incomplete: s.incomplete, Gen: s.drawGen}
	cur := s.emu.CursorPosition()
	f.CursorX, f.CursorY = cur.X, cur.Y
	f.Cursor = s.cursorOn && scroll == 0 && !s.exited
	if cap(rows) < h {
		rows = make([][]Cell, h)
	}
	f.Rows = rows[:h]
	for y := range h {
		f.Rows[y] = s.rowInto(f.Rows[y], f.Top+y, w, sb)
	}
}

// Rows are the lines from to to, both included, of the scrollback and the
// screen together, as Points count them, and the index of the first: fewer
// at the ends. A client reads with them the text chosen beyond what it shows.
func (s *Screen) Rows(from, to int) (int, [][]Cell) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.emu.Width()
	sb := s.emu.ScrollbackLen()
	if s.emu.IsAltScreen() {
		sb = 0
	}
	from, to = max(from, 0), min(to, sb+s.emu.Height()-1)
	var out [][]Cell
	for line := from; line <= to; line++ {
		out = append(out, s.row(line, w, sb))
	}
	return from, out
}

// row is line idx of the scrollback, sb lines, and the screen together, w
// cells. s.mu is held.
func (s *Screen) row(idx, w, sb int) []Cell { return s.rowInto(nil, idx, w, sb) }

// rowInto is row, into out's room when there is enough.
func (s *Screen) rowInto(out []Cell, idx, w, sb int) []Cell {
	if cap(out) < w {
		out = make([]Cell, w)
	}
	out = out[:w]
	for x := range w {
		cell := &out[x]
		c := s.cellAt(x, idx, sb)
		if c == nil {
			*cell = Cell{Text: " ", Width: 1}
			continue
		}
		cell.Style, cell.Width, cell.Text = c.Style, c.Width, " "
		switch {
		case c.Width == 0:
			cell.Text = ""
		case c.Content != "":
			cell.Text = cellText(c.Content, c.Width)
		}
	}
	return out
}

// Draw is f as lines of styled text. The cursor is drawn as a reverse-video
// cell when cursor is set and f shows it: the real terminal cursor belongs to
// the client's own UI. What sel chooses is drawn reversed too.
func Draw(f Frame, cursor bool, sel *Selection) []string {
	lines := make([]string, len(f.Rows))
	var b strings.Builder
	for y, row := range f.Rows {
		b.Reset()
		var prev uv.Style
		styled := false
		for x := 0; x < len(row); x++ {
			c := &row[x]
			if c.Width == 0 {
				continue // the tail of a wide character drawn before it
			}
			st := &c.Style
			flip := cursor && f.Cursor && x == f.CursorX && y == f.CursorY
			if sel != nil && sel.Contains(f.Top+y, x) {
				flip = !flip
			}
			if flip {
				own := c.Style
				own.Attrs ^= uv.AttrReverse
				st = &own
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
				prev, styled = *st, !st.IsZero()
			}
			b.WriteString(c.Text)
			x += c.Width - 1
		}
		if styled {
			b.WriteString(ansi.ResetStyle)
		}
		lines[y] = b.String()
	}
	return lines
}

// PointAt is the cell at (x, y) of the frame, as a Selection counts.
func (f Frame) PointAt(x, y int) Point { return Point{Line: f.Top + y, Col: x} }

// SelectedText is the text sel chooses among rows, the first of them line
// first, trailing blanks trimmed from each line.
func SelectedText(first int, rows [][]Cell, sel Selection) string {
	a, b := sel.ordered()
	var lines []string
	for line := a.Line; line <= b.Line; line++ {
		i := line - first
		if i < 0 || i >= len(rows) {
			continue
		}
		var sbuf strings.Builder
		for x, c := range rows[i] {
			if !sel.Contains(line, x) {
				continue
			}
			if c.Width == 0 {
				continue
			}
			sbuf.WriteString(c.Text)
		}
		lines = append(lines, strings.TrimRight(sbuf.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// cellAt is the cell at column x of line idx, counting the sb lines of
// scrollback first.
func (s *Screen) cellAt(x, idx, sb int) *uv.Cell {
	if idx < sb {
		return s.emu.ScrollbackCellAt(x, idx)
	}
	return s.emu.CellAt(x, idx-sb)
}

// TopAt is the index of the first row a frame shows scroll lines back, as
// Points count lines.
func (s *Screen) TopAt(scroll int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emu.IsAltScreen() {
		return 0
	}
	sb := s.emu.ScrollbackLen()
	return sb - min(max(scroll, 0), sb)
}
