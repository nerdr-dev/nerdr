package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// quitDialog opens the dialog of ctrl+q q and returns what it shows.
func quitDialog(t *testing.T, m *Model) string {
	t.Helper()
	var view string
	locked(m, func() {
		m.runCommand("q")
		if _, ok := m.dialog.(closeOrQuit); !ok {
			t.Fatalf("ctrl+q q opened %T", m.dialog)
		}
		view = ansi.Strip(m.dialog.view(m, 200, 40))
	})
	return view
}

func quitting(c *core.Core) bool {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	return c.Quitting
}

// TestQuitHereLeavesServers: with a core on this machine and one on a
// server, the dialog names both for quit, and quit here stops only this
// machine's, the server's working on.
func TestQuitHereLeavesServers(t *testing.T) {
	m, here, box, _, _ := twoMachines(t)
	locked(m, func() { m.fleet.Named("box").Host = "box.example" })
	view := quitDialog(t, m)
	for _, want := range []string{
		"Quit: the agents are stopped, on this machine, box.",
		"Quit here: the agents are stopped on this machine;",
		"those on box work on.",
		"l quit here",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("no %q in the dialog:\n%s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !quitting(here) {
		t.Fatal("this machine's core was not quit")
	}
	if quitting(box) {
		t.Fatal("the server's core was quit")
	}
}

// TestQuitNamesTheServer: a client of a core on a server alone, as over
// nerdr bridge, is told the server's agents are stopped by quit, the
// server named; quit here is not offered, close being that.
func TestQuitNamesTheServer(t *testing.T) {
	m, c, _, _ := remoteTUI(t, "sh", "-c", "sleep 30")
	locked(m, func() { m.SetCoreHost("tor-m5-ultra-r") })
	view := quitDialog(t, m)
	for _, want := range []string{
		"Close: the agents work on, on tor-m5-ultra-r,",
		"Quit: the agents are stopped, on tor-m5-ultra-r.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("no %q in the dialog:\n%s", want, view)
		}
	}
	if strings.Contains(view, "quit here") {
		t.Fatalf("quit here offered with no machine here:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if m.dialog == nil || quitting(c) {
		t.Fatal("l did something with no quit here offered")
	}
}

// TestQuitHereInProcess: the core of the TUI's own, with a server, asks
// too, and quit here sends the server's core nothing.
func TestQuitHereInProcess(t *testing.T) {
	m := paneModel(t)
	box, _, dial := boxCore(t, "sh", "-c", "sleep 30")
	conn, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := m.addMachine("box", "box.example", conn, nil, func(any) {})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != nil {
		m.Update(cmd())
	}
	view := quitDialog(t, m)
	if strings.Contains(view, "close") || !strings.Contains(view, "those on box work on.") {
		t.Fatalf("the dialog:\n%s", view)
	}
	_, stop := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if stop == nil {
		t.Fatal("quit here did not stop this machine's agents")
	}
	stop()
	if quitting(box) {
		t.Fatal("the server's core was quit")
	}
}

// TestQuitYIsNotAClientsKey: y, the simple question's yes, does nothing
// in a client's dialog, which offers no yes.
func TestQuitYIsNotAClientsKey(t *testing.T) {
	m, c, _, _ := remoteTUI(t, "sh", "-c", "sleep 30")
	quitDialog(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.dialog == nil || quitting(c) {
		t.Fatal("y closed the dialog or quit the core")
	}
}

// TestQuitConfirmLeavesServerBack: the simple question, asked with the
// server away, stops this machine's agents only, the server connected
// again meanwhile working on: the question did not name it.
func TestQuitConfirmLeavesServerBack(t *testing.T) {
	m := paneModel(t)
	box, _, dial := boxCore(t, "sh", "-c", "sleep 30")
	conn, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := m.addMachine("box", "box.example", conn, nil, func(any) {})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != nil {
		m.Update(cmd())
	}
	locked(m, func() {
		m.fleet.Named("box").Away = true
		m.runCommand("q")
		if _, ok := m.dialog.(*confirm); !ok {
			t.Fatalf("ctrl+q q opened %T with the server away", m.dialog)
		}
		m.fleet.Named("box").Away = false
	})
	_, stop := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if stop == nil {
		t.Fatal("enter did not quit")
	}
	stop()
	if quitting(box) {
		t.Fatal("the server, not named, was quit")
	}
}
