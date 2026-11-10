package app

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/awake"
	"github.com/nerdr-dev/nerdr/internal/core"
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

// keeper is what keeps the screen on, the client's own: awake.Keeper but in
// tests.
type keeper interface {
	Set(sleep, screen bool)
	Err() string
}

var newScreenKeeper = func() keeper { return awake.New() }

// keepScreen keeps the screen in front of you on and unlocked, with
// keep_screen_on, while a machine's core keeps its machine awake for the
// agents (core.KeptAwake): until keep_screen_max minutes after your last
// key or click. The screen is the one where the TUI runs, which is not the
// core's when that is on another machine.
func (m *Model) keepScreen(now time.Time) {
	kept := false
	for _, mc := range m.fleet.Machines() {
		kept = kept || mc.Core.KeptAwake
	}
	most := time.Duration(max(m.cfg().KeepScreenMax, 1)) * time.Minute
	want := kept && core.IsOn(m.cfg().KeepScreenOn) && now.Sub(m.lastInput(now)) < most
	if m.screenKeeper == nil {
		if !want {
			return
		}
		m.screenKeeper = newScreenKeeper()
	}
	m.screenKeeper.Set(want, want) // a screen kept on is a machine kept awake
	if err := m.screenKeeper.Err(); err != m.screenErr {
		m.screenErr = err
		if err != "" {
			m.toast("keep_screen_on: " + err)
		}
	}
}
