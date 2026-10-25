package review

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Dragging over the diff's lines chooses them, and letting go copies their
// code, without the diff's marks or its own lines; a click alone copies
// nothing.
func TestDragCopiesTheDiffLines(t *testing.T) {
	lines := []diffLine{
		{kind: '@', text: "@@ -1,3 +1,3 @@"},
		{kind: ' ', text: " func a() {", newLine: 1},
		{kind: '-', text: "-\treturn 1"},
		{kind: '+', text: "+\treturn 2", newLine: 2},
		{kind: ' ', text: " }", newLine: 3},
	}
	m := Model{width: 120, height: 40, focus: focusFiles, diffLines: lines}
	lay := m.layout(m.width, m.height)
	row := func(i int) int { return lay.diff.y + 2 + m.diffHeader() + i }
	send := func(y int, a tea.MouseAction) tea.Cmd {
		nm, cmd := m.Update(tea.MouseMsg{X: lay.diff.x + 5, Y: y, Button: tea.MouseButtonLeft, Action: a})
		m = nm.(Model)
		return cmd
	}

	send(row(1), tea.MouseActionPress)
	if cmd := send(row(1), tea.MouseActionRelease); cmd != nil || m.diffSel.on {
		t.Fatal("a click alone chooses and copies nothing")
	}

	send(row(0), tea.MouseActionPress)
	send(row(4), tea.MouseActionMotion)
	if !m.diffSel.has(2) || !m.diffSel.has(4) {
		t.Fatalf("the lines dragged over are chosen: %+v", m.diffSel)
	}
	cmd := send(row(4), tea.MouseActionRelease)
	if cmd == nil {
		t.Fatal("letting go copies")
	}
	got, ok := cmd().(CopyMsg)
	if want := "func a() {\n\treturn 1\n\treturn 2\n}"; !ok || got.Text != want {
		t.Fatalf("copied %q, want %q", got.Text, want)
	}
	if !m.diffSel.on {
		t.Fatal("the lines stay shown as chosen once copied")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if nm.(Model).diffSel.on {
		t.Fatal("a key lets the choice go")
	}
}
