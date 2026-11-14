package app

import (
	"time"
)

// herdr's agent integrations, when installed, report to nerdr (see
// internal/hooks): the exact conversation a tab's agent is in, and for some
// agents its state, which then counts for more than what the screen shows.

// Notice shows a message for longer than a toast: for what went wrong
// before the screen was up.
func (m *Model) Notice(s string) { m.say(catWarning, s, 20*time.Second) }
