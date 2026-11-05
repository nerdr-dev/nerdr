package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
)

// fromCore is a Cmd of core's, to run in the TUI's event loop.
func fromCore(c core.Cmd) tea.Cmd {
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		ev := c()
		if b, ok := ev.(core.BatchEvent); ok {
			out := make(tea.BatchMsg, len(b))
			for i, cmd := range b {
				out[i] = fromCore(cmd)
			}
			return out
		}
		return ev
	}
}

// toCore is a Cmd of the TUI's, for core to run. What it returns is passed back
// in as it is, so a tea.BatchMsg still reaches the TUI's event loop.
func toCore(c tea.Cmd) core.Cmd {
	if c == nil {
		return nil
	}
	return func() core.Event { return c() }
}

// stopScrolling leaves the scrollback view.
func (m *Model) stopScrolling() { m.scrolling = false }

// dialogClosed reports whether no dialog is open, for core to open one.
func (m *Model) dialogClosed() bool { return m.dialog == nil }
