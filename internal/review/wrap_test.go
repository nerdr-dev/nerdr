package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestWrapStarts(t *testing.T) {
	for _, c := range []struct {
		s    string
		w    int
		want []int
	}{
		{"short", 10, []int{0}},
		{"aaaa bbbb cccc", 10, []int{0, 10}},
		{"aaaaaaaaaaaaaaaaaaaaaaa", 10, []int{0, 10, 20}},
		// The indent is no place to break.
		{"        abcdefghijklmnop", 10, []int{0, 10, 20}},
		{"漢字漢字漢字", 5, []int{0, 4, 8}},
	} {
		got := wrapStarts(c.s, c.w)
		if len(got) != len(c.want) {
			t.Errorf("wrapStarts(%q, %d) = %v, want %v", c.s, c.w, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("wrapStarts(%q, %d) = %v, want %v", c.s, c.w, got, c.want)
				break
			}
		}
	}
}

// TestLongLinesWrap covers a line longer than the pane is wide: all of it
// is shown, on rows of the pane's width, the ones after the first with ↪
// where the line number would be.
func TestLongLinesWrap(t *testing.T) {
	long := "+" + strings.Repeat("word ", 30) + "END"
	lines := []diffLine{
		{kind: ' ', text: " before", newLine: 1},
		{kind: '+', text: long, newLine: 2},
		{kind: ' ', text: " after", newLine: 3},
	}
	pane := renderDiff(lines, 1, 0, 60, 20, true, "diff", "", lineSpan{})
	plain := ansi.Strip(pane)
	if !strings.Contains(plain, "END") {
		t.Fatalf("the end of a long line is shown:\n%s", plain)
	}
	if n := strings.Count(plain, "↪"); n != 2 {
		t.Fatalf("a line three rows long has two rows marked ↪, got %d:\n%s", n, plain)
	}
	for i, row := range strings.Split(pane, "\n") {
		if w := lipgloss.Width(row); w != 60 {
			t.Fatalf("row %d is %d wide, want 60: %q", i, w, ansi.Strip(row))
		}
	}
	if !strings.Contains(plain, "after") {
		t.Fatal("the line after a wrapped one follows its last row")
	}
}

// TestCursorOnAWrappedLineStaysInView covers the view counting rows, not
// lines: moving down to a line that wraps scrolls far enough to show all
// of it, and the wheel goes as far as the end of the last line.
func TestCursorOnAWrappedLineStaysInView(t *testing.T) {
	lines := make([]diffLine, 60)
	for i := range lines {
		lines[i] = diffLine{kind: ' ', text: " " + strings.Repeat("x ", 100), newLine: i + 1}
	}
	m := Model{width: 120, height: 40, focus: focusDiff, diffLines: lines}
	v := m.diffView()
	if v.rows(0) < 2 {
		t.Fatalf("the test needs lines that wrap, got %d row", v.rows(0))
	}

	m.setDiffCursor(10)
	v = m.diffView()
	if last := v.last(m.diffViewTop); last != 10 {
		t.Fatalf("the cursor's line is the last all in view: last %d, top %d", last, m.diffViewTop)
	}

	m.scrollDiffTo(v.rowOf(len(lines)))
	if got := m.diffView().last(m.diffViewTop); got != len(lines)-1 {
		t.Fatalf("scrolled to the end, the last line is in view: last %d", got)
	}
}

// TestClickOnAWrappedRowSelectsItsLine covers a click on a ↪ row landing
// on the line it is part of.
func TestClickOnAWrappedRowSelectsItsLine(t *testing.T) {
	lines := []diffLine{
		{kind: ' ', text: " " + strings.Repeat("y ", 100), newLine: 1},
		{kind: ' ', text: " short", newLine: 2},
	}
	m := Model{width: 120, height: 40, focus: focusFiles, diffLines: lines}
	lay := m.layout(m.width, m.height)
	rows := m.diffView().rows(0)

	click := func(row int) int {
		nm, _ := m.Update(tea.MouseMsg{X: lay.diff.x + 5, Y: lay.diff.y + 2 + m.diffHeader() + row,
			Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		return nm.(Model).diffCursor
	}
	if got := click(rows - 1); got != 0 {
		t.Fatalf("a click on the long line's last row selects it, got line %d", got)
	}
	if got := click(rows); got != 1 {
		t.Fatalf("a click on the row after it selects the next line, got line %d", got)
	}
}

// A single source line can fill several screens. Both the rendered view
// and click targets must follow the screen row while the cursor stays
// attached to its source line.
func TestScrollWithinTallDiffLine(t *testing.T) {
	m := Model{width: 120, height: 40, focus: focusDiff,
		diffLines: []diffLine{{kind: '+', text: "+" + strings.Repeat("x", 4000) + "END", newLine: 1}}}
	v := m.diffView()
	if v.rows(0) <= v.innerH*2 {
		t.Fatal("test needs more than two screens")
	}
	m.page(1)
	if m.diffViewTop != v.innerH || m.diffCursor != 0 {
		t.Fatalf("page down: top %d, cursor %d", m.diffViewTop, m.diffCursor)
	}
	m.page(-1)
	if m.diffViewTop != 0 {
		t.Fatal("page up did not return to the start")
	}

	lay := m.layout(m.width, m.height)
	nm, _ := m.Update(tea.MouseMsg{X: lay.diff.x + 5, Y: lay.diff.y + 3,
		Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = nm.(Model)
	if m.diffViewTop != wheelRows {
		t.Fatalf("wheel top %d", m.diffViewTop)
	}
	// Clicking a continuation row must not jump back to the line's start.
	nm, _ = m.Update(tea.MouseMsg{X: lay.diff.x + 5, Y: lay.diff.y + 2 + m.diffHeader(),
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = nm.(Model)
	if m.diffViewTop != wheelRows || m.diffCursor != 0 {
		t.Fatalf("click: top %d, cursor %d", m.diffViewTop, m.diffCursor)
	}

	m.dragScrollbar(scrollDiff, lay.diff, m.diffHeader(), lay.diff.y+lay.diff.h-1)
	if m.diffViewTop != v.maxTop() {
		t.Fatalf("scrollbar top %d, want %d", m.diffViewTop, v.maxTop())
	}
	pane := renderDiff(m.diffLines, m.diffCursor, m.diffViewTop, lay.diff.w, lay.diff.h, true, "diff", "", lineSpan{})
	if !strings.Contains(ansi.Strip(pane), "END") {
		t.Fatalf("end of line missing:\n%s", ansi.Strip(pane))
	}
	if got := v.lineAt(m.diffViewTop, 0); got != 0 {
		t.Fatalf("continuation belongs to line %d", got)
	}
}

func TestDiffRowsPreserveGraphemes(t *testing.T) {
	for _, source := range []string{"❤️", "hello ❤️", "👨‍👩‍👧‍👦", "ab❤️cd❤️ef", "ab👨‍👩‍👧‍👦cd", "e\u0301漢字❤️"} {
		for _, width := range []int{4, 7, 40} {
			for _, kind := range []byte{'+', '-', ' '} {
				text := source
				if kind != ' ' {
					text = string(kind) + text
				}
				rows := diffRows(diffLine{kind: kind, text: text}, width)
				var joined strings.Builder
				for _, row := range rows {
					plain := ansi.Strip(row)
					limit := width
					if kind != ' ' {
						plain = plain[1:]
						limit++
					}
					if ansi.StringWidth(row) > limit {
						t.Fatalf("row exceeds width: %q", row)
					}
					joined.WriteString(strings.TrimRight(plain, " "))
				}
				// Trailing spaces at a wrap boundary are layout, so compare without
				// spaces; every grapheme itself must survive unchanged.
				if got, want := strings.ReplaceAll(joined.String(), " ", ""), strings.ReplaceAll(source, " ", ""); got != want {
					t.Errorf("kind %c width %d: %q became %q", kind, width, source, joined.String())
				}
			}
		}
	}
}
