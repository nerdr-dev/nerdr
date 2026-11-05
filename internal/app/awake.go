package app

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// keepAwake keeps the machine from sleeping, and with keep_screen_on the
// screen on, while keep_awake asks: while an agent works, or always.
func (m *Model) keepAwake(now time.Time) {
	if m.Busy() {
		m.lastBusy = now
	}
	want := false
	switch m.Cfg.KeepAwake {
	case config.KeepAlways:
		want = true
	case config.KeepBusy:
		want = !m.lastBusy.IsZero() && now.Sub(m.lastBusy) < core.AwakeAfter
	}
	if m.keeper == nil {
		if !want {
			return
		}
		m.keeper = core.NewKeeper()
	}
	m.keeper.Set(want, core.IsOn(m.Cfg.KeepScreenOn) && now.Sub(m.lastInput(now)) < m.ScreenMax())
	if err := m.keeper.Err(); err != m.keeperErr {
		m.keeperErr = err
		if err != "" {
			m.toast("keep_awake: " + err)
		}
	}
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
