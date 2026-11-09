package app

import "github.com/nerdr-dev/nerdr/internal/core"

// A test's model has no loop: what a request leaves to core's is run at
// once, with Mu held as the TUI holds it, and what comes of it handled as
// the loop would.
func init() { runCoreNow = runNow }

func runNow(m *Model, c core.Cmd) {
	if c == nil {
		return
	}
	switch ev := c().(type) {
	case nil:
	case core.BatchEvent:
		for _, c := range ev {
			runNow(m, c)
		}
	default:
		next, _ := m.home().Core.Handle(ev)
		runNow(m, next)
	}
}
