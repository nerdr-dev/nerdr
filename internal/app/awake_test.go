package app

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
)

type fakeKeeper struct{ sleep, screen bool }

func (k *fakeKeeper) Set(sleep, screen bool) { k.sleep, k.screen = sleep, screen }
func (k *fakeKeeper) Err() string            { return "" }

// TestKeepAwake covers keep_awake: busy holds the machine awake while an
// agent works and a minute after, always all the time, off never; and
// nothing is held, or made, before it is first wanted.
func TestKeepAwake(t *testing.T) {
	k := &fakeKeeper{}
	old := core.NewKeeper
	core.NewKeeper = func() core.Keeper { return k }
	defer func() { core.NewKeeper = old }()

	m := testModel(t)
	m.home().Core.Cfg.KeepAwake = config.KeepBusy
	now := time.Now()
	m.home().Core.KeepAwake(now, func() time.Time { return m.lastInput(now) })
	if m.home().Core.Awake != nil {
		t.Fatal("a keeper was made with no agent working")
	}
	m.home().Core.Tabs[1].Light = core.StWorking
	m.home().Core.KeepAwake(now, func() time.Time { return m.lastInput(now) })
	if !k.sleep || k.screen {
		t.Fatalf("an agent works: %+v", k)
	}
	m.home().Core.Tabs[1].Light = core.StDone
	m.home().Core.KeepAwake(now.Add(30*time.Second), func() time.Time { return m.lastInput(now.Add(30 * time.Second)) })
	if !k.sleep {
		t.Fatal("let go at once after the turn")
	}
	m.home().Core.KeepAwake(now.Add(core.AwakeAfter+time.Second), func() time.Time { return m.lastInput(now.Add(core.AwakeAfter + time.Second)) })
	if k.sleep {
		t.Fatal("still held a minute after")
	}
	on := true
	m.home().Core.Cfg.KeepScreenOn = &on
	m.home().Core.Cfg.KeepAwake = config.KeepAlways
	m.home().Core.KeepAwake(now.Add(time.Hour), func() time.Time { return m.lastInput(now.Add(time.Hour)) })
	if !k.sleep || !k.screen {
		t.Fatalf("always, with the screen: %+v", k)
	}
	// The screen is let go of keep_screen_max after the last input, the
	// machine still kept awake.
	m.home().Core.Cfg.KeepScreenMax = 60
	m.home().Core.KeepAwake(m.started.Add(61*time.Minute), func() time.Time { return m.lastInput(m.started.Add(61 * time.Minute)) })
	if !k.sleep || k.screen {
		t.Fatalf("an hour with no input: %+v", k)
	}

	m.home().Core.Cfg.KeepAwake = "off"
	m.home().Core.Tabs[0].Light = core.StWorking
	m.home().Core.KeepAwake(now.Add(time.Hour), func() time.Time { return m.lastInput(now.Add(time.Hour)) })
	if k.sleep {
		t.Fatal("off holds nothing")
	}
}
