package core

import (
	"slices"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func portsCore(t *testing.T, base, n int) *Core {
	t.Helper()
	return &Core{Client: NoClient{}, Root: t.TempDir(), Cfg: config.Config{PortBase: base, PortsPerTab: &n}}
}

func portsTab(id, dir string) *Tab {
	return &Tab{AgentState: Agent{Tab: state.Tab{ID: id, Dir: dir, Agent: agents.Claude}}}
}

func TestTabsGetPortsOfTheirOwn(t *testing.T) {
	m := portsCore(t, 30000, 10)
	a, b := portsTab("a", "/w/a"), portsTab("b", "/w/b")
	m.givePorts(a)
	m.Tabs = append(m.Tabs, a)
	m.givePorts(b)
	m.Tabs = append(m.Tabs, b)
	if a.Ports != (workspace.Ports{First: 30000, Count: 10}) || b.Ports != (workspace.Ports{First: 30010, Count: 10}) {
		t.Fatalf("ports a %v, b %v", a.Ports, b.Ports)
	}
	// Given once, the range stays the tab's.
	m.givePorts(a)
	if a.Ports.First != 30000 {
		t.Fatalf("a's range changed to %v", a.Ports)
	}
	// Closed, a's range is free for the next.
	m.Tabs = m.Tabs[1:]
	c := portsTab("c", "/w/c")
	m.givePorts(c)
	if c.Ports.First != 30000 {
		t.Fatalf("c got %v, not a's freed range", c.Ports)
	}
}

func TestPortsRunOut(t *testing.T) {
	m := portsCore(t, 65530, 5)
	a, b := portsTab("a", "/w/a"), portsTab("b", "/w/b")
	m.givePorts(a)
	m.Tabs = append(m.Tabs, a)
	m.givePorts(b)
	if a.Ports.First != 65530 || !b.Ports.IsZero() {
		t.Fatalf("a %v, b %v; want b none past 65535", a.Ports, b.Ports)
	}
	if ans := m.tabPorts(b); ans.Err == "" {
		t.Fatalf("nerdr ports with none free: %+v", ans)
	}
}

func TestPortsHeldForTheTabBeingMade(t *testing.T) {
	m := portsCore(t, 30000, 5)
	held, release := m.HoldPorts("/w/new/")
	if held.IsZero() {
		t.Fatal("nothing held")
	}
	release(true)
	other := portsTab("o", "/w/other")
	m.givePorts(other)
	if other.Ports.Overlaps(held) {
		t.Fatalf("another tab got the held range %v", other.Ports)
	}
	m.Tabs = append(m.Tabs, other)
	made := portsTab("n", "/w/new")
	m.givePorts(made)
	if made.Ports != held {
		t.Fatalf("the tab made got %v, not the %v held for it", made.Ports, held)
	}
	if len(m.portHolds.by) != 0 {
		t.Fatalf("still held: %v", m.portHolds.by)
	}
}

func TestPortsOff(t *testing.T) {
	m := portsCore(t, 30000, 0)
	a := portsTab("a", "/w/a")
	m.givePorts(a)
	if p, _ := m.HoldPorts("/w/b"); !a.Ports.IsZero() || !p.IsZero() || m.portEnv(a) != nil {
		t.Fatalf("ports given with ports_per_tab 0: %v", a.Ports)
	}
	if ans := m.tabPorts(a); ans.Err == "" {
		t.Fatalf("nerdr ports with ports off: %+v", ans)
	}
}

func TestTabPortsForEveryAgentOfTheTab(t *testing.T) {
	m := portsCore(t, 30000, 10)
	a := portsTab("a", "/w/a")
	m.Tabs = []*Tab{a}
	// A reviewer, or an agent beside the tab's, has the tab's ports.
	r := portsTab("r", "/w/a")
	r.ReviewOf = a
	for _, x := range []*Tab{a, r} {
		if ans := m.tabPorts(x); ans.Err != "" || ans.Text != "30000 30009" {
			t.Errorf("%s: nerdr ports = %+v", x.ID, ans)
		}
		env := m.TabEnv(x)
		for _, want := range []string{"NERDR_PORT=30000", "NERDR_PORT_END=30009"} {
			if !slices.Contains(env, want) {
				t.Errorf("%s: env lacks %s", x.ID, want)
			}
		}
	}
	// One inherited from a nerdr this one runs in is not passed on.
	m.ClientEnv = []string{"NERDR_PORT=1"}
	if env := m.TabEnv(a); slices.Contains(env, "NERDR_PORT=1") {
		t.Error("an inherited NERDR_PORT was passed on")
	}
}

// A range is held for as long as the job making the tab's worktrees runs,
// however long its setup takes, and for portHoldFor once it is done.
func TestPortsHeldWhileTheJobRuns(t *testing.T) {
	m := portsCore(t, 30000, 10)
	held, release := m.HoldPorts("/w/slow")
	m.portHolds.by["/w/slow"].done = time.Now().Add(-24 * time.Hour) // as if long ago
	if p, _ := m.HoldPorts("/w/other"); p.Overlaps(held) {
		t.Fatalf("a range held for a job still running was given out again: %v", p)
	}
	release(true)
	m.portHolds.by["/w/slow"].done = time.Now().Add(-2 * portHoldFor)
	if p, _ := m.HoldPorts("/w/third"); p != held {
		t.Fatalf("a range held long after its job was done is still held: got %v, want %v", p, held)
	}
}

// A job that fails lets its range go at once.
func TestPortsLetGoWhenTheJobFails(t *testing.T) {
	m := portsCore(t, 65526, 10) // room for one range alone
	held, release := m.HoldPorts("/w/fails")
	if held.IsZero() {
		t.Fatal("nothing held")
	}
	if p, _ := m.HoldPorts("/w/next"); !p.IsZero() {
		t.Fatalf("two ranges given out of room for one: %v", p)
	}
	release(false)
	if p, _ := m.HoldPorts("/w/next"); p != held {
		t.Fatalf("after the job failed, the next got %v, want %v", p, held)
	}
	// Told again, the failed job's release leaves the next one's hold be.
	release(false)
	if _, ok := m.portHolds.by["/w/next"]; !ok {
		t.Fatal("a stale release let another job's range go")
	}
}

// A second job for the folder a range is held for, failing as it finds
// the folder there, leaves the first job's range held.
func TestPortsHeldAgainstASecondJobForTheFolder(t *testing.T) {
	m := portsCore(t, 30000, 10)
	a, releaseA := m.HoldPorts("/w/same")
	b, releaseB := m.HoldPorts("/w/same")
	if b != a {
		t.Fatalf("the second job got %v, not the %v held for the folder", b, a)
	}
	releaseB(false)
	if p, _ := m.HoldPorts("/w/third"); p.Overlaps(a) {
		t.Fatalf("the first job's range %v was given to a third: %v", a, p)
	}
	releaseA(true)
	made := portsTab("n", "/w/same")
	m.givePorts(made)
	if made.Ports != a {
		t.Fatalf("the tab made got %v, want %v", made.Ports, a)
	}
}
