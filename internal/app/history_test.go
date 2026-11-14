package app

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// What the bar says is kept, the latest first, of the kind it is; the
// same again counts on the one before, and only the latest are kept.
func TestTheHistoryKeepsWhatTheBarSaid(t *testing.T) {
	m := testModel(t)
	m.toast("saved")
	m.Notice("the sandbox lets it see the folder")
	m.Notice("the sandbox lets it see the folder")
	h := m.history.entries
	if len(h) != 2 || h[0].cat != catWarning || h[0].n != 2 || h[1].cat != catInfo || h[1].text != "saved" {
		t.Fatalf("%+v", h)
	}
	for i := range historyCap + 5 {
		m.toast("toast " + strconv.Itoa(i))
	}
	if n := len(m.history.entries); n != historyCap || m.history.entries[0].text != "toast "+strconv.Itoa(historyCap+4) {
		t.Fatalf("%d kept, the latest %q", n, m.history.entries[0].text)
	}
}

// A message too long for the bar is cut there, with a link to F6, which
// shows it whole, picked.
func TestALongMessageLinksToTheHistory(t *testing.T) {
	m := testModel(t)
	m.toast("short, before")
	long := "copilot's sandbox lets it see into nerdr's client socket's folder (\"/Users/x/Library/Application Support/nerdr/run\"): " +
		strings.Repeat("it can tell what is there, ", 6) + "THE END."
	m.Notice(long)
	screen := ansi.Strip(m.View())
	if !strings.Contains(screen, "F6 full") || strings.Contains(screen, "THE END.") {
		t.Fatalf("not cut with a link:\n%s", screen)
	}
	clickOn(t, m, "F6 full", tea.MouseButtonLeft)
	if testing.Verbose() {
		t.Log("\n" + ansi.Strip(m.View()))
	}
	if !m.panelHistory() || m.history.entries[m.history.index()].text != long {
		t.Fatalf("history shown %v, picked %+v", m.panelHistory(), m.history.entries[m.history.index()])
	}
	if screen := ansi.Strip(m.View()); !strings.Contains(screen, "THE END.") {
		t.Fatalf("the whole message is not shown:\n%s", screen)
	}
	// ↓ picks the one before it.
	m.historyKey(keyMsg("down"))
	if e := m.history.entries[m.history.index()]; e.text != "short, before" {
		t.Errorf("picked %q", e.text)
	}
}

// A message that fits has no link, and F6 opens the history with the
// keyboard.
func TestF6OpensTheHistory(t *testing.T) {
	m := testModel(t)
	m.toast("fits")
	if screen := ansi.Strip(m.View()); !strings.Contains(screen, "fits") || strings.Contains(screen, "F6 full") {
		t.Fatalf("\n%s", screen)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF6})
	if !m.panelHistory() {
		t.Fatal("F6 did not open the history")
	}
	if screen := ansi.Strip(m.View()); !strings.Contains(screen, "history (1)") || !strings.Contains(screen, "info") {
		t.Fatalf("\n%s", screen)
	}
}
