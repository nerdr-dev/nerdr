package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/awake"
	"github.com/nerdr-dev/nerdr/internal/config"
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

// KeepAwake keeps the machine from sleeping while keep_awake asks: while an
// agent works, or always. KeptAwake says whether it does, for a client to
// keep its own screen on by (keep_screen_on, the client's setting): the
// screen is the user's, where the client runs, which may be another
// machine.
func (m *Core) KeepAwake(now time.Time) {
	if m.Busy() {
		m.LastBusy = now
	}
	want := false
	switch m.Cfg.KeepAwake {
	case config.KeepAlways:
		want = true
	case config.KeepBusy:
		want = !m.LastBusy.IsZero() && now.Sub(m.LastBusy) < AwakeAfter
	}
	m.KeptAwake = want
	if m.Awake == nil {
		if !want {
			return
		}
		m.Awake = NewKeeper()
	}
	m.Awake.Set(want, false)
	if err := m.Awake.Err(); err != m.AwakeErr {
		m.AwakeErr = err
		if err != "" {
			m.Toast("keep_awake: " + err)
		}
	}
}
