package review

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	numW   int // the width of one line number
	innerH int
	sel    lineSpan // the lines chosen with the mouse, drawn as chosen
	first  []int    // the row each line starts on, and the rows in all last
}

func newDiffView(lines []diffLine, paneW, innerH int) diffView {
	return newCachedDiffView(nil, lines, paneW, innerH)
}

// newCachedDiffView is newDiffView with the rows each line wraps to kept
// in c, when there is one, for the next frame at the same width.
func newCachedDiffView(c *rowCache, lines []diffLine, paneW, innerH int) diffView {
	numWidth := 1
	for _, dl := range lines {
		numWidth = max(numWidth, len(strconv.Itoa(max(dl.newLine, dl.oldLine))))
	}
	gutter := numWidth + 3 // mark + space + number + space
	if bothNums {
		gutter += numWidth + 2 // and the old side's number, a space before it and one after
	}
	v := diffView{lines: lines, textW: paneW - 2 - gutter, gutter: gutter, numW: numWidth, innerH: max(innerH, 1)}
	v.first = c.starts(v)
	return v
}

// rowCache keeps where each line of a diff starts when it is wrapped, so
// the diff is wrapped once rather than over every line, several times,
// in every frame: on a diff of thousands of long lines that is what made
// each key and scroll stutter. It is good for as long as the lines' text
// and the width are the same, which it checks for itself; comparing a
// string with itself costs no reading of it.
type rowCache struct {
	mu    sync.Mutex
	textW int
	texts []string
	kinds []byte
	first []int
}

// starts is v's row starts, from c when it holds them for v's lines and
// width. A nil c works them out every time.
func (c *rowCache) starts(v diffView) []int {
	if c == nil {
		return v.countRows()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.holds(v) {
		return c.first
	}
	c.first = v.countRows()
	c.textW = v.textW
	c.texts = c.texts[:0]
	c.kinds = c.kinds[:0]
	for _, dl := range v.lines {
		c.texts = append(c.texts, dl.text)
		c.kinds = append(c.kinds, dl.kind)
	}
	return c.first
}

// holds is whether c's rows are of v's lines at v's width.
func (c *rowCache) holds(v diffView) bool {
	if c.first == nil || c.textW != v.textW || len(c.texts) != len(v.lines) {
		return false
	}
	for i, dl := range v.lines {
		if c.texts[i] != dl.text || c.kinds[i] != dl.kind {
			return false
		}
	}
	return true
}

// countRows is the row each of v's lines starts on, and the rows in all.
func (v diffView) countRows() []int {
	first := make([]int, len(v.lines)+1)
	for i := range v.lines {
		first[i+1] = first[i] + len(wrapStarts(diffPlain(v.lines[i]), v.bodyW(i)))
	}
	return first
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
	return v.first[i+1] - v.first[i]
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
	if r < 0 || r >= v.first[len(v.lines)] {
		return -1
	}
	// The last line starting on or before r.
	return sort.SearchInts(v.first[1:], r+1)
}

// rowOf is the row line i starts on, counting from the diff's start.
func (v diffView) rowOf(i int) int {
	return v.first[clamp(i, 0, len(v.lines))]
}

// render draws the rows from screen row top on, innerH of them, each
// gutter+textW wide, with the mark on line cursor's first.
func (v diffView) render(top, cursor int, active bool) []string {
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
				rows = append(rows, mark+v.nums(dl, false)+r)
			} else {
				rows = append(rows, " "+v.nums(dl, true)+r)
			}
		}
	}
	return rows
}

// nums is the gutter after its mark: the line's numbers, the old side's
// too when the scheme shows both, in the tint of the row's kind. A row
// that continues a wrapped line has a ↪ in place of the new number.
func (v diffView) nums(dl diffLine, cont bool) string {
	// The space after GitHub's numbers is the row's own, in its tint.
	style, gap := numStyle, " "
	switch dl.kind {
	case '+':
		style, gap = addNumStyle, addStyle.Render(" ")
	case '-':
		style, gap = delNumStyle, delStyle.Render(" ")
	case '@':
		style, gap = hunkNumStyle, hunkStyle.Render(" ")
	}
	num := func(n int) string {
		if n <= 0 {
			return strings.Repeat(" ", v.numW)
		}
		return fmt.Sprintf("%*d", v.numW, n)
	}
	newNum := num(dl.newLine)
	if cont {
		newNum = strings.Repeat(" ", v.numW-1) + "↪"
	}
	if !bothNums {
		return style.Render(" "+newNum) + " "
	}
	oldNum := num(dl.oldLine)
	if cont {
		oldNum = num(0)
	}
	return style.Render(" "+oldNum+" "+newNum+" ") + gap
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
		if dl.kind == '@' {
			// The hunk's band, like a changed line's tint, goes to the edge.
			rows[j] = hunkStyle.Render(fit(ansi.Cut(plain, s, e), w))
			continue
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
		if w > 0 {
			row = lipgloss.NewStyle().MaxWidth(w).Render(row)
		} else {
			row = "" // MaxWidth(0) is no limit at all, not none
		}
		rows[j] = lead + row + line.Render(strings.Repeat(" ", max(0, w-lipgloss.Width(row))))
	}
	return rows
}
