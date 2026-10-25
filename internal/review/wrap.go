package review

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A diff line longer than the pane is wide is wrapped onto more rows, at
// a space where there is one, rather than cut off at the edge: the rows
// after its first have no line number, only a ↪ where it would be. The
// cursor counts source lines; the view's top counts wrapped screen rows,
// so even a line taller than the pane can be scrolled through.

// tabSpaces is a tab as lipgloss draws it, which is how the pane drew it
// before it wrapped.
const tabSpaces = "    "

// diffBody is dl as drawn after its sign, styled, and its sign: added and
// removed lines have the sign in a column of their own, the rest none.
func diffBody(dl diffLine) (body, sign string) {
	switch dl.kind {
	case 'h':
		body = headerStyle.Render(dl.text)
	case '@':
		body = hunkStyle.Render(dl.text)
	case 'a':
		body = refStyle.Render(dl.text)
	case 'w':
		body = whyStyle.Render(dl.text)
	case '+', '-':
		sign = string(dl.kind)
		body = dl.hl
		if body == "" {
			body = changeStyle(dl.kind).Render(strings.TrimPrefix(dl.text, sign))
		}
	default:
		body = dl.hl
		if body == "" {
			body = dl.text
		}
	}
	return strings.ReplaceAll(body, "\t", tabSpaces), sign
}

// diffPlain is the text of diffBody without its styling, cheaper: what
// the rows are counted from.
func diffPlain(dl diffLine) string {
	s := dl.text
	if dl.kind == '+' || dl.kind == '-' {
		s = strings.TrimPrefix(s, string(dl.kind))
	}
	return strings.ReplaceAll(s, "\t", tabSpaces)
}

func changeStyle(kind byte) lipgloss.Style {
	if kind == '-' {
		return delStyle
	}
	return addStyle
}

// wrapStarts is the column each row of s starts at when s is wrapped w
// cells wide: after the last space that fits, or at w when no space does.
// Spaces in the indent are no place to break, or a row would be only
// them.
func wrapStarts(s string, w int) []int {
	starts := []int{0}
	if w <= 0 || len(s) <= w {
		// No row is wider than its bytes.
		return starts
	}
	col, rowStart, brk := 0, 0, -1
	indent := true
	for len(s) > 0 {
		cluster, rw := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		s = s[len(cluster):]
		if col+rw-rowStart > w && col > rowStart {
			if brk <= rowStart {
				brk = col
			}
			starts = append(starts, brk)
			rowStart, brk = brk, -1
		}
		col += rw
		if cluster == " " && !indent {
			brk = col
		} else if cluster != " " {
			indent = false
		}
	}
	return starts
}

// diffView is the diff as the pane wraps it: lines into rows, innerH of
// them shown.
type diffView struct {
	lines  []diffLine
	textW  int // the columns after the gutter
	gutter int
	innerH int
	sel    lineSpan // the lines chosen with the mouse, drawn as chosen
}

func newDiffView(lines []diffLine, paneW, innerH int) diffView {
	numWidth := 1
	for _, dl := range lines {
		if w := len(fmt.Sprint(dl.newLine)); dl.newLine > 0 && w > numWidth {
			numWidth = w
		}
	}
	gutter := numWidth + 3 // mark + space + number + space
	return diffView{lines: lines, textW: paneW - 2 - gutter, gutter: gutter, innerH: max(innerH, 1)}
}

// bodyW is how wide the text of line i is wrapped.
func (v diffView) bodyW(i int) int {
	if k := v.lines[i].kind; k == '+' || k == '-' {
		return v.textW - 1
	}
	return v.textW
}

// rows is how many rows line i takes.
func (v diffView) rows(i int) int {
	return len(wrapStarts(diffPlain(v.lines[i]), v.bodyW(i)))
}

// fit moves the top screen row to show cursor's source line. A line
// taller than the viewport keeps its current visible slice.
func (v diffView) fit(top, cursor int) int {
	top = clamp(top, 0, v.maxTop())
	if cursor < 0 || cursor >= len(v.lines) {
		return top
	}
	first, n := v.rowOf(cursor), v.rows(cursor)
	if n > v.innerH {
		if first+n <= top || first >= top+v.innerH {
			return min(first, v.maxTop())
		}
		return top
	}
	if first < top {
		return first
	}
	if first+n > top+v.innerH {
		return first + n - v.innerH
	}
	return top
}

// maxTop is the screen row that fills the viewport at the diff's end.
func (v diffView) maxTop() int {
	return max(0, v.rowOf(len(v.lines))-v.innerH)
}

// last is the last source line with a row visible from top.
func (v diffView) last(top int) int {
	return v.lineAt(0, min(top+v.innerH-1, v.rowOf(len(v.lines))-1))
}

// lineAt is the source line shown on screen row r from the top screen
// row; -1 outside the diff.
func (v diffView) lineAt(top, r int) int {
	r += top
	if r < 0 {
		return -1
	}
	for i := range v.lines {
		r -= v.rows(i)
		if r < 0 {
			return i
		}
	}
	return -1
}

// rowOf is the row line i starts on, counting from the diff's start.
func (v diffView) rowOf(i int) int {
	n := 0
	for j := 0; j < i && j < len(v.lines); j++ {
		n += v.rows(j)
	}
	return n
}

// render draws the rows from screen row top on, innerH of them, each
// gutter+textW wide, with the mark on line cursor's first.
func (v diffView) render(top, cursor int, active bool) []string {
	numWidth := v.gutter - 3
	cont := strings.Repeat(" ", numWidth-1) + dimStyle.Render("↪")
	rows := make([]string, 0, v.innerH)
	first := v.lineAt(0, top)
	if first < 0 {
		return rows
	}
	skip := top - v.rowOf(first)
	for i := first; i < len(v.lines) && len(rows) < v.innerH; i++ {
		dl := v.lines[i]
		mark := " "
		if active && i == cursor {
			mark = cursorMarkStyle.Render("▶")
		}
		num := strings.Repeat(" ", numWidth)
		if dl.newLine > 0 {
			num = dimStyle.Render(fmt.Sprintf("%*d", numWidth, dl.newLine))
		}
		drawn := diffRows(dl, v.bodyW(i))
		if v.sel.has(i) {
			drawn = selRows(dl, v.bodyW(i))
		}
		for j, r := range drawn {
			if skip > 0 {
				skip--
				continue
			}
			if len(rows) == v.innerH {
				break
			}
			if j == 0 {
				rows = append(rows, mark+" "+num+" "+r)
			} else {
				rows = append(rows, "  "+cont+" "+r)
			}
		}
	}
	return rows
}

// selRows is diffRows for a line chosen with the mouse: its text alone,
// in the colours of a selection, so what is copied is plain to see.
func selRows(dl diffLine, w int) []string {
	plain := diffPlain(dl)
	starts := wrapStarts(plain, w)
	end := ansi.StringWidth(plain)
	lead := ""
	if dl.kind == '+' || dl.kind == '-' {
		lead = selStyle.Render(" ")
	}
	rows := make([]string, len(starts))
	for j, s := range starts {
		e := end
		if j+1 < len(starts) {
			e = starts[j+1]
		}
		rows[j] = lead + selStyle.Render(fit(ansi.Cut(plain, s, e), w))
	}
	return rows
}

// diffRows draws dl in rows w cells wide, plus its sign column for an
// added or removed line. A changed line's tint is carried to the edge of
// the pane: plain padding would end it where the code ends, leaving a
// ragged block that reads as noise rather than as a changed region.
func diffRows(dl diffLine, w int) []string {
	body, sign := diffBody(dl)
	plain := diffPlain(dl)
	starts := wrapStarts(plain, w)
	end := ansi.StringWidth(plain)
	rows := make([]string, len(starts))
	for j, s := range starts {
		e := end
		if j+1 < len(starts) {
			e = starts[j+1]
		}
		row := ansi.Cut(body, s, e) + ansi.ResetStyle
		if sign == "" {
			rows[j] = fit(row, w)
			continue
		}
		line := changeStyle(dl.kind)
		lead := line.Render(" ")
		if j == 0 {
			signStyle := addSignStyle
			if dl.kind == '-' {
				signStyle = delSignStyle
			}
			lead = signStyle.Render(sign)
		}
		row = lipgloss.NewStyle().MaxWidth(max(w, 0)).Render(row)
		rows[j] = lead + row + line.Render(strings.Repeat(" ", max(0, w-lipgloss.Width(row))))
	}
	return rows
}
