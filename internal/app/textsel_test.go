package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestTextSelKeepsWhatTheWrapTookOut(t *testing.T) {
	for _, text := range []string{
		"error: the agent did not start\n\nclaude: command not found in PATH abcdefghijklmnopqrstuvwxyz",
		"hello\u2003world abcdefghijklmnopqrstuvwxyz",
		"hello\tworld abcdefghijklmnopqrstuvwxyz",
		"  indented, then a word too long: abcdefghijklmnopqrstuvwxyz  \nend",
	} {
		var s textSel
		s.setText(text, 10)
		s.on, s.from = true, selPoint{0, 0}
		s.to = selPoint{len(s.rows) - 1, 1 << 20}
		if got := s.selected(); got != text {
			var rows []string
			for _, r := range s.rows {
				rows = append(rows, r.drawn)
			}
			t.Errorf("all of it selected, copied:\n%q\nnot\n%q (rows %q)", got, text, rows)
		}
		for _, r := range s.rows {
			if w := ansi.StringWidth(r.drawn); w > 10 || w != r.width() {
				t.Errorf("row %q of %d cells, %d noted", r.drawn, w, r.width())
			}
		}
	}
}

// screenAt is the last place the screen shows s: below the list, in the
// dialog, the prompt picked.
func screenAt(t *testing.T, m *Model, s string) (int, int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for y := len(lines) - 1; y >= 0; y-- {
		if i := strings.Index(lines[y], s); i >= 0 {
			return ansi.StringWidth(lines[y][:i]), y
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", s, ansi.Strip(m.View()))
	return 0, 0
}

func TestSelectTextInTheSentDialog(t *testing.T) {
	m := testModel(t)
	var copied string
	was := toClipboard
	toClipboard = func(s string) bool { copied = s; return true }
	defer func() { toClipboard = was }()
	tb := m.cur()
	m.setLast(tb, "codex failed to start:\nerror: unknown flag --sandbox-mode")
	m.openSentOf(tb)

	x, y := screenAt(t, m, "error: unknown")
	m.Update(tea.MouseMsg{X: x + 7, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: x + 19, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m.View()
	m.Update(tea.MouseMsg{X: x + 19, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if copied != "unknown flag " {
		t.Fatalf("copied %q", copied)
	}
	if m.dialog == nil || m.drag != dragNone {
		t.Fatal("the dialog should stay open, and the drag end")
	}

	// From a row above, up: the rows between, and the newline.
	fx, fy := screenAt(t, m, "to start:")
	m.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: fx, Y: fy, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if copied != "to start:\nerror" {
		t.Fatalf("copied %q", copied)
	}

	copied = ""
	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if copied != "" {
		t.Fatalf("a click alone copies nothing, not %q", copied)
	}

	// A tab is drawn as spaces; what is under the pointer is copied.
	m.dialog = nil
	m.setLast(tb, "prefix\talpha beta")
	m.openSentOf(tb)
	x, y = screenAt(t, m, "alpha beta")
	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if copied != "alpha" {
		t.Fatalf("over a tab, copied %q", copied)
	}
	x, _ = screenAt(t, m, "prefix")
	m.Update(tea.MouseMsg{X: x + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: x + 11, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if copied != "fix\tal" {
		t.Fatalf("the tab itself, copied %q", copied)
	}

	m.Update(keyMsg("c"))
	if copied != "prefix\talpha beta" || m.dialog == nil {
		t.Fatalf("c copies the prompt picked, and leaves the dialog open: %q", copied)
	}
}
