package app

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// remoteTUI is a TUI as a client in a process of its own, of a core with
// one tab whose agent runs argv; the core's loop runs as in its own
// process. What the TUI is told outside Update comes to told.
// remoteLayout, when set, is the layout the core of remoteTUI keeps from
// its session.
var remoteLayout *core.Layout

func remoteTUI(t *testing.T, argv ...string) (*Model, *core.Core, *core.Tab, chan any) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Set up already: install is the core's to say, as its own setting.
	c := &core.Core{Root: t.TempDir(), Wake: make(chan struct{}, 1), Cfg: config.Config{Install: "x"}}
	pane, err := term.Start(argv, t.TempDir(), os.Environ(), 80, 20, c.Notify)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(pane.Close)
	tb := &core.Tab{Tab: state.Tab{ID: "1", Name: "login", Agent: "codex", Dir: "/w/login",
		Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/login", Branch: "login"}}}}
	tb.Pane, tb.Light = pane, core.StIdle
	c.Tabs = []*core.Tab{tb}
	if remoteLayout != nil {
		// As a core started from the session has it, before any client.
		c.Client = core.Unattached(*remoteLayout, core.TabRef{})
	}
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	loop := core.NewLoop(c, nil)
	c.Mu.Lock()
	c.AttachClient(a, func(ev core.RequestEv) { loop.Post(ev) })
	c.Mu.Unlock()
	loop.StartSteps()
	t.Cleanup(func() {
		loop.Stop()
		a.Close("")
		b.Close("")
	})

	yes, no := true, false
	cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, Layout: "agent", TabDetails: &no, Mouse: &yes}
	told := make(chan any, 64)
	// A router, as in a terminal: what has the keyboard is told core.
	router := input.New(strings.NewReader(""), 0x11)
	m, err := NewRemote(cfg, c.Root, router, b, func(msg any) {
		select {
		case told <- msg:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	return m, c, tb, told
}

// pump has m take in what came, as the program does on InboxMsg, until
// ok, drawing meanwhile.
func pump(t *testing.T, m *Model, told chan any, what string, ok func(v string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case msg := <-told:
			m.Update(msg)
		case <-time.After(5 * time.Millisecond):
		}
		v := ansi.Strip(m.View())
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: never shown:\n%s", what, v)
		}
	}
}

// TestRemoteTUI: a TUI in a process of its own shows core's tabs and its
// agent's screen, has core do what it asks and sees it done, types into
// the agent, and core goes by what it shows.
func TestRemoteTUI(t *testing.T) {
	m, c, tb, told := remoteTUI(t, "sh", "-c", `stty -echo; echo ready; while read l; do echo "got $l"; done`)
	pump(t, m, told, "the tab and its agent", func(v string) bool {
		return strings.Contains(v, "login") && strings.Contains(v, "ready")
	})

	m.Mu.Lock()
	m.do(core.RenameTab{T: m.Ref(m.Cur()), Name: "signin"})
	name := m.Cur().Name
	m.Mu.Unlock()
	if name != "signin" {
		t.Fatalf("the copy shows %q once core has done it", name)
	}
	c.Mu.Lock()
	if tb.Name != "signin" {
		t.Errorf("core has %q", tb.Name)
	}
	front := c.Client.Front()
	w, h := c.Client.TabSize(front)
	c.Mu.Unlock()
	if front.Tab != "1" || w == 0 || h == 0 {
		t.Errorf("core goes by front %+v, %d×%d", front, w, h)
	}

	m.Mu.Lock()
	kp := m.keyPane()
	m.Mu.Unlock()
	if kp == nil {
		t.Fatal("no program for the keys")
	}
	kp.Input([]byte("hello\r"))
	pump(t, m, told, "what was typed", func(v string) bool { return strings.Contains(v, "got hello") })
}

// TestRemoteTUIConnectsAgain: when the connection to core closes, the
// TUI connects again, and goes on with the same tabs.
func TestRemoteTUIConnectsAgain(t *testing.T) {
	m, c, _, told := remoteTUI(t, "sh", "-c", `stty -echo; echo ready; while read l; do echo "got $l"; done`)
	pump(t, m, told, "the agent", func(v string) bool { return strings.Contains(v, "ready") })
	m.Mu.Lock()
	tab := m.Cur()
	m.Mu.Unlock()

	loop := core.NewLoop(c, nil)
	loop.StartSteps()
	t.Cleanup(loop.Stop)
	m.SetRedial(func() (*attach.Conn, error) {
		a, b, err := attach.Pair()
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { a.Close(""); b.Close("") })
		c.Mu.Lock()
		c.AttachClient(a, func(ev core.RequestEv) { loop.Post(ev) })
		c.Mu.Unlock()
		return b, nil
	})
	m.Mu.Lock()
	m.remote.Close("") // as core gone would
	m.Mu.Unlock()

	var back tea.Cmd
	deadline := time.After(5 * time.Second)
	for back == nil {
		select {
		case msg := <-told:
			if _, ok := msg.(core.CoreGoneMsg); ok {
				_, back = m.Update(msg)
			}
		case <-deadline:
			t.Fatal("never told core was gone")
		}
	}
	m.Update(back())
	m.Mu.Lock()
	m.do(core.RenameTab{T: m.Ref(m.Cur()), Name: "again"})
	same, name := m.Cur() == tab, m.Cur().Name
	m.Mu.Unlock()
	if !same || name != "again" {
		t.Fatalf("after connecting again: same tab %v, name %q", same, name)
	}
	m.Mu.Lock()
	kp := m.keyPane()
	m.Mu.Unlock()
	kp.Input([]byte("hello\r"))
	pump(t, m, told, "typed after connecting again", func(v string) bool { return strings.Contains(v, "got hello") })
}

// TestRemoteTUIQuiet: a TUI with nothing happening asks nothing of core;
// one telling core of the keyboard after every message had core change,
// which brought it a message, and so on without end.
func TestRemoteTUIQuiet(t *testing.T) {
	m, c, _, told := remoteTUI(t, "sh", "-c", `echo ready; sleep 30`)
	pump(t, m, told, "the agent", func(v string) bool { return strings.Contains(v, "ready") })
	time.Sleep(200 * time.Millisecond)
	c.Mu.Lock()
	gen := c.Gen
	c.Mu.Unlock()
	for range 5 {
		m.Update(core.RedrawMsg{})
		m.View()
	}
	c.Mu.Lock()
	moved := c.Gen - gen
	c.Mu.Unlock()
	if moved != 0 {
		t.Errorf("core changed %d times for a TUI drawing again", moved)
	}
}

// TestRemoteTUITakenOver: a TUI another nerdr took over from ends, and
// does not take core back, which the other would do in its turn.
func TestRemoteTUITakenOver(t *testing.T) {
	m, _, _, _ := remoteTUI(t, "sh", "-c", `echo ready; sleep 30`)
	m.SetRedial(func() (*attach.Conn, error) {
		t.Error("connected again")
		return nil, attach.ErrNoCore
	})
	_, cmd := m.Update(core.CoreGoneMsg{Why: attach.ByeTakenOver})
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || m.GoneErr() != attach.ByeTakenOver {
		t.Fatalf("not ended: %q", m.GoneErr())
	}
}

// TestRemoteTUICoreEnded: a TUI whose core does not come back ends,
// saying so, rather than trying for ever.
func TestRemoteTUICoreEnded(t *testing.T) {
	m, _, _, _ := remoteTUI(t, "sh", "-c", `echo ready; sleep 30`)
	defer func(w time.Duration) { coreGoneWait = w }(coreGoneWait)
	coreGoneWait = 50 * time.Millisecond
	m.SetRedial(func() (*attach.Conn, error) { return nil, attach.ErrNoCore })
	_, cmd := m.Update(core.CoreGoneMsg{})
	if cmd == nil {
		t.Fatal("no command")
	}
	_, quit := m.Update(cmd())
	if quit == nil || !strings.Contains(m.GoneErr(), "did not come back") {
		t.Fatalf("not ended: %q", m.GoneErr())
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("not quit")
	}
}

// TestRemoteTUIIsLaidOutAsCoreSays: a client in another process reads no
// session of its own, which is core's, where core runs: it lays the tabs
// out as core says the last client left them.
func TestRemoteTUIIsLaidOutAsCoreSays(t *testing.T) {
	remoteLayout = &core.Layout{SideWidth: 33}
	t.Cleanup(func() { remoteLayout = nil })
	m, _, _, _ := remoteTUI(t, "cat")
	if m.sideWidth != 33 {
		t.Fatalf("side width %d, want core's 33", m.sideWidth)
	}
}
