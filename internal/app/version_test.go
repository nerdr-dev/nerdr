package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// versionedBox is a core of one tab, as boxCore, reached through a client
// socket of its own that says it is nerdr *version, as a core's does; and
// restart, which ends that socket, as a core that ends does, and opens it
// again saying *version as it is by then: the core started again, updated.
func versionedBox(t *testing.T, version *string) (dial func() (*attach.Conn, error), restart func()) {
	t.Helper()
	c := &core.Core{Root: t.TempDir(), Wake: make(chan struct{}, 1), Cfg: config.Config{Install: "x"}}
	pane, err := term.Start([]string{"sh", "-c", "echo box-ready; sleep 30"}, t.TempDir(), os.Environ(), 80, 20, c.Notify)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(pane.Close)
	tb := &core.Tab{Tab: state.Tab{ID: "1", Name: "deploy", Agent: "codex", Dir: "/w/deploy",
		Worktrees: []workspace.Worktree{{Repo: "/r/ops", Path: "/w/deploy", Branch: "deploy"}}}}
	tb.Pane, tb.Light = pane, core.StIdle
	c.Tabs = []*core.Tab{tb}
	loop := core.NewLoop(c, nil)
	loop.StartSteps()
	t.Cleanup(loop.Stop)

	// Short, for the socket's path.
	d, err := os.MkdirTemp("", "nv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	sock, key := filepath.Join(d, "c.sock"), filepath.Join(d, "c.key")
	var srv *attach.Server
	listen := func() {
		s, err := attach.Listen(sock, key, attach.Config{
			Version: *version,
			Check:   func(hooks.Peer) string { return "" },
			Client: func(cn *attach.Conn, _ attach.Hello) {
				c.Mu.Lock()
				c.AttachClient(cn, func(ev core.RequestEv) { loop.Post(ev) })
				c.Mu.Unlock()
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		srv = s
	}
	listen()
	t.Cleanup(func() { srv.Close("") })
	dial = func() (*attach.Conn, error) {
		cn, _, err := attach.Dial(sock, key, attach.Hello{Version: "0.9.1"})
		return cn, err
	}
	restart = func() {
		srv.Close("")
		listen()
	}
	return dial, restart
}

// settingsView is the settings dialog as drawn, opened on the active tab's
// machine.
func settingsView(m *Model) string {
	locked(m, func() { m.openSettings() })
	return ansi.Strip(m.View())
}

// nextMachineView moves the dialog open to the next machine, as ctrl+o.
func nextMachineView(m *Model) string {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd != nil {
		m.Update(cmd())
	}
	return ansi.Strip(m.View())
}

// TestSettingsShowEachMachinesVersion: the settings say the version of
// nerdr of the machine they are on, which ctrl+o changes, and this
// client's beside one of another; another version of the same protocol is
// let in and not warned of. Connected to again, a core updated meanwhile
// says its new version; one not connected to says it is not, and the
// version it had.
func TestSettingsShowEachMachinesVersion(t *testing.T) {
	m, _, _, told := remoteTUI(t, "sh", "-c", `echo here-ready; sleep 30`)
	m.SetVersion("0.9.1")
	version := "0.9.0"
	dial, restart := versionedBox(t, &version)
	conn, err := dial()
	if err != nil {
		t.Fatalf("a core of another version, of the same protocol, refused: %v", err)
	}
	cmd, err := m.addMachine("box", "", conn, dial, func(msg any) {
		select {
		case told <- msg:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != nil {
		m.Update(cmd())
	}
	pump(t, m, told, "box's tab", func(v string) bool { return strings.Contains(v, "ops (1)") })
	var box *fleet.Machine
	locked(m, func() { box = m.fleet.Named("box") })
	if box.Gone != "" || box.Away {
		t.Fatalf("box not connected: %q", box.Gone)
	}

	// This machine's core was reached with no hello, as remoteTUI has it:
	// it said no version, and none is made up.
	goTo(m, 0)
	v := settingsView(m)
	if !strings.Contains(v, "nerdr on this machine: its version not known") {
		t.Fatalf("this machine's settings:\n%s", v)
	}
	v = nextMachineView(m)
	if !strings.Contains(v, "Settings · on box") || !strings.Contains(v, "nerdr 0.9.0 on box · this client is 0.9.1") {
		t.Fatalf("box's settings:\n%s", v)
	}
	for _, w := range []string{"update nerdr", "(protocol", "is older"} {
		if strings.Contains(v, w) {
			t.Fatalf("warned of box's version (%q):\n%s", w, v)
		}
	}
	if v = nextMachineView(m); strings.Contains(v, "0.9.0") || !strings.Contains(v, "on this machine: its version not known") {
		t.Fatalf("back on this machine:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// box's core is updated, and started again: the TUI connects to it
	// again, and it says its new version.
	version = "0.9.1"
	restart()
	retry := gone(t, m, told, "box")
	if retry == nil {
		t.Fatal("not trying again")
	}
	if msg := retry(); msg != nil {
		m.Update(msg)
	}
	if box.Away || box.Gone != "" {
		t.Fatalf("box not back: %q %q", box.Gone, box.Err)
	}
	goTo(m, 1)
	v = settingsView(m)
	if !strings.Contains(v, "nerdr 0.9.1 on box") || strings.Contains(v, "this client is") || strings.Contains(v, "0.9.0") {
		t.Fatalf("box's settings, updated:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	locked(m, func() { m.disconnect(box) })
	if v = settingsView(m); !strings.Contains(v, "nerdr on box: not connected; it was 0.9.1 when last connected") {
		t.Fatalf("box's settings, disconnected:\n%s", v)
	}
}

// TestVersionNote: what the settings say of a machine's version, as much
// as is known of it.
func TestVersionNote(t *testing.T) {
	m := &Model{version: "1.2"}
	conn := &core.CoreConn{}
	for _, tc := range []struct {
		mc   *fleet.Machine
		want string
	}{
		{&fleet.Machine{}, "nerdr 1.2 on this machine"}, // core in this process
		{&fleet.Machine{Name: "box", Conn: conn, Version: "1.2"}, "nerdr 1.2 on box"},
		{&fleet.Machine{Name: "box", Conn: conn, Version: "1.1"}, "nerdr 1.1 on box · this client is 1.2"},
		{&fleet.Machine{Name: "box", Conn: conn}, "nerdr on box: its version not known"},
		{&fleet.Machine{Name: "box", Conn: conn, Version: "1.1", Away: true}, "nerdr on box: not connected; it was 1.1 when last connected"},
		{&fleet.Machine{Name: "box", Gone: "ssh: refused"}, "nerdr on box: not connected, its version not known"},
	} {
		if got := m.versionNote(tc.mc); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.mc.Name, got, tc.want)
		}
	}
}
