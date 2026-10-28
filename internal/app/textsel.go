package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	rows []string
	// seps[i] is what came between rows i-1 and i in text: a newline, or
	// the spaces the wrap took out, so a copy is the text as it was.
	seps []string
	// zones are the zone of each row drawn, by id, from the last frame.
	zones map[int]int
	// on is set from a press on the text until the button is let go.
	on       bool
	from, to selPoint
	// scroll, if the rows are not all shown, scrolls them by n (down when
	// positive), for a drag past the top or the bottom.
	scroll func(n int)
}

// setText wraps text to w columns, unless s has it so already; a
// selection in other text is let go.
func (s *textSel) setText(text string, w int) {
	if s.rows != nil && s.text == text && s.w == w {
		return
	}
	s.text, s.w, s.on = text, w, false
	s.rows, s.seps = nil, nil
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		rest, lost := line, false
		for j, r := range strings.Split(ansi.Wrap(line, w, ""), "\n") {
			sep := ""
			switch {
			case j == 0 && i > 0:
				sep = "\n"
			case j == 0:
			case lost:
				sep = " "
			case !strings.HasPrefix(rest, r):
				t := strings.TrimLeft(rest, " ")
				sep, rest = rest[:len(rest)-len(t)], t
			}
			var ok bool
			if rest, ok = strings.CutPrefix(rest, r); !ok {
				lost = true // not as the wrap left it: tabs, say
			}
			s.rows = append(s.rows, r)
			s.seps = append(s.seps, sep)
		}
	}
}

// selStyle is how what is selected is shown.
var selStyle = lipgloss.NewStyle().Reverse(true)

// selRows draws rows from to to of s, each a zone a press on starts a
// selection in, with what is selected shown.
func (m *Model) selRows(s *textSel, from, to int) []string {
	s.zones = map[int]int{}
	wide := 1 // so an empty row has a cell to be found by
	for _, r := range s.rows {
		wide = max(wide, ansi.StringWidth(r))
	}
	var out []string
	for i := from; i < min(to, len(s.rows)); i++ {
		row := s.rows[i]
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
			return selPoint{row, min(max(x-r.x0, 0), ansi.StringWidth(s.rows[row]))}, 0
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
	return selPoint{bottom, ansi.StringWidth(s.rows[bottom])}, 1
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
	w := ansi.StringWidth(s.rows[i])
	a, b = 0, w
	if i == from.row {
		a = from.col
	}
	if i == to.row {
		b = min(to.col+1, w) // the cell under the pointer too, as in a pane
	}
	return a, b, a < b
}

// selected is the text selected in s.
func (s *textSel) selected() string {
	if !s.on || s.from == s.to {
		return ""
	}
	from, to := s.ordered()
	var out strings.Builder
	for i := from.row; i <= to.row; i++ {
		if i > from.row {
			out.WriteString(s.seps[i])
		}
		if a, b, ok := s.span(i); ok {
			out.WriteString(ansi.Cut(s.rows[i], a, b))
		}
	}
	return out.String()
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
