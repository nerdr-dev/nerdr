package review

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Lines of the diff are chosen by dragging over them with the mouse, and
// copied when the button is let go: nerdr has the mouse, so the
// terminal's own selection would take the gutter and the pane's frame
// with them. A click alone chooses nothing; it puts the cursor there, as
// it always has.

// lineSpan is the diff's lines from one to another, either way round;
// the zero one has none.
type lineSpan struct {
	from, to int
	on       bool
}

func (s lineSpan) has(i int) bool {
	return s.on && i >= min(s.from, s.to) && i <= max(s.from, s.to)
}

// diffArea is where the diff's lines are on screen in l, and the rows of
// its own above them; ok is false when the diff is not shown.
func (m *Model) diffArea(l paneLayout) (r rect, header int, ok bool) {
	switch {
	case l.diff.w > 0:
		return l.diff, m.diffHeader(), true
	case l.zoom.w > 0 && m.focus == focusDiff:
		return l.zoom, 0, true
	}
	return rect{}, 0, false
}

// startSelect begins choosing lines at the one pressed on, if it is one.
func (m *Model) startSelect(l paneLayout, x, y int) {
	r, header, ok := m.diffArea(l)
	if !ok || !r.contains(x, y) {
		return
	}
	i := r.contentRow(y, header)
	if i < 0 {
		return
	}
	line := m.diffView().lineAt(m.diffViewTop, i)
	if line < 0 {
		return
	}
	m.diffSel = lineSpan{from: line, to: line}
	m.selecting, m.selRect, m.selHeader = true, r, header
}

// extendSelect takes the choice to the line at screen row y, scrolling the
// diff a row when y is past its top or bottom.
func (m *Model) extendSelect(y int) {
	v := m.diffView()
	i := y - m.selRect.y - 2 - m.selHeader
	switch {
	case i < 0:
		m.scrollDiffTo(m.diffViewTop - 1)
		i = 0
	case i >= v.innerH:
		m.scrollDiffTo(m.diffViewTop + 1)
		i = v.innerH - 1
	}
	line := v.lineAt(m.diffViewTop, i)
	if line < 0 {
		line = len(m.diffLines) - 1 // under the diff's end
	}
	m.diffSel.to = line
	m.diffSel.on = m.diffSel.to != m.diffSel.from || m.diffSel.on
	m.setDiffCursor(line)
}

// endSelect copies the lines chosen, once the button is let go; they stay
// shown as chosen until the next click or key.
func (m *Model) endSelect() tea.Cmd {
	if !m.selecting {
		return nil
	}
	m.selecting = false
	if !m.diffSel.on {
		m.diffSel = lineSpan{}
		return nil
	}
	text := m.selText()
	if text == "" {
		return nil
	}
	return func() tea.Msg { return CopyMsg{Text: text} }
}

// selText is the code of the lines chosen, as it is in the file: without
// the diff's +, - and space in front, and without the lines of the diff's
// own (hunk heads, references, rationale).
func (m *Model) selText() string {
	var out []string
	for i, dl := range m.diffLines {
		if !m.diffSel.has(i) {
			continue
		}
		switch dl.kind {
		case '+', '-', ' ':
			if dl.text != "" {
				out = append(out, dl.text[1:])
			}
		}
	}
	return strings.Join(out, "\n")
}
