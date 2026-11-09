package vt

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultScrollbackSize is the default maximum number of lines in the scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback represents a scrollback buffer that stores lines scrolled off the screen.
//
// The lines are kept packed (see packedLine): a uv.Cell is over a hundred
// bytes, and a busy terminal's thousands of lines of history as cells came
// to tens of MB each. A line is unpacked as it is read.
type Scrollback struct {
	lines    []packedLine
	maxLines int
	// dropped counts the lines let go from the front, so that a line
	// keeps its number (dropped+index) while others come and go.
	dropped uint64
	// line is the line last unpacked for CellAt, numbered lineN+1 (0 for
	// none): a frame reads a line cell by cell.
	line  uv.Line
	lineN uint64
}

// packedLine is a line of cells as the scrollback keeps it: the cells'
// content one after another in text, each cell's length of content and
// width in cells, and its style and link in runs, one per stretch of cells
// alike. A line that does not fit (content or width over 255) is kept as
// it was, in raw.
type packedLine struct {
	text  string
	cells []packedCell
	runs  []styleRun
	raw   uv.Line
}

type packedCell struct{ n, width uint8 }

// styleRun is the style and link of the cells up to end.
type styleRun struct {
	end   int
	style uv.Style
	link  uv.Link
}

func pack(line uv.Line) packedLine {
	var p packedLine
	var b strings.Builder
	p.cells = make([]packedCell, len(line))
	for i := range line {
		c := &line[i]
		if len(c.Content) > 255 || c.Width < 0 || c.Width > 255 {
			return packedLine{raw: line}
		}
		b.WriteString(c.Content)
		p.cells[i] = packedCell{uint8(len(c.Content)), uint8(c.Width)}
		if n := len(p.runs); n > 0 && p.runs[n-1].style == c.Style && p.runs[n-1].link == c.Link {
			p.runs[n-1].end = i + 1
		} else {
			p.runs = append(p.runs, styleRun{end: i + 1, style: c.Style, link: c.Link})
		}
	}
	p.text = b.String()
	return p
}

// unpack is the line p packs, into out's room when there is enough.
func (p *packedLine) unpack(out uv.Line) uv.Line {
	if p.raw != nil {
		return append(out[:0], p.raw...)
	}
	out = out[:0]
	at, run := 0, 0
	for i, pc := range p.cells {
		for p.runs[run].end <= i {
			run++
		}
		r := &p.runs[run]
		out = append(out, uv.Cell{
			Content: p.text[at : at+int(pc.n)],
			Width:   int(pc.width),
			Style:   r.style,
			Link:    r.link,
		})
		at += int(pc.n)
	}
	return out
}

// NewScrollback creates a new scrollback buffer with the given maximum number of lines.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{
		lines:    make([]packedLine, 0, min(maxLines, 1000)), // Pre-allocate reasonable capacity
		maxLines: maxLines,
	}
}

// Push adds a line to the scrollback buffer.
// If the buffer is full, the oldest line is removed.
func (s *Scrollback) Push(line uv.Line) {
	if s == nil || s.maxLines <= 0 {
		return
	}

	// Find last non-empty cell to trim trailing empty cells.
	// This helps with wrapping and window resizing.
	lastNonEmpty := -1
	for i := len(line) - 1; i >= 0; i-- {
		c := &line[i]
		if !c.IsZero() && !c.Equal(&uv.EmptyCell) {
			lastNonEmpty = i
			break
		}
	}
	s.add(line[:lastNonEmpty+1])
}

// add keeps line as it is, the oldest let go when the buffer is full.
func (s *Scrollback) add(line uv.Line) {
	p := pack(line)
	if p.raw != nil {
		p.raw = append(uv.Line(nil), line...)
	}
	if s.maxLines > 0 && len(s.lines) >= s.maxLines {
		s.drop(len(s.lines) - s.maxLines + 1)
	}
	s.lines = append(s.lines, p)
}

// drop lets go of the n oldest lines. Sliding the front along costs
// nothing; append moves what is left once the room behind runs out.
func (s *Scrollback) drop(n int) {
	clear(s.lines[:n])
	s.lines = s.lines[n:]
	s.dropped += uint64(n)
}

// PushN adds n lines from the buffer starting at line y to the scrollback.
func (s *Scrollback) PushN(buf *uv.RenderBuffer, y, n int) {
	if s == nil || buf == nil || n <= 0 {
		return
	}

	for i := range min(n, buf.Height()-y) {
		if line := buf.Line(y + i); line != nil {
			s.Push(line)
		}
	}
}

// Len returns the number of lines in the scrollback buffer.
func (s *Scrollback) Len() int {
	if s == nil {
		return 0
	}
	return len(s.lines)
}

// MaxLines returns the maximum number of lines the scrollback buffer can hold.
func (s *Scrollback) MaxLines() int {
	if s == nil {
		return 0
	}
	return s.maxLines
}

// SetMaxLines sets the maximum number of lines in the scrollback buffer.
// If the current number of lines exceeds the new maximum, oldest lines are removed.
func (s *Scrollback) SetMaxLines(maxLines int) {
	if s == nil || maxLines <= 0 {
		return
	}

	s.maxLines = maxLines
	if len(s.lines) > maxLines {
		s.drop(len(s.lines) - maxLines)
	}
}

// Line returns the line at the given index, a copy of its own.
// Index 0 is the oldest line, Len()-1 is the most recent.
// Returns nil if index is out of bounds.
func (s *Scrollback) Line(index int) uv.Line {
	if s == nil || index < 0 || index >= len(s.lines) {
		return nil
	}
	return s.lines[index].unpack(nil)
}

// Lines returns all lines in the scrollback buffer, copies of their own.
// Index 0 is the oldest line.
func (s *Scrollback) Lines() []uv.Line {
	if s == nil {
		return nil
	}
	out := make([]uv.Line, len(s.lines))
	for i := range s.lines {
		out[i] = s.lines[i].unpack(nil)
	}
	return out
}

// Clear removes all lines from the scrollback buffer.
func (s *Scrollback) Clear() {
	if s == nil {
		return
	}
	s.drop(len(s.lines))
	s.lines = s.lines[:0]
}

// CellAt returns the cell at the given position in the scrollback buffer.
// x is the column, y is the line index (0 = oldest).
// Returns nil if position is out of bounds. The cell is the scrollback's
// own, good until the next call: the line is unpacked for it, and kept for
// the next cell of the same line.
func (s *Scrollback) CellAt(x, y int) *uv.Cell {
	if s == nil || y < 0 || y >= len(s.lines) {
		return nil
	}
	if n := s.dropped + uint64(y) + 1; s.lineN != n {
		s.line, s.lineN = s.lines[y].unpack(s.line), n
	}
	if x < 0 || x >= len(s.line) {
		return nil
	}
	return &s.line[x]
}
