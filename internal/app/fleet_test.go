package app

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The TUI showing two machines' cores, each in the test's process: this
// one's, as remoteTUI has it, and box's.

// boxCore is a core of one tab, "deploy" in repository ops, whose agent
// runs argv; and how to connect to it, again and again.
func boxCore(t *testing.T, argv ...string) (*core.Core, *core.Tab, func() (*attach.Conn, error)) {
	c, tb, dial, _ := boxCoreOf(t, argv...)
	return c, tb, dial
}

// boxCoreOf is boxCore, and has what closes core's end of the connection
// last dialed, saying why, as core's server does.
func boxCoreOf(t *testing.T, argv ...string) (*core.Core, *core.Tab, func() (*attach.Conn, error), func(why string)) {
	t.Helper()
	c := &core.Core{Root: t.TempDir(), Wake: make(chan struct{}, 1), Cfg: config.Config{Install: "x"}}
	pane, err := term.Start(argv, t.TempDir(), os.Environ(), 80, 20, c.Notify)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(pane.Close)
	// The same id as the tab of this machine's: the cores do not know of
	// each other, and the TUI tells the two apart by their machines.
	tb := &core.Tab{Tab: state.Tab{ID: "1", Name: "deploy", Agent: "codex", Dir: "/w/deploy",
		Worktrees: []workspace.Worktree{{Repo: "/r/ops", Path: "/w/deploy", Branch: "deploy"}}}}
	tb.Pane, tb.Light = pane, core.StIdle
	c.Tabs = []*core.Tab{tb}
	loop := core.NewLoop(c, nil)
	loop.StartSteps()
	t.Cleanup(loop.Stop)
	var last *attach.Conn
	dial := func() (*attach.Conn, error) {
		a, b, err := attach.Pair()
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { a.Close(""); b.Close("") })
		c.Mu.Lock()
		c.AttachClient(a, func(ev core.RequestEv) { loop.Post(ev) })
		last = a
		c.Mu.Unlock()
		return b, nil
	}
	bye := func(why string) {
		c.Mu.Lock()
		a := last
		c.Mu.Unlock()
		a.Close(why)
	}
	return c, tb, dial, bye
}

// twoMachines is the TUI of remoteTUI showing box's core too, after its
// own: the cores' agents echo what is typed, each saying whose it is.
func twoMachines(t *testing.T) (m *Model, here, box *core.Core, dial func() (*attach.Conn, error), told chan any) {
	t.Helper()
	m, here, box, dial, _, told = twoMachinesBye(t)
	return
}

// twoMachinesBye is twoMachines, and has what closes box's core's end of
// the connection, saying why.
func twoMachinesBye(t *testing.T) (m *Model, here, box *core.Core, dial func() (*attach.Conn, error), bye func(string), told chan any) {
	t.Helper()
	m, here, _, told = remoteTUI(t, "sh", "-c", `stty -echo; echo here-ready; while read l; do echo "here got $l"; done`)
	box, _, dial, bye = boxCoreOf(t, "sh", "-c", `stty -echo; echo box-ready; while read l; do echo "box got $l"; done`)
	conn, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := m.addMachine("box", "", conn, nil, func(msg any) {
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
	pump(t, m, told, "the tabs of both", func(v string) bool {
		return strings.Contains(v, "here-ready") && strings.Contains(v, "box")
	})
	return m, here, box, dial, bye, told
}

// gone has m take the message that the connection to machine name's core
// closed, as the program does, and returns what it gave back.
func gone(t *testing.T, m *Model, told chan any, name string) tea.Cmd {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-told:
			if tm, ok := msg.(fleet.Told); ok && tm.Machine == name {
				if _, ok := tm.Msg.(core.CoreGoneMsg); ok {
					_, cmd := m.Update(msg)
					return cmd
				}
			}
			m.Update(msg)
		case <-deadline:
			t.Fatalf("never told %q's core was gone", name)
		}
	}
}

// locked runs f with the fleet locked, as the TUI reads it.
func locked(m *Model, f func()) {
	m.fleet.Lock()
	defer m.fleet.Unlock()
	f()
}

// boxTab is the TUI's copy of box's tab, and its ref.
func boxTab(t *testing.T, m *Model) (*core.Tab, core.TabRef) {
	t.Helper()
	var tb *core.Tab
	var ref core.TabRef
	locked(m, func() {
		tabs := m.fleet.Tabs()
		if len(tabs) != 2 {
			t.Fatalf("%d tabs", len(tabs))
		}
		tb, ref = tabs[1], m.fleet.Ref(tabs[1])
	})
	return tb, ref
}

// goTo makes tab i of the fleet's the active one.
func goTo(m *Model, i int) {
	locked(m, func() { m.selectTab(i) })
}

// TestFleetShowsBothMachines: the TUI shows the tabs of both cores, this
// machine's first, each named by its own machine though their ids are the
// same, box's in a group of box's under its name.
func TestFleetShowsBothMachines(t *testing.T) {
	m, _, _, _, told := twoMachines(t)
	tb, ref := boxTab(t, m)
	locked(m, func() {
		first := m.fleet.Tabs()[0]
		if first.Name != "login" || tb.Name != "deploy" {
			t.Fatalf("tabs %q, %q", first.Name, tb.Name)
		}
		if r := m.fleet.Ref(first); r != (core.TabRef{Tab: "1"}) || ref != (core.TabRef{Core: "box", Tab: "1"}) {
			t.Fatalf("refs %+v, %+v", r, ref)
		}
		if m.fleet.ByRef(ref) != tb || m.machineOf(tb).Name != "box" {
			t.Fatal("box's tab is not found by its ref")
		}
		if gs := m.fleet.Groups(); len(gs) != 2 || gs[0] != "app" || gs[1] != "box:ops" {
			t.Fatalf("groups %q", gs)
		}
	})
	goTo(m, 1)
	pump(t, m, told, "box's agent, under box's name", func(v string) bool {
		return strings.Contains(v, "box-ready") && strings.Contains(v, " box ") && strings.Contains(v, "ops")
	})
}

// TestFleetRequestGoesToItsCore: what the TUI asks of a tab goes to its
// machine's core alone, and what is typed to its agent too.
func TestFleetRequestGoesToItsCore(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	_, ref := boxTab(t, m)
	locked(m, func() { m.do(core.RenameTab{T: ref, Name: "ship"}) })
	here.Mu.Lock()
	hereName := here.Tabs[0].Name
	here.Mu.Unlock()
	box.Mu.Lock()
	boxName := box.Tabs[0].Name
	box.Mu.Unlock()
	if hereName != "login" || boxName != "ship" {
		t.Fatalf("this machine's tab is %q, box's %q", hereName, boxName)
	}

	goTo(m, 1)
	var kp core.Program
	locked(m, func() { kp = m.keyPane() })
	if kp == nil {
		t.Fatal("no program for the keys")
	}
	kp.Input([]byte("hello\r"))
	pump(t, m, told, "typed to box's agent", func(v string) bool { return strings.Contains(v, "box got hello") })
	box.Mu.Lock()
	front := box.Client.Front()
	box.Mu.Unlock()
	if front != (core.TabRef{Tab: "1"}) {
		t.Errorf("box's core has its front tab as %+v", front)
	}
}

// TestFleetOneGoneOtherWorks: a machine whose core is gone for good is
// grey, and says why in place of its tabs' agents; the other works on;
// and the TUI ends only once that one is gone too.
func TestFleetOneGoneOtherWorks(t *testing.T) {
	m, here, _, _, bye, told := twoMachinesBye(t)
	var box *fleet.Machine
	locked(m, func() { box = m.fleet.Named("box") })
	bye("")
	if cmd := gone(t, m, told, "box"); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("the TUI ended with this machine left")
		}
	}
	if box.Gone == "" {
		t.Fatal("box is not grey")
	}
	goTo(m, 1)
	pump(t, m, told, "box grey", func(v string) bool {
		return strings.Contains(v, "box is not connected") && strings.Contains(v, "box ✕")
	})

	goTo(m, 0)
	locked(m, func() { m.do(core.RenameTab{T: m.fleet.Ref(m.Cur()), Name: "signin"}) })
	here.Mu.Lock()
	name := here.Tabs[0].Name
	here.Mu.Unlock()
	if name != "signin" {
		t.Fatalf("this machine's core has %q", name)
	}

	_, cmd := m.Update(core.CoreGoneMsg{Why: attach.ByeHolderGone})
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || m.GoneErr() != attach.ByeHolderGone {
		t.Fatalf("not ended with no machine left: %q", m.GoneErr())
	}
}

// TestFleetTakenOverIsGrey: a machine another nerdr took over is grey,
// the TUI going on with the other; it is not taken back by itself, which
// the other would do in its turn, but r takes it back, once asked.
func TestFleetTakenOverIsGrey(t *testing.T) {
	m, _, box, dial, bye, told := twoMachinesBye(t)
	var mc *fleet.Machine
	dialed := 0
	locked(m, func() {
		mc = m.fleet.Named("box")
		mc.Redial = func() (*attach.Conn, error) {
			dialed++
			return dial()
		}
	})
	bye(attach.ByeTakenOver)
	if cmd := gone(t, m, told, "box"); cmd != nil {
		t.Fatalf("taken over: %T", cmd())
	}
	if mc.Gone != attach.ByeTakenOver || dialed != 0 {
		t.Fatalf("gone %q, dialed %d", mc.Gone, dialed)
	}
	goTo(m, 1)
	pump(t, m, told, "box taken over", func(v string) bool { return strings.Contains(v, "r take it back") })

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if _, ok := m.dialog.(takeBack); !ok {
		t.Fatalf("dialog %T", m.dialog)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("not connecting again")
	}
	m.Update(cmd())
	if dialed != 1 || mc.Gone != "" || mc.Away {
		t.Fatalf("dialed %d, gone %q, away %v", dialed, mc.Gone, mc.Away)
	}
	_, ref := boxTab(t, m)
	locked(m, func() { m.do(core.RenameTab{T: ref, Name: "back"}) })
	box.Mu.Lock()
	name := box.Tabs[0].Name
	box.Mu.Unlock()
	if name != "back" {
		t.Fatalf("box's core has %q", name)
	}
	pump(t, m, told, "box back", func(v string) bool { return strings.Contains(v, "box-ready") })
}

// TestFleetThisTakenOverIsGrey: this machine's core taken over does not
// end the TUI either, while box is there.
func TestFleetThisTakenOverIsGrey(t *testing.T) {
	m, _, _, _, _ := twoMachines(t)
	_, cmd := m.Update(core.CoreGoneMsg{Why: attach.ByeTakenOver})
	if cmd != nil {
		t.Fatalf("taken over: %T", cmd())
	}
	if m.home().Gone != attach.ByeTakenOver || m.GoneErr() != "" {
		t.Fatalf("gone %q, err %q", m.home().Gone, m.GoneErr())
	}
}

// TestFleetSwapsWithinItsMachine: a tab moves only among its machine's,
// the two machines' tabs being in groups of their own.
func TestFleetSwapsWithinItsMachine(t *testing.T) {
	m, here, box, _, _ := twoMachines(t)
	goTo(m, 0)
	locked(m, func() {
		if m.moveTab(1) != nil || m.Active != 0 {
			t.Fatalf("moved to %d", m.Active)
		}
	})
	here.Mu.Lock()
	n := len(here.Tabs)
	here.Mu.Unlock()
	box.Mu.Lock()
	k := len(box.Tabs)
	box.Mu.Unlock()
	if n != 1 || k != 1 {
		t.Fatalf("tabs here %d, on box %d", n, k)
	}
}

// TestFleetSideListNamesMachines: the list of repositories at the side
// names box's under its badge, as the repository row does.
func TestFleetSideListNamesMachines(t *testing.T) {
	m, _, _, _, told := twoMachines(t)
	locked(m, func() { m.sideBar, m.sideSmall = true, false })
	v := pump(t, m, told, "the side list", func(v string) bool { return strings.Contains(v, " box ") && strings.Contains(v, "ops") })
	var side int
	locked(m, func() { side = m.sideW() })
	if side == 0 {
		t.Fatal("no side list")
	}
	if strings.Contains(v, "box:ops") {
		t.Fatalf("the group's name shown whole:\n%s", v)
	}
}

// TestFleetHostIsEachMachines: the machine a tab's core runs on is named
// by the bar while the tab is in front, each machine's by its own.
func TestFleetHostIsEachMachines(t *testing.T) {
	m, _, _, _, _ := twoMachines(t)
	locked(m, func() { m.SetCoreHost("devbox") })
	goTo(m, 0)
	var here, box string
	locked(m, func() { here = ansi.Strip(m.hostBadge()) })
	goTo(m, 1)
	locked(m, func() { box = ansi.Strip(m.hostBadge()) })
	if !strings.Contains(here, "devbox") || box != "" {
		t.Fatalf("badge %q here, %q on box", here, box)
	}
}

// gitRepo makes dir a git repository, for a core to find.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
}

// wizardRepos are the repositories the new-tab dialog open offers, and
// the machine it is on.
func wizardRepos(m *Model) (string, []string) {
	var on string
	var out []string
	locked(m, func() {
		w, ok := m.dialog.(*wizard)
		if !ok {
			return
		}
		on = w.mc.Name
		for _, r := range w.repos {
			out = append(out, r.Path)
		}
	})
	return on, out
}

// TestFleetNewTabOnChosenMachine: the new-tab dialog starts on the active
// tab's machine, ctrl+o moves it to box, whose repositories it offers
// then, and the tab it makes is box's.
func TestFleetNewTabOnChosenMachine(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	here.Mu.Lock()
	hereRoot := here.Root
	here.Mu.Unlock()
	box.Mu.Lock()
	boxRoot := box.Root
	box.Mu.Unlock()
	gitRepo(t, hereRoot)
	gitRepo(t, boxRoot)

	goTo(m, 0)
	var cmd tea.Cmd
	locked(m, func() { cmd = m.openWizard() })
	if cmd != nil {
		m.Update(cmd())
	}
	pump(t, m, told, "this machine's repositories", func(v string) bool {
		on, repos := wizardRepos(m)
		return on == "" && slices.Contains(repos, hereRoot) && strings.Contains(v, "New tab · on this machine")
	})

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd != nil {
		m.Update(cmd())
	}
	pump(t, m, told, "box's repositories", func(v string) bool {
		on, repos := wizardRepos(m)
		return on == "box" && slices.Contains(repos, boxRoot) && !slices.Contains(repos, hereRoot) &&
			strings.Contains(v, "New tab · on box")
	})

	var w *wizard
	locked(m, func() {
		w = m.dialog.(*wizard)
		m.dialog = nil
		m.addTab(w.mc, "new", boxRoot, "", nil, agents.Codex, false, "")
	})
	here.Mu.Lock()
	n := len(here.Tabs)
	here.Mu.Unlock()
	box.Mu.Lock()
	k := len(box.Tabs)
	box.Mu.Unlock()
	if n != 1 || k != 2 {
		t.Fatalf("tabs here %d, on box %d", n, k)
	}
	locked(m, func() {
		if c := m.Cur(); c == nil || c.Name != "new" || m.machineOf(c).Name != "box" {
			t.Fatalf("in front: %+v", c)
		}
	})
}

// TestFleetDialogFollowsTheActiveTab: with box's tab in front, a dialog
// of no tab is box's, and what it asks naming no tab goes to box's core.
func TestFleetDialogFollowsTheActiveTab(t *testing.T) {
	m, _, _, _, _ := twoMachines(t)
	goTo(m, 1)
	locked(m, func() {
		if mc := m.dialogOn(); mc.Name != "box" {
			t.Fatalf("on %q", mc.Name)
		}
		if mc := m.forOf(core.ScanRepos{}); mc.Name != "box" {
			t.Fatalf("ScanRepos goes to %q", mc.Name)
		}
		m.openSettings()
		if d := m.dialog.(*settings); d.mc.Name != "box" {
			t.Fatalf("settings of %q", d.mc.Name)
		}
	})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd != nil {
		m.Update(cmd())
	}
	locked(m, func() {
		d, ok := m.dialog.(*settings)
		if !ok || d.mc.Name != "" || m.dialogOn().Name != "" {
			t.Fatalf("after ctrl+o: %T", m.dialog)
		}
	})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Settings · on this machine") {
		t.Fatalf("title:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	locked(m, func() {
		if m.dialog != nil || m.dialogOn().Name != "box" {
			t.Fatalf("closed: %T, on %q", m.dialog, m.dialogOn().Name)
		}
	})
}

// TestFleetServerUnreachableAtStart: a server that could not be reached
// as the TUI started is shown grey, saying why; one that answers later,
// as the user connects again, is shown with its tabs.
func TestFleetServerUnreachableAtStart(t *testing.T) {
	m, _, _, told := remoteTUI(t, "sh", "-c", `echo here-ready; sleep 30`)
	_, _, dial := boxCore(t, "sh", "-c", `echo box-ready; sleep 30`)
	s := config.Server{Name: "box"}
	if cmd := m.AddServer(s, nil, errors.New("ssh: connect to host box: Connection refused")); cmd != nil {
		t.Fatal("a command for a server not reached")
	}
	var mc *fleet.Machine
	locked(m, func() { mc = m.fleet.Named("box") })
	if mc == nil || mc.Gone == "" || mc.Conn != nil {
		t.Fatalf("box: %+v", mc)
	}
	pump(t, m, told, "box grey", func(v string) bool { return strings.Contains(v, "box ✕") || strings.Contains(v, "here-ready") })
	locked(m, func() {
		if cmd := m.doOn(mc, core.ScanRepos{}); cmd != nil {
			t.Error("a request done on a server never connected to")
		}
	})

	conn, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(serverDialedMsg{s: s, conn: conn})
	if cmd != nil {
		m.Update(cmd())
	}
	if mc.Gone != "" || mc.Away || mc.Conn == nil {
		t.Fatalf("box after connecting: gone %q, away %v", mc.Gone, mc.Away)
	}
	goTo(m, 1)
	pump(t, m, told, "box's tab", func(v string) bool { return strings.Contains(v, "box-ready") })
}

// TestFleetLoginGoesToSsh: while a login runs, what is typed goes to
// ssh's own terminal, and what it writes there is shown.
func TestFleetLoginGoesToSsh(t *testing.T) {
	var sink func([]byte)
	r := routerFunc(func(f func([]byte)) { sink = f })
	var typed string
	l := &loginExec{s: config.Server{Name: "box"}, router: r,
		dial: func(s config.Server, again bool, tty *os.File) (*attach.Conn, error) {
			if again || tty == nil {
				return nil, errors.New("no terminal to ask on")
			}
			_, _ = tty.WriteString("box's password: ")
			go sink([]byte("secret\r"))
			line, err := bufio.NewReader(tty).ReadString('\n')
			typed = strings.TrimSpace(line)
			return nil, err
		}}
	var out syncBuffer
	l.SetStdout(&out)
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	if typed != "secret" {
		t.Fatalf("ssh read %q", typed)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.String(), "box's password:") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "box's password:") {
		t.Fatalf("shown: %q", out.String())
	}
	if sink != nil {
		t.Error("what is typed still goes to ssh's terminal")
	}
}

// routerFunc is an input router for loginExec.
type routerFunc func(func([]byte))

func (r routerFunc) Passthrough(f func([]byte)) { r(f) }

// syncBuffer is a bytes.Buffer written from one goroutine, read from
// another.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestFleetMachinesDialog: the Machines dialog lists both machines, d
// disconnects box, its agents working on, and x takes it out of the TUI
// and of [[servers]].
func TestFleetMachinesDialog(t *testing.T) {
	m, _, box, _, told := twoMachines(t)
	if err := config.AddServer(config.Server{Name: "box", SSH: "box.lan"}); err != nil {
		t.Fatal(err)
	}
	locked(m, func() { m.cfg().Servers = []config.Server{{Name: "box", SSH: "box.lan"}} })
	locked(m, func() { m.openMachines() })
	v := pump(t, m, told, "the machines", func(v string) bool { return strings.Contains(v, "Machines") })
	if !strings.Contains(v, "this machine") || !strings.Contains(v, "box") {
		t.Fatalf("listed:\n%s", v)
	}
	key := func(k string) {
		_, cmd := m.Update(keyMsg(k))
		if cmd != nil {
			m.Update(cmd())
		}
	}
	key("down")
	key("d")
	var mc *fleet.Machine
	locked(m, func() { mc = m.fleet.Named("box") })
	if mc.Gone == "" {
		t.Fatal("box not disconnected")
	}
	pump(t, m, told, "box disconnected", func(v string) bool { return strings.Contains(v, "disconnected") })

	key("x")
	key("y")
	locked(m, func() {
		if m.fleet.Named("box") != nil || len(m.fleet.Tabs()) != 1 {
			t.Fatalf("box still shown: %d tabs", len(m.fleet.Tabs()))
		}
	})
	c, err := config.Load()
	if err != nil || len(c.Servers) != 0 {
		t.Fatalf("servers %+v, %v", c.Servers, err)
	}
	box.Mu.Lock()
	n := len(box.Tabs)
	box.Mu.Unlock()
	if n != 1 {
		t.Fatalf("box's core has %d tabs", n)
	}
	pump(t, m, told, "this machine's tab", func(v string) bool { return strings.Contains(v, "here-ready") })
}
