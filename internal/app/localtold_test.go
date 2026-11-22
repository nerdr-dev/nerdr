package app

import (
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"

	tea "github.com/charmbracelet/bubbletea"
)

// TestToldPanicIsRaised: with core in the TUI's own process
// (NERDR_HOLDER=0), what its loop hands the TUI comes as the home
// machine's; a panic on core's goroutines is still raised in Update.
func TestToldPanicIsRaised(t *testing.T) {
	m := testModel(t)
	defer func() {
		v := recover()
		if s, _ := v.(string); !strings.Contains(s, "boom") {
			t.Fatalf("raised %v", v)
		}
	}()
	m.Update(fleet.Told{Msg: core.PanicMsg{Value: "boom", Stack: []byte("stack")}})
	t.Fatal("a panic of core's was let go")
}

// TestWirePanicIsShown: a core in another process that panicked is said,
// and the TUI goes on.
func TestWirePanicIsShown(t *testing.T) {
	m := testModel(t)
	m.Update(fleet.Told{Msg: core.WirePanic{Value: "boom", Stack: []byte("stack")}})
	if !strings.Contains(m.toastText, "panicked: boom") {
		t.Fatalf("bar says %q", m.toastText)
	}
}

// TestToldBatchIsRun: a Cmd of the TUI's that core's loop ran in the
// TUI's process returns a tea.BatchMsg, which comes back as the home
// machine's; what is in it is still run.
func TestToldBatchIsRun(t *testing.T) {
	m := testModel(t)
	ran := 0
	sub := func() tea.Msg { ran++; return nil }
	ev := toCore(tea.Batch(sub, sub))()
	if _, ok := ev.(tea.BatchMsg); !ok {
		t.Fatalf("toCore ran to %T", ev)
	}
	_, cmd := m.Update(fleet.Told{Msg: ev})
	if cmd == nil {
		t.Fatal("nothing to run")
	}
	// What Bubble Tea is given is the batch itself, as a message of its
	// own, which it runs.
	b, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("handed Bubble Tea %T", cmd())
	}
	for _, c := range b {
		if c != nil {
			c()
		}
	}
	if ran != 2 {
		t.Fatalf("ran %d of 2", ran)
	}
}

// TestToldOwnMessageIsKept: a message of the TUI's own, not Bubble Tea's,
// is taken in Update as before.
func TestToldOwnMessageIsKept(t *testing.T) {
	if ofBubbleTea(core.ChangedMsg{}) || ofBubbleTea(nil) || !ofBubbleTea(tea.BatchMsg{}) || !ofBubbleTea(tea.ClearScreen()) {
		t.Fatal("ofBubbleTea is wrong")
	}
}
