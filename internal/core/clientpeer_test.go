package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
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
		{"a chain cut short", nil, hooks.Peer{Known: true, Chain: []int{stray, shell}, Cut: true}, true},
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

// TestClientUnderNerdr: a client under a nerdr that runs programs, of any
// folder, is refused, however nerdr was named, unless it is under the
// processes that started this core first; one under none is taken.
func TestClientUnderNerdr(t *testing.T) {
	args := map[int][]string{
		10: {"nerdr", "connect"},
		11: {"bash"},
		12: {"/home/u/bin/nerdr-new", "holder", "--", "/home/u/bin/nerdr-new", "core"},
		13: {"nerdr", "holder", "--resume", "7"}, // as one updated execs into
		14: {"tmux"},
		15: {"nerdr", spawn.LocalArg},
		16: {"bash", "holder"}, // not nerdr's
	}
	defer func(f func(int) ([]string, error)) { procArgs = f }(procArgs)
	procArgs = func(pid int) ([]string, error) {
		if a, ok := args[pid]; ok {
			return a, nil
		}
		return nil, os.ErrNotExist
	}
	// The shell, 11, started this core from under the holder, 12.
	origin := []spawn.Process{{Pid: 11, Start: 111}}
	for _, c := range []struct {
		name    string
		chain   []int
		starts  []uint64
		origin  []spawn.Process
		refused bool
	}{
		{"your terminal", []int{10, 11, 14, 1}, nil, nil, false},
		{"under a holder", []int{10, 11, 12, 1}, nil, nil, true},
		{"under an updated holder", []int{10, 11, 13, 1}, nil, nil, true},
		{"under a nerdr running its programs itself", []int{10, 11, 15, 1}, nil, nil, true},
		{"under a program given holder", []int{10, 16, 14, 1}, nil, nil, false},
		{"a chain that cannot be read", []int{20, 21}, nil, nil, false},
		{"from where the core was started", []int{10, 11, 12, 1}, []uint64{1, 111, 1, 1}, origin, false},
		{"another program under that holder", []int{10, 14, 12, 1}, []uint64{1, 1, 1, 1}, origin, true},
		{"a process given the id of the one it started from", []int{10, 11, 12, 1}, []uint64{1, 112, 1, 1}, origin, true},
		{"a start not known", []int{10, 11, 12, 1}, nil, []spawn.Process{{Pid: 11}}, true},
		{"a process it started from that has exec'd into a nerdr", []int{10, 15, 12, 1}, []uint64{1, 151, 1, 1}, []spawn.Process{{Pid: 15, Start: 151}}, true},
	} {
		if got := ClientUnderNerdr(hooks.Peer{Known: true, Chain: c.chain, Starts: c.starts}, c.origin) != ""; got != c.refused {
			t.Errorf("%s: refused = %v", c.name, got)
		}
	}
}

// TestClientUnderARealHolder: the command line is read from the kernel.
func TestClientUnderARealHolder(t *testing.T) {
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	// sh runs the script "holder", its command line nerdr's holder's.
	if err := os.WriteFile(filepath.Join(dir, "holder"), []byte("sleep 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: "/bin/sh", Args: []string{"nerdr", "holder", "--"}, Dir: dir}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if a, _ := spawn.Args(cmd.Process.Pid); spawn.IsHolder(a) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never saw its command line")
		}
	}
	if ClientUnderNerdr(hooks.Peer{Known: true, Chain: []int{os.Getpid(), cmd.Process.Pid, 1}}, nil) == "" {
		t.Error("a client under a holder taken")
	}
	if ClientUnderNerdr(hooks.Peer{Known: true, Chain: []int{os.Getpid(), os.Getppid(), 1}}, nil) != "" {
		t.Error("a client under no holder refused")
	}
}
