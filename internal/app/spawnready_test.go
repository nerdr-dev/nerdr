package app

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// startAsked runs cmd, what started an agent, and when its CLI was to be
// asked something first (see core.Spawn), hands m the answer, which starts
// it; one with nothing to ask started at once. What else cmd does is left
// to run on its own: a tick could sleep.
func startAsked(t *testing.T, m *Model, cmd core.Cmd) {
	t.Helper()
	ready := make(chan core.SpawnReady, 1)
	var run func(c core.Cmd)
	run = func(c core.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch ev := c().(type) {
			case core.BatchEvent:
				for _, c := range ev {
					run(c)
				}
			case core.SpawnReady:
				ready <- ev
			}
		}()
	}
	run(cmd)
	select {
	case ev := <-ready:
		m.Update(ev)
	case <-time.After(2 * time.Second):
	}
}
