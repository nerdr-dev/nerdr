package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/awake"
)

// Keeper is what holds the machine awake; awake.Keeper but in tests.
type Keeper interface {
	Set(sleep, screen bool)
	Err() string
}

var NewKeeper = func() Keeper { return awake.New() }

// AwakeAfter is how long the machine is kept awake after the last agent
// stopped working: the messages queued for it go in once it is idle, and
// it works again.
const AwakeAfter = time.Minute

// Busy is whether an agent works, in a tab or reviewing one.
func (m *Core) Busy() bool {
	for _, t := range m.Tabs {
		if t.Light.AtWork() {
			return true
		}
		if t.Review != nil {
			for _, s := range t.Review.Seats {
				if s.R.Light.AtWork() {
					return true
				}
			}
		}
	}
	return false
}

// ScreenMax is how long after the last input the screen is kept on.
func (m *Core) ScreenMax() time.Duration {
	return time.Duration(max(m.Cfg.KeepScreenMax, 1)) * time.Minute
}
