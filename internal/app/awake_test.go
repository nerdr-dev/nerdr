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
// nothing is held, or made, before it is first wanted. Core keeps no
// screen on: that is the client's (TestKeepScreen).
func TestKeepAwake(t *testing.T) {
	k := &fakeKeeper{}
	old := core.NewKeeper
	core.NewKeeper = func() core.Keeper { return k }
	defer func() { core.NewKeeper = old }()

	m := testModel(t)
	c := &m.home().Core
	c.Cfg.KeepAwake = config.KeepBusy
	on := true
	c.Cfg.KeepScreenOn = &on
	now := time.Now()
	c.KeepAwake(now)
	if c.Awake != nil || c.KeptAwake {
		t.Fatal("a keeper was made with no agent working")
	}
	c.Tabs[1].Light = core.StWorking
	c.KeepAwake(now)
	if !k.sleep || k.screen || !c.KeptAwake {
		t.Fatalf("an agent works: %+v", k)
	}
	c.Tabs[1].Light = core.StDone
	c.KeepAwake(now.Add(30 * time.Second))
	if !k.sleep {
		t.Fatal("let go at once after the turn")
	}
	c.KeepAwake(now.Add(core.AwakeAfter + time.Second))
	if k.sleep || c.KeptAwake {
		t.Fatal("still held a minute after")
	}
	c.Cfg.KeepAwake = config.KeepAlways
	c.KeepAwake(now.Add(time.Hour))
	if !k.sleep || k.screen {
		t.Fatalf("always: %+v", k)
	}

	c.Cfg.KeepAwake = "off"
	c.Tabs[0].Light = core.StWorking
	c.KeepAwake(now.Add(time.Hour))
	if k.sleep {
		t.Fatal("off holds nothing")
	}
}

// TestKeepScreen covers keep_screen_on: the client keeps its own screen on
// while a machine's core keeps its machine awake, until keep_screen_max
// after the last input; and makes nothing before it is first wanted.
func TestKeepScreen(t *testing.T) {
	k := &fakeKeeper{}
	old := newScreenKeeper
	newScreenKeeper = func() keeper { return k }
	defer func() { newScreenKeeper = old }()

	m := testModel(t)
	now := time.Now()
	m.home().Core.KeptAwake = true
	m.keepScreen(now)
	if m.screenKeeper != nil {
		t.Fatal("a keeper was made with keep_screen_on off")
	}
	on := true
	m.cfg().KeepScreenOn = &on
	m.cfg().KeepScreenMax = 60
	m.home().Core.KeptAwake = false
	m.keepScreen(now)
	if m.screenKeeper != nil {
		t.Fatal("a keeper was made with no machine kept awake")
	}
	m.home().Core.KeptAwake = true
	m.keepScreen(now)
	if !k.screen {
		t.Fatalf("a machine kept awake: %+v", k)
	}
	m.keepScreen(m.started.Add(61 * time.Minute))
	if k.screen {
		t.Fatalf("an hour with no input: %+v", k)
	}
	m.keepScreen(now)
	m.home().Core.KeptAwake = false
	m.keepScreen(now)
	if k.screen {
		t.Fatal("kept on with no machine kept awake")
	}
}
