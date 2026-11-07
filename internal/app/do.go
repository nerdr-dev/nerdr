package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// do has core do r: what the user did that changes core's state rather
// than only what the TUI shows. With core in this process it is done there
// and then, Mu held as in Update; see core.Request.
func (m *Model) do(r core.Request) tea.Cmd {
	return fromCore(r.Do(&m.Core))
}
