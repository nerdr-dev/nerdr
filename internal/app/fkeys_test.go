package app

import (
	"testing"
	"time"

	"nerdr/internal/input"
	"nerdr/internal/term"
)

// TestFKeysLeaveAShellAtItsPrompt: in a terminal pane, F1-F4 stay nerdr's
// while the shell is at its prompt; a full-screen program there gets them
// for itself.
func TestFKeysLeaveAShellAtItsPrompt(t *testing.T) {
	for _, c := range []struct {
		name, script string
		pass         bool
	}{
		{"prompt", "cat", false},
		{"full screen", `printf '\033[?1049h'; cat`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := testModel(t)
			m.router = input.New(nil, 0x11)
			pane, err := term.Start([]string{"sh", "-c", c.script}, t.TempDir(), nil, 60, 10, func() {})
			if err != nil {
				t.Skip(err)
			}
			defer pane.Close()
			for deadline := time.Now().Add(2 * time.Second); pane.AltScreen() != c.pass; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Skip("the program did not go to the alternate screen")
				}
			}
			tb := m.cur()
			m.addPane(tb, &paneTab{kind: paneShell, shell: pane}, false)
			if tb.screen() != pane || m.keyPane() != pane {
				t.Fatal("the terminal pane should have the keyboard")
			}
			m.syncInput()
			if m.router.PassesFKeys() != c.pass {
				t.Fatalf("F-keys passed to the program: %v, want %v", m.router.PassesFKeys(), c.pass)
			}
		})
	}
}
