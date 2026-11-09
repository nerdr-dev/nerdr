package app

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// startAsked runs cmd, what started an agent, and when its skills were to
// be looked at or its CLI asked something first (see core.Spawn), hands m
// what came of it, which starts it; one with nothing to ask started at
// once. What else cmd does is left
// to run on its own: a tick could sleep.
func startAsked(t *testing.T, m *Model, cmd core.Cmd) {
	t.Helper()
	ready := make(chan core.Event, 1)
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
			case core.SpawnReady, core.SpawnLooked:
				ready <- ev
			}
		}()
	}
	run(cmd)
	select {
	case ev := <-ready:
		m.home().Core.Mu.Lock()
		next, _ := m.home().Core.Handle(ev)
		m.home().Core.Mu.Unlock()
		if _, looked := ev.(core.SpawnLooked); looked {
			startAsked(t, m, next)
		}
	case <-time.After(2 * time.Second):
	}
}
