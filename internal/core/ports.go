package core

import (
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Each tab is given a range of ports of its own (config port_base and
// ports_per_tab), for what runs in it to listen on: its agents, terminals,
// plugins and setup are told it (TabEnv, workspace.Ports.Env), and an
// agent asks for it with nerdr ports, through the socket its sandbox lets
// it reach already, when the sandbox keeps the variables from it. It is kept
// in the session for as long as the tab is open, and is free again for
// another once it is closed. A range is given on the machine the tab runs
// on: a tab moved to another is made there afresh, and given one there.
//
// A tab's setup runs before the tab is made, in the worktrees it will be
// in: the range is given then (HoldPorts), held for the tab's folder, and
// taken up by the tab made in it (givePorts). It is held for as long as
// the job making the worktrees runs, however long their setup takes; let
// go of at once when the job fails, and kept for portHoldFor after it is
// done, for the tab to be made, when it does not.

// portHolds are the ranges held for tabs being made, by folder, with a
// lock of their own: the job making the worktrees, in the background,
// says when it is done (portRelease).
type portHolds struct {
	mu sync.Mutex
	by map[string]*portHold
}

// portHold is a range held for a tab being made: busy while the job that
// makes its worktrees runs, and done when it ended.
type portHold struct {
	ports workspace.Ports
	busy  bool
	done  time.Time
}

// portHoldFor is how long a range is held for a tab once the job that
// made its worktrees is done: for a wizard left open before its tab is
// made.
const portHoldFor = time.Hour

// portRelease is told by the job a range was held for whether it made
// the worktrees: when not, the range is let go of.
type portRelease func(ok bool)

// holds is m's portHolds. m.Mu is held.
func (m *Core) holds() *portHolds {
	if m.portHolds == nil {
		m.portHolds = &portHolds{by: map[string]*portHold{}}
	}
	return m.portHolds
}

// held is the ranges held now, those held too long since their job was
// done let go of.
func (h *portHolds) held() []workspace.Ports {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []workspace.Ports
	for d, x := range h.by {
		if !x.busy && time.Since(x.done) > portHoldFor {
			delete(h.by, d)
			continue
		}
		out = append(out, x.ports)
	}
	return out
}

// take is the range held for dir, which is no longer held.
func (h *portHolds) take(dir string) (workspace.Ports, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	x, ok := h.by[dir]
	if !ok {
		return workspace.Ports{}, false
	}
	delete(h.by, dir)
	return x.ports, true
}

// release is the portRelease of the range held for dir as p.
func (h *portHolds) release(dir string, p workspace.Ports) portRelease {
	return func(ok bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		// The range only: a tab may have taken it up already, and another
		// job be holding one for the folder since.
		if x, there := h.by[dir]; there && x.ports == p && x.busy {
			if ok {
				x.busy, x.done = false, time.Now()
			} else {
				delete(h.by, dir)
			}
		}
	}
}

// portsOn reports whether the config gives tabs ports.
func (m *Core) portsOn() bool { return m.Cfg.PortsPerTab != nil && *m.Cfg.PortsPerTab > 0 }

// HoldPorts is the range of the tab to be made in folder dir, and what
// the job making its worktrees tells when it is done: that of the tab
// already there, or a new one, held for it until the tab is made. Zero
// when tabs are given none, or none is free. A range held for dir already,
// by another job for the same folder, is that job's: this one is given it
// and lets nothing go, as it fails when it finds the folder there. m.Mu
// is held.
func (m *Core) HoldPorts(dir string) (workspace.Ports, portRelease) {
	none := func(bool) {}
	if !m.portsOn() || dir == "" {
		return workspace.Ports{}, none
	}
	dir = filepath.Clean(dir)
	for _, t := range m.Tabs {
		if !t.Ports.IsZero() && SamePath(t.Dir, dir) {
			return t.Ports, none
		}
	}
	h := m.holds()
	p := m.freePorts() // which lets go of the holds gone stale first
	h.mu.Lock()
	defer h.mu.Unlock()
	if x, ok := h.by[dir]; ok {
		return x.ports, none
	}
	if p.IsZero() {
		return p, none
	}
	h.by[dir] = &portHold{ports: p, busy: true}
	return p, h.release(dir, p)
}

// givePorts gives tab t its range, if it has none: the one held for its
// folder, or a new one. m.Mu is held.
func (m *Core) givePorts(t *Tab) {
	if !t.Ports.IsZero() || !m.portsOn() {
		return
	}
	if p, ok := m.holds().take(filepath.Clean(t.Dir)); ok && !m.portsTaken(p, t) {
		t.Ports = p
		return
	}
	t.Ports = m.freePorts()
}

// portsTaken reports whether p overlaps the range of a tab other than
// but.
func (m *Core) portsTaken(p workspace.Ports, but *Tab) bool {
	for _, t := range m.Tabs {
		if t != but && t.Ports.Overlaps(p) {
			return true
		}
	}
	return false
}

// freePorts is the first range from port_base that no tab has, nor is
// held. Zero when there is none. m.Mu is held.
func (m *Core) freePorts() workspace.Ports {
	n := *m.Cfg.PortsPerTab
	var taken []workspace.Ports
	for _, t := range m.Tabs {
		taken = append(taken, t.Ports)
	}
	taken = append(taken, m.holds().held()...)
next:
	for first := m.Cfg.PortBase; first+n-1 <= 65535; first += n {
		p := workspace.Ports{First: first, Count: n}
		for _, q := range taken {
			if p.Overlaps(q) {
				continue next
			}
		}
		return p
	}
	return workspace.Ports{}
}

// portEnv is the variables that tell a program of t's tab its ports:
// none when tabs are given none. m.Mu is held.
func (m *Core) portEnv(t *Tab) []string {
	o := t.Owner()
	if !m.portsOn() {
		return nil
	}
	m.givePorts(o)
	return o.Ports.Env()
}

// tabPorts answers nerdr ports for t's tab: its first and last port, a
// space between. m.Mu is held.
func (m *Core) tabPorts(t *Tab) hooks.Answer {
	o := t.Owner()
	if m.portsOn() {
		m.givePorts(o)
	}
	if !m.portsOn() || o.Ports.IsZero() {
		why := "no port range is free"
		if !m.portsOn() {
			why = "ports_per_tab is 0 in nerdr's config"
		}
		return hooks.Answer{Err: "this tab has no ports: " + why}
	}
	return hooks.Answer{Text: strconv.Itoa(o.Ports.First) + " " + strconv.Itoa(o.Ports.Last())}
}
