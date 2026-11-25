package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The palette finds a command by part of its name and runs it; the hint
// line in a group lists the group's keys.
func TestPaletteAndGroupHint(t *testing.T) {
	m := testModel(t)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg(":"))
	if _, ok := m.dialog.(*palette); !ok {
		t.Fatalf(": opened %T", m.dialog)
	}
	for _, r := range "settings" {
		m.Update(keyMsg(string(r)))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "ctrl+q s") {
		t.Fatalf("the palette does not show the keys:\n%s", view)
	}
	m.Update(keyMsg("esc"))

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("l"))
	m.prefixAt = m.prefixAt.Add(-hintDelay)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Swap sides") || strings.Contains(view, "New tab") {
		t.Fatalf("the hint in l lists the layout group's keys only:\n%s", view)
	}
}

// TestPaletteScrollsAtTheEdges: the palette's list, grouped like the
// menu, moves only when the cursor goes past the rows shown — up at the
// top, down at the bottom — not with every step.
func TestPaletteScrollsAtTheEdges(t *testing.T) {
	m := testModel(t)
	m.openPalette()
	p := m.dialog.(*palette)
	const room = 6
	rows := p.rows(p.matches())
	if rows[0].heading == "" {
		t.Fatal("the first row is its group's heading")
	}
	for range 10 {
		p.cursor++
		p.scroll(rows, room)
	}
	top := p.top
	if top == 0 {
		t.Fatal("going down past the bottom scrolls")
	}
	p.cursor--
	p.scroll(rows, room)
	if p.top != top {
		t.Fatalf("one up inside the window scrolled: top %d, was %d", p.top, top)
	}
	for p.cursor > 0 {
		p.cursor--
		p.scroll(rows, room)
	}
	if p.top != 0 {
		t.Fatalf("back at the first command, its heading shows: top %d", p.top)
	}
}
