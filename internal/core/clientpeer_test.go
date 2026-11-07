package core

import (
	"os"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// TestClientRefusal: who the client socket takes a client from.
func TestClientRefusal(t *testing.T) {
	p, err := term.Start([]string{"sleep", "30"}, t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	m := &Core{}
	tab := &Tab{}
	tab.Pane = p
	under := hooks.Peer{Known: true, Chain: []int{1 << 30, p.Pid(), 1}}

	const stray, shell, terminal = 1 << 30, 1<<30 + 1, 1<<30 + 2
	for _, c := range []struct {
		name    string
		tabs    []*Tab
		peer    hooks.Peer
		refused bool
	}{
		{"your terminal", []*Tab{tab}, hooks.Peer{Known: true, Chain: []int{stray, shell, terminal, 1}}, false},
		{"under an agent", []*Tab{tab}, hooks.Peer{Known: true, Chain: []int{stray, p.Pid(), os.Getpid(), 1}}, true},
		{"under an agent outside nerdr's tree", []*Tab{tab}, under, true},
		{"under nerdr itself", nil, hooks.Peer{Known: true, Chain: []int{stray, os.Getpid(), 1}}, true},
		{"a system that cannot say", []*Tab{tab}, hooks.Peer{}, true},
		{"a peer asked and not told", nil, hooks.Peer{Known: true}, true},
	} {
		m.Tabs = c.tabs
		if got := m.ClientRefusal(c.peer) != ""; got != c.refused {
			t.Errorf("%s: refused = %v", c.name, got)
		}
	}

	// An agent beside a tab's own, and a reviewer, count as much.
	beside := &Tab{}
	beside.Pane = p
	host := &Tab{}
	host.PaneList = []*PaneTab{{Kind: PaneAgent, A: host}, {Kind: PaneAgent, A: beside}}
	m.Tabs = []*Tab{host}
	if m.ClientRefusal(under) == "" {
		t.Error("a client under an agent beside the tab's own was taken")
	}
	reviewer := &Tab{}
	reviewer.Pane = p
	host = &Tab{}
	host.Review = &ReviewRun{Seats: []*Seat{{R: reviewer}}}
	m.Tabs = []*Tab{host}
	if m.ClientRefusal(under) == "" {
		t.Error("a client under a reviewer was taken")
	}

	// Under an agent whose start nerdr has, or that the chain says
	// started at another time: refused all the same.
	tab.PidStart = p.StartTime()
	m.Tabs = []*Tab{tab}
	for _, peer := range []hooks.Peer{under, {Known: true, Chain: under.Chain, Starts: []uint64{0, tab.PidStart + 1, 1}}} {
		if m.ClientRefusal(peer) == "" {
			t.Errorf("a client under an agent taken: %+v", peer)
		}
	}
}
