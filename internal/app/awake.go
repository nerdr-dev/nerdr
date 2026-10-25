package app

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/awake"
	"github.com/nerdr-dev/nerdr/internal/config"
)

// keeper is what holds the machine awake; awake.Keeper but in tests.
type keeper interface {
	Set(sleep, screen bool)
	Err() string
}

var newKeeper = func() keeper { return awake.New() }

// awakeAfter is how long the machine is kept awake after the last agent
// stopped working: the messages queued for it go in once it is idle, and
// it works again.
const awakeAfter = time.Minute

// keepAwake keeps the machine from sleeping, and with keep_screen_on the
// screen on, while keep_awake asks: while an agent works, or always.
func (m *Model) keepAwake(now time.Time) {
	if m.busy() {
		m.lastBusy = now
	}
	want := false
	switch m.cfg.KeepAwake {
	case config.KeepAlways:
		want = true
	case config.KeepBusy:
		want = !m.lastBusy.IsZero() && now.Sub(m.lastBusy) < awakeAfter
	}
	if m.keeper == nil {
		if !want {
			return
		}
		m.keeper = newKeeper()
	}
	m.keeper.Set(want, isOn(m.cfg.KeepScreenOn) && now.Sub(m.lastInput(now)) < m.screenMax())
	if err := m.keeper.Err(); err != m.keeperErr {
		m.keeperErr = err
		if err != "" {
			m.toast("keep_awake: " + err)
		}
	}
}

// busy is whether an agent works, in a tab or reviewing one.
func (m *Model) busy() bool {
	for _, t := range m.tabs {
		if t.status == stWorking {
			return true
		}
		if t.review != nil {
			for _, s := range t.review.seats {
				if s.r.status == stWorking {
					return true
				}
			}
		}
	}
	return false
}

// screenMax is how long after the last input the screen is kept on.
func (m *Model) screenMax() time.Duration {
	return time.Duration(max(m.cfg.KeepScreenMax, 1)) * time.Minute
}

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
