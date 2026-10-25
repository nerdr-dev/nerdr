package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// An agent on the alternate screen keeps no scrollback for nerdr: in the
// scrollback, the arrows and page keys go to it, as a terminal sends the
// wheel and the page keys there, rather than doing nothing.
func TestScrollbackOfAFullScreenAgent(t *testing.T) {
	m := testModel(t)
	login := m.cur()
	pane, err := term.Start([]string{"sh", "-c", `printf '\033[?1049h'; stty raw -echo; cat -v`}, t.TempDir(), nil, 60, 10, func() {})
	if err != nil {
		t.Skip(err)
	}
	defer pane.Close()
	login.pane, login.status = pane, stIdle
	for deadline := time.Now().Add(2 * time.Second); !pane.AltScreen(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Skip("the program did not go to the alternate screen")
		}
	}
	time.Sleep(50 * time.Millisecond) // stty
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.Update(keyMsg("b"))
	if !m.scrolling || !strings.Contains(ansi.Strip(m.View()), "own screen") {
		t.Fatalf("b should scroll the agent's own screen: scrolling %v", m.scrolling)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	deadline := time.Now().Add(2 * time.Second)
	for !(strings.Contains(pane.Snapshot().Screen, "^[[A") && strings.Contains(pane.Snapshot().Screen, "^[[5~")) {
		if time.Now().After(deadline) {
			t.Fatalf("the agent should get the arrow and page up:\n%s", pane.Snapshot().Screen)
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.Update(keyMsg("q"))
	if m.scrolling {
		t.Fatal("q leaves")
	}
}
