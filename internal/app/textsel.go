package app

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Text in a dialog can be selected with the mouse, as the output of a
// pane can: press on it, drag, and what is between is copied when the
// button is let go. The terminal's own selection cannot reach it, as
// nerdr has the mouse. A dialog keeps a textSel for the text and draws its
// rows with selRows.

// selPoint is a cell of a textSel: its row, and the column in it.
type selPoint struct{ row, col int }

func (a selPoint) before(b selPoint) bool {
	return a.row < b.row || a.row == b.row && a.col < b.col
}

// textSel is text wrapped to rows for a dialog, and what of it is being
// selected.
type textSel struct {
	text string
	w    int
	rows []selRow
	// zones are the zone of each row drawn, by id, from the last frame.
	zones map[int]int
	// on is set from a press on the text until the button is let go.
	on       bool
	from, to selPoint
	// scroll, if the rows are not all shown, scrolls them by n (down when
	// positive), for a drag past the top or the bottom.
	scroll func(n int)
}

// selRow is a row of a textSel: what is drawn, a tab as spaces, and where
// in the text each of its cells came from, so a copy is cut from the text
// as it was, the spaces and line ends the wrap took out kept.
type selRow struct {
	drawn string
	// start[c] and end[c] are the bytes of the text cell c was drawn
	// from; off and endOff are the row's.
	start, end  []int
	off, endOff int
}

func (r selRow) width() int { return len(r.start) }

// glyph is a grapheme of the text, as wrapped.
type glyph struct {
	drawn      string
	start, end int
	w          int
	space      bool
}

// tabWidth is how many spaces a tab is drawn as, as lipgloss does.
const tabWidth = 4

// setText wraps text to w columns, unless s has it so already; a
// selection in other text is let go.
func (s *textSel) setText(text string, w int) {
	if s.rows != nil && s.text == text && s.w == w {
		return
	}
	s.text, s.w, s.on = text, w, false
	s.rows = nil
	off := 0
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		for _, gs := range wrapGlyphs(glyphsOf(line, off), w) {
			r := selRow{off: off, endOff: off}
			if len(gs) > 0 {
				r.off, r.endOff = gs[0].start, gs[len(gs)-1].end
			}
			var b strings.Builder
			for _, g := range gs {
				b.WriteString(g.drawn)
				for range g.w {
					r.start, r.end = append(r.start, g.start), append(r.end, g.end)
				}
			}
			r.drawn = b.String()
			s.rows = append(s.rows, r)
		}
		off += len(line) + 1
	}
}

// glyphsOf is line's graphemes, line being at byte off of the text. One
// that takes no cell is drawn with the one before it.
func glyphsOf(line string, off int) []glyph {
	var gs []glyph
	pending := -1 // the start of graphemes of no width with none before
	for rest, state := line, -1; rest != ""; {
		var g string
		g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		start := off
		off += len(g)
		r, _ := utf8.DecodeRuneInString(g)
		gl := glyph{drawn: g, start: start, end: off, w: ansi.StringWidth(g),
			space: r != '\u00a0' && unicode.IsSpace(r)}
		if g == "\t" {
			gl.drawn, gl.w = strings.Repeat(" ", tabWidth), tabWidth
		}
		switch {
		case gl.w == 0 && len(gs) > 0:
			gs[len(gs)-1].drawn += g
			gs[len(gs)-1].end = off
			continue
		case gl.w == 0:
			if pending < 0 {
				pending = start
			}
			continue
		case pending >= 0:
			gl.start, pending = pending, -1
		}
		gs = append(gs, gl)
	}
	return gs
}

// wrapGlyphs wraps a line to rows of at most w cells: at the last space
// that fits, the spaces there taken out, and in a word too long for a row
// where it gets to the edge. The line's own spaces at its end go too.
func wrapGlyphs(gs []glyph, w int) [][]glyph {
	var rows [][]glyph
	var row []glyph
	cells, broke := 0, false
	for _, g := range gs {
		if broke && len(row) == 0 && g.space {
			continue // the rest of the spaces broken at
		}
		if cells+g.w > w && len(row) > 0 {
			broke = true
			if g.space {
				rows = append(rows, trimSpaces(row))
				row, cells = nil, 0
				continue
			}
			i := len(row) - 1
			for i >= 0 && !row[i].space {
				i--
			}
			if head := trimSpaces(row[:max(i, 0)]); i > 0 && len(head) > 0 {
				rows = append(rows, head)
				row = slices.Clone(row[i+1:])
			} else {
				rows = append(rows, row)
				row = nil
			}
			cells = 0
			for _, g := range row {
				cells += g.w
			}
		}
		row = append(row, g)
		cells += g.w
	}
	return append(rows, trimSpaces(row))
}

// trimSpaces is row without the spaces at its end.
func trimSpaces(row []glyph) []glyph {
	for len(row) > 0 && row[len(row)-1].space {
		row = row[:len(row)-1]
	}
	return row
}

// selStyle is how what is selected is shown.
var selStyle = lipgloss.NewStyle().Reverse(true)

// selRows draws rows from to to of s, each a zone a press on starts a
// selection in, with what is selected shown.
func (m *Model) selRows(s *textSel, from, to int) []string {
	s.zones = map[int]int{}
	wide := 1 // so an empty row has a cell to be found by
	for _, r := range s.rows {
		wide = max(wide, r.width())
	}
	var out []string
	for i := from; i < min(to, len(s.rows)); i++ {
		row := s.rows[i].drawn
		if a, b, ok := s.span(i); ok {
			row = ansi.Cut(row, 0, a) + selStyle.Render(ansi.Cut(row, a, b)) + ansi.Cut(row, b, 1<<20)
		}
		s.zones[len(m.zoneFns)] = i
		out = append(out, m.zone(padTo(row, wide), func(m *Model, ev tea.MouseMsg) tea.Cmd {
			if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
				p, _ := s.pointAt(m, ev.X, ev.Y)
				s.on, s.from, s.to = true, p, p
				m.textSel, m.drag = s, dragText
			}
			return nil
		}))
	}
	return out
}

// pointAt is the cell of s drawn at (x, y) on the screen, or, off the
// rows shown, the start of the first or the end of the last; past says
// which (-1 above, 1 below).
func (s *textSel) pointAt(m *Model, x, y int) (p selPoint, past int) {
	top, topY, bottom := -1, 0, -1
	for _, r := range m.zoneRects {
		row, ok := s.zones[r.id]
		if !ok {
			continue
		}
		if r.y == y {
			return selPoint{row, min(max(x-r.x0, 0), s.rows[row].width())}, 0
		}
		if top < 0 || row < top {
			top, topY = row, r.y
		}
		bottom = max(bottom, row)
	}
	switch {
	case top < 0:
		return s.to, 0
	case y < topY:
		return selPoint{top, 0}, -1
	}
	return selPoint{bottom, s.rows[bottom].width()}, 1
}

// ordered is the selection from its start to its end.
func (s *textSel) ordered() (selPoint, selPoint) {
	if s.to.before(s.from) {
		return s.to, s.from
	}
	return s.from, s.to
}

// span is the columns of row i selected, from a up to b.
func (s *textSel) span(i int) (a, b int, ok bool) {
	if !s.on || s.from == s.to {
		return 0, 0, false
	}
	from, to := s.ordered()
	if i < from.row || i > to.row {
		return 0, 0, false
	}
	w := s.rows[i].width()
	a, b = 0, w
	if i == from.row {
		a = from.col
	}
	if i == to.row {
		b = min(to.col+1, w) // the cell under the pointer too, as in a pane
	}
	return a, b, a < b
}

// selected is the text selected in s, cut from the text itself.
func (s *textSel) selected() string {
	if !s.on || s.from == s.to {
		return ""
	}
	from, to := s.ordered()
	begin, end := s.rows[from.row].endOff, s.rows[to.row].endOff
	if r := s.rows[from.row]; from.col < r.width() {
		begin = r.start[from.col]
	}
	if r := s.rows[to.row]; to.col < r.width() {
		end = r.end[to.col] // the cell under the pointer too, as in a pane
	}
	if begin >= end {
		return ""
	}
	return s.text[begin:end]
}

// dragText carries a selection in a dialog's text on, and copies it when
// the button is let go.
func (m *Model) dragText(ev tea.MouseMsg) {
	s := m.textSel
	p, past := s.pointAt(m, ev.X, ev.Y)
	if past != 0 && s.scroll != nil {
		s.scroll(past)
	}
	s.to = p
	if ev.Action == tea.MouseActionRelease {
		text := s.selected()
		s.on, m.textSel, m.drag = false, nil, dragNone
		m.copyText(text)
	}
}
