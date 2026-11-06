package app

import (
	"time"
)

// lastInput is when you last pressed a key or clicked in nerdr: since it
// started, until you have.
func (m *Model) lastInput(now time.Time) time.Time {
	if m.started.IsZero() {
		m.started = now
	}
	if m.router != nil {
		if t := m.router.LastInput(); t.After(m.started) {
			return t
		}
	}
	return m.started
}
