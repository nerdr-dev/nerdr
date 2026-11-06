package app

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/term"

	"github.com/charmbracelet/x/ansi"
)

// drawnPane is a program's screen as last drawn: at its DrawGen, scroll
// lines back, with the cursor or not and the text chosen, if any.
type drawnPane struct {
	gen    uint64
	scroll int
	cursor bool
	sel    *term.Selection
	lines  []string
	// frame is the last frame taken, its rows used again for the next.
	frame term.Frame
}

// drawnPanesMax is how many drawings are kept before all are let go, so the
// programs gone take none with them.
const drawnPanesMax = 64

// drawPane draws tp's screen scroll lines back, the cursor drawn when cursor
// is set, and the text being chosen in it. A program drawing many times a
// second is drawn from its frames as they come; one whose screen has not
// changed, from its last drawing. See reuseCheck.
func (m *Model) drawPane(tp core.Program, scroll int, cursor bool) []string {
	var sel *term.Selection
	if m.selShown && tp == m.selPane {
		s := m.sel
		sel = &s
	}
	d := m.drawnPanes[tp]
	if d != nil && d.scroll == scroll && d.cursor == cursor && sameSel(d.sel, sel) && tp.DrawGen() == d.gen {
		if reuseCheck {
			if f := tp.Frame(scroll); f.Gen == d.gen {
				if now := term.Draw(f, cursor, sel); strings.Join(now, "\n") != strings.Join(d.lines, "\n") {
					panic("pane drawn again from its last drawing, but it has changed:\n--- reused\n" +
						ansi.Strip(strings.Join(d.lines, "\n")) + "\n--- now\n" + ansi.Strip(strings.Join(now, "\n")))
				}
			}
		}
		return append([]string(nil), d.lines...)
	}
	if d == nil {
		if m.drawnPanes == nil || len(m.drawnPanes) >= drawnPanesMax {
			m.drawnPanes = map[core.Program]*drawnPane{}
		}
		d = &drawnPane{}
		m.drawnPanes[tp] = d
	}
	tp.FrameInto(&d.frame, scroll)
	d.lines = term.Draw(d.frame, cursor, sel)
	d.gen, d.scroll, d.cursor, d.sel = d.frame.Gen, scroll, cursor, sel
	return append([]string(nil), d.lines...)
}

func sameSel(a, b *term.Selection) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// selectedText is the text chosen in p, read from its rows.
func selectedText(p core.Program, sel term.Selection) string {
	from, to := sel.From.Line, sel.To.Line
	if to < from {
		from, to = to, from
	}
	first, rows := p.Rows(from, to)
	return term.SelectedText(first, rows, sel)
}
