package app

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
)

type fakeKeeper struct{ sleep, screen bool }

func (k *fakeKeeper) Set(sleep, screen bool) { k.sleep, k.screen = sleep, screen }
func (k *fakeKeeper) Err() string            { return "" }

// TestKeepAwake covers keep_awake: busy holds the machine awake while an
// agent works and a minute after, always all the time, off never; and
// nothing is held, or made, before it is first wanted.
func TestKeepAwake(t *testing.T) {
	k := &fakeKeeper{}
	old := newKeeper
	newKeeper = func() keeper { return k }
	defer func() { newKeeper = old }()

	m := testModel(t)
	m.cfg.KeepAwake = config.KeepBusy
	now := time.Now()
	m.keepAwake(now)
	if m.keeper != nil {
		t.Fatal("a keeper was made with no agent working")
	}
	m.tabs[1].status = stWorking
	m.keepAwake(now)
	if !k.sleep || k.screen {
		t.Fatalf("an agent works: %+v", k)
	}
	m.tabs[1].status = stDone
	m.keepAwake(now.Add(30 * time.Second))
	if !k.sleep {
		t.Fatal("let go at once after the turn")
	}
	m.keepAwake(now.Add(awakeAfter + time.Second))
	if k.sleep {
		t.Fatal("still held a minute after")
	}
	on := true
	m.cfg.KeepScreenOn = &on
	m.cfg.KeepAwake = config.KeepAlways
	m.keepAwake(now.Add(time.Hour))
	if !k.sleep || !k.screen {
		t.Fatalf("always, with the screen: %+v", k)
	}
	// The screen is let go of keep_screen_max after the last input, the
	// machine still kept awake.
	m.cfg.KeepScreenMax = 60
	m.keepAwake(m.started.Add(61 * time.Minute))
	if !k.sleep || k.screen {
		t.Fatalf("an hour with no input: %+v", k)
	}

	m.cfg.KeepAwake = "off"
	m.tabs[0].status = stWorking
	m.keepAwake(now.Add(time.Hour))
	if k.sleep {
		t.Fatal("off holds nothing")
	}
}
