package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// pointerLog has the shapes asked of the terminal recorded, for the test.
func pointerLog(t *testing.T) *[]string {
	sent := &[]string{}
	was := setPointer
	setPointer = func(s string) { *sent = append(*sent, s) }
	t.Cleanup(func() { setPointer = was })
	return sent
}

// at is where the screen first shows s.
func at(t *testing.T, m *Model, s string) (x, y int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, s); i >= 0 {
			return ansi.StringWidth(line[:i]), y
		}
	}
	t.Fatalf("%q is not on the screen", s)
	return 0, 0
}

func lastShape(sent []string) string {
	if len(sent) == 0 {
		return ""
	}
	return sent[len(sent)-1]
}

// A screen that changes under a pointer that stays put changes its shape:
// a menu closed with Esc takes the hand away with it.
func TestPointerFollowsTheScreen(t *testing.T) {
	sent := pointerLog(t)
	m := testModel(t)
	clickOn(t, m, "logout", tea.MouseButtonRight)
	mu, ok := m.dialog.(*menu)
	if !ok {
		t.Fatalf("dialog %T after a right-click on a tab", m.dialog)
	}
	x, y := at(t, m, mu.items[1].label)
	m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion})
	m.View()
	if lastShape(*sent) != pointerHand {
		t.Fatalf("no hand over a menu item: %q", *sent)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.View()
	if m.dialog != nil {
		t.Fatalf("still open: %T", m.dialog)
	}
	if lastShape(*sent) != pointerDefault {
		t.Fatalf("hand left over what the menu was drawn on: %q", *sent)
	}
}

// While a drag goes on the pointer keeps its shape; where the drag ends
// gives it its shape again.
func TestPointerAfterADrag(t *testing.T) {
	sent := pointerLog(t)
	m := testModel(t)
	m.runCommand("l v") // two tabs, the rule between them
	x, y := at(t, m, "logout")
	m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion})
	m.View()
	if lastShape(*sent) != pointerHand {
		t.Fatalf("no hand over a tab: %q", *sent)
	}
	aw := m.agentWidth()
	m.Update(tea.MouseMsg{X: aw, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.View()
	m.Update(tea.MouseMsg{X: 60, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m.View()
	if n := len(*sent); n != 1 {
		t.Fatalf("shape changed during the drag: %q", *sent)
	}
	m.Update(tea.MouseMsg{X: 60, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	m.View()
	if lastShape(*sent) != pointerDefault {
		t.Fatalf("hand kept after a drag ending in a pane: %q", *sent)
	}
}

// The pointer is a hand over what can be clicked, and the terminal is
// asked only when that changes.
func TestPointerHand(t *testing.T) {
	log := pointerLog(t)

	m := &Model{}
	m.home().Core.Client = m
	row := "ab" + m.zone("cd", func(*Model, tea.MouseMsg) tea.Cmd { return nil }) + m.tip("ef", "a tip") + m.area("  "+m.zone("gh", func(*Model, tea.MouseMsg) tea.Cmd { return nil })+"  ", func(*Model, tea.MouseMsg) tea.Cmd { return nil })
	_, m.zoneRects = scanZones(row)

	m.mouse = mouseAll
	move := func(x int) {
		m.hover.x, m.hover.y, m.hover.seen = x, 0, true
		m.syncPointer()
	}
	move(0) // already the terminal's own: nothing sent
	move(2)
	move(3) // still on it
	move(4) // a tip alone is not something to click
	move(5)
	move(2)
	move(6) // a dialog's box, say: it takes the click, but is no button
	move(8) // the button in it
	sent := *log
	want := []string{pointerHand, pointerDefault, pointerHand, pointerDefault, pointerHand}
	if len(sent) != len(want) {
		t.Fatalf("sent %q, want %q", sent, want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Fatalf("sent %q, want %q", sent, want)
		}
	}

}

// An editor opened in the middle of a drag takes the hand away, though
// the drag is not over.
func TestPointerEditorDuringADrag(t *testing.T) {
	sent := pointerLog(t)
	m := testModel(t)
	x, y := at(t, m, "logout")
	m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion})
	m.View()
	if lastShape(*sent) != pointerHand {
		t.Fatalf("no hand over a tab: %q", *sent)
	}
	m.drag = dragSide
	m.editor = &term.Pane{Screen: term.NewScreen(120, 30)}
	m.syncMouse() // as Update does
	m.View()
	if lastShape(*sent) != pointerDefault {
		t.Fatalf("hand kept over the editor: %q", *sent)
	}
}
