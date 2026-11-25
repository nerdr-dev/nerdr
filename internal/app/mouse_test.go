package app

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestScanZones(t *testing.T) {
	m := &Model{}
	m.home().Core.Client = m
	var hit []string
	fn := func(name string) zoneFn {
		return func(*Model, tea.MouseMsg) tea.Cmd { hit = append(hit, name); return nil }
	}
	row := "ab" + m.zone("\x1b[1mcd\x1b[0m", fn("cd")) + "é" + m.zone("fgh", fn("fgh"))
	// Truncated in the middle of the second zone, as a bar is.
	row = ansi.Truncate(row, 6, "")
	box := m.zone("  "+m.zone("ok", fn("ok"))+"  \n  x  ", fn("box"))
	out, rects := scanZones(row + "\n" + box)
	m.zoneRects = rects

	if strings.Contains(out, "z") {
		t.Fatalf("markers left in %q", out)
	}
	if got := ansi.Strip(out); got != "abcdéf\n  ok  \n  x  " {
		t.Fatalf("drawn %q", got)
	}
	for _, c := range []struct {
		x, y int
		want string
	}{
		{0, 0, ""}, {2, 0, "cd"}, {3, 0, "cd"}, {4, 0, ""}, {5, 0, "fgh"}, {6, 0, ""},
		{2, 1, "ok"}, {0, 1, "box"}, {5, 1, "box"}, {2, 2, "box"}, {6, 2, ""},
	} {
		hit = nil
		if id := m.zoneAt(c.x, c.y); id >= 0 {
			m.zoneFns[id](m, tea.MouseMsg{})
		}
		if got := strings.Join(hit, ","); got != c.want {
			t.Errorf("(%d,%d) hit %q, want %q", c.x, c.y, got, c.want)
		}
	}
}

// usersConfig is where the config is read from outside the tests.
var usersConfig = os.Getenv("XDG_CONFIG_HOME")

// testModel is a model with two tabs in repo "app" and one in "lib",
// drawn at 120×30, with nothing running.
func testModel(t *testing.T) *Model {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// What is picked in a review pane is saved in the config: not the
	// user's, unless the test gave one of its own.
	if os.Getenv("XDG_CONFIG_HOME") == usersConfig {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	yes, no := true, false
	// The bottom bar off: most tests are of the bars at the top; those of
	// the bottom bar turn it on (see bottombar_test.go).
	cfg := config.Config{Prefix: "ctrl+q", TabDetails: &no, BottomBar: &no, Mouse: &yes}
	mk := func(id, name, repo string) state.Tab {
		return state.Tab{ID: id, Name: name, Agent: "claude", Dir: "/w/" + name,
			Worktrees: []workspace.Worktree{{Repo: repo, Path: "/w/" + name, Branch: name}}}
	}
	sess := state.Session{Tabs: []state.Tab{mk("1", "login", "/r/app"), mk("2", "logout", "/r/app"), mk("3", "parse", "/r/lib")}}
	m := New(cfg, t.TempDir(), nil, sess)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

// clickOn clicks the first place the screen shows s, with button b.
func clickOn(t *testing.T, m *Model, s string, b tea.MouseButton) tea.Cmd {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, s); i >= 0 {
			x := ansi.StringWidth(line[:i])
			_, press := m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
			_, release := m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionRelease})
			m.View()
			return tea.Batch(press, release)
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", s, ansi.Strip(m.View()))
	return nil
}

func TestClickTabsAndGroups(t *testing.T) {
	m := testModel(t)
	clickOn(t, m, "logout", tea.MouseButtonLeft)
	if m.Cur().Name != "logout" {
		t.Fatalf("active %q after clicking logout", m.Cur().Name)
	}
	clickOn(t, m, "lib (1)", tea.MouseButtonLeft)
	if m.Cur().Name != "parse" {
		t.Fatalf("active %q after clicking group lib", m.Cur().Name)
	}
	// The wheel over the group row steps through the groups.
	m.View()
	m.Update(tea.MouseMsg{X: 100, Y: 0, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.Cur().GroupName() != "app" {
		t.Fatalf("group %q after the wheel", m.Cur().GroupName())
	}
}

// TestWheelBurst: a burst of the wheel over the tabs — a wheel spinning
// on, or a trackpad gliding — steps one tab, not one for every report.
func TestWheelBurst(t *testing.T) {
	m := testModel(t)
	m.View()
	y := m.tabRow()
	before := m.Cur().Name
	for range 30 {
		m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	after := m.Cur().Name
	if after == before {
		t.Fatal("the wheel over the tabs did not step")
	}
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.Cur().Name != after {
		t.Fatalf("stepped on to %s straight after", m.Cur().Name)
	}
	// Once the gap has passed, the next notch steps again.
	m.lastWheelStep = m.lastWheelStep.Add(-wheelGap)
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.Cur().Name == after {
		t.Fatal("the wheel did not step after the gap")
	}
}

// TestWheelSlowStep: a step that takes longer than wheelGap to draw does
// not let the turns queued up meanwhile step on, each in its turn; they
// are let go without drawing the screen again.
func TestWheelSlowStep(t *testing.T) {
	m := testModel(t)
	m.View()
	y := m.tabRow()
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	after := m.Cur().Name
	// The step took a long while to handle, and is drawn only now.
	m.lastWheelStep = m.lastWheelStep.Add(-2 * wheelGap)
	m.View()
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.Cur().Name != after {
		t.Fatalf("a queued turn stepped on to %s", m.Cur().Name)
	}
	if !m.sameFrame {
		t.Fatal("a turn held back drew the screen again")
	}
}

// TestWheelAfterFocus: the wheel is not heard while nerdr is in the
// background, nor after it gets the focus back while the wheel goes on;
// TestDragTheSideEdge drags the sidebar's edge wider, and a right-click
// on it gives it back the width its names need.
func TestDragTheSideEdge(t *testing.T) {
	m := testModel(t)
	m.sideBar = true
	m.resize()
	m.View()
	auto := m.sideW()
	if auto == 0 {
		t.Fatal("no sidebar")
	}
	y := m.barRows() + 3
	m.Update(tea.MouseMsg{X: auto - 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: auto + 9, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m.Update(tea.MouseMsg{X: auto + 9, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if w := m.sideW(); w != auto+10 {
		t.Fatalf("sidebar %d columns after the drag, want %d", w, auto+10)
	}
	if s, _ := state.Load(m.home().Core.Root); s.SideWidth != auto+9 {
		t.Fatalf("saved width %d, want %d", s.SideWidth, auto+9)
	}
	m.Update(tea.MouseMsg{X: m.sideW() - 1, Y: y, Button: tea.MouseButtonRight, Action: tea.MouseActionPress})
	if m.sideWidth != 0 || m.sideW() != auto {
		t.Fatalf("sidebar %d columns after the right-click, want %d", m.sideW(), auto)
	}
}

// TestTooltip rests the pointer on a tab's light: its tip shows once the
// wait is over, under the pointer, and a click puts it away.
func TestTooltip(t *testing.T) {
	m := testModel(t)
	if m.mouse != mouseAll {
		t.Fatal("the pointer is not followed for the tips")
	}
	var x, y int
	for row, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, "logout"); i >= 0 {
			x, y = ansi.StringWidth(line[:i])-4, row // its light: "○ 2:logout"
			break
		}
	}
	move := func(x, y int) tea.Cmd {
		_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion})
		m.View()
		return cmd
	}
	if move(x, y) == nil {
		t.Fatal("no wait for the tip")
	}
	under := func() string {
		lines := strings.Split(ansi.Strip(m.View()), "\n")
		return string([]rune(lines[y+1])[x:])
	}
	if strings.HasPrefix(under(), " Starting ") {
		t.Fatal("the tip shows before the wait is over")
	}
	m.Update(tipMsg{m.hover.seq})
	if !strings.HasPrefix(under(), " Starting ") {
		t.Fatalf("no tip under the pointer: %q", under())
	}
	// Moving on within the tab, to its name, gives the tab's own tip.
	if move(x+4, y) == nil || m.hover.tip == "" || !strings.HasPrefix(m.hover.tip, "logout · claude") {
		t.Fatalf("tip on the name %q", m.hover.tip)
	}
	m.Update(tipMsg{m.hover.seq})
	m.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.hover.shown || m.Cur().Name != "logout" {
		t.Fatalf("after a click: tip shown %v, tab %q", m.hover.shown, m.Cur().Name)
	}
	// A late wait for a tip left behind shows nothing.
	m.Update(tipMsg{m.hover.seq - 1})
	if m.hover.shown {
		t.Fatal("a stale wait showed the tip")
	}
}

// TestClickPRViews: on F2 a click on "my PRs" shows all your pull
// requests, and a click on "this branch" goes back to the branch's.
func TestClickPRViews(t *testing.T) {
	m := testModel(t)
	m.home().Core.PRWatch.At = time.Now() // known already: GitHub is not asked
	m.handleKey(keyMsg("f2"))
	// As my PRs has it: the title in the border, the views right below.
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for i, l := range lines {
		if strings.Contains(l, "this branch") && (i == 0 || !strings.Contains(lines[i-1], "─ pull request")) {
			t.Fatalf("the views are not right below the titled border:\n%s", strings.Join(lines, "\n"))
		}
	}
	clickOn(t, m, "my PRs", tea.MouseButtonLeft)
	if !m.review.AllPRs() {
		t.Fatalf("a click on my PRs should show all PRs:\n%s", ansi.Strip(m.View()))
	}
	clickOn(t, m, "this branch", tea.MouseButtonLeft)
	if m.review.AllPRs() {
		t.Fatalf("a click on this branch should go back to the branch's PR:\n%s", ansi.Strip(m.View()))
	}
}

// A drag over a terminal pane selects its text and copies it, as a drag
// over the agent does.
func TestDragCopiesFromATerminalPane(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "shell")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'hello-panel'\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.home().Core.Cfg.Shell = script
	m.Cur().Dir = dir
	m.runCommand("t t")
	defer m.Shutdown()
	var copied string
	was := toClipboard
	toClipboard = func(s string) bool { copied = s; return true }
	defer func() { toClipboard = was }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
			if i := strings.Index(line, "hello-panel"); i >= 0 {
				x := ansi.StringWidth(line[:i])
				m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
				m.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
				m.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
				if copied != "hello" {
					t.Fatalf("copied %q", copied)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell is not on the screen:\n%s", ansi.Strip(m.View()))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A terminal beside the agent (prefix t t) scrolls with the wheel and
// copies what is dragged over, as the agent does; a click on the agent
// gives it the keyboard back.
func TestTerminalBesideTheAgentScrollsAndCopies(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "shell")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nfor i in $(seq 1 100); do echo line-$i; done\necho last-line\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.home().Core.Cfg.Shell = script
	m.Cur().Dir = dir
	agent, err := term.Start([]string{"sh", "-c", "echo agent-here; sleep 5"}, dir, os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	m.Cur().Pane = agent
	m.runCommand("t t")
	m.resize()
	defer m.Shutdown()
	var copied string
	was := toClipboard
	toClipboard = func(s string) bool { copied = s; return true }
	defer func() { toClipboard = was }()

	var x, y int
	deadline := time.Now().Add(3 * time.Second)
	for x == 0 {
		for i, line := range strings.Split(ansi.Strip(m.View()), "\n") {
			if j := strings.Index(line, "last-line"); j >= 0 {
				x, y = ansi.StringWidth(line[:j]), i
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell is not on the screen:\n%s", ansi.Strip(m.View()))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "agent-here") {
		t.Fatalf("the agent should stay on screen beside the terminal:\n%s", v)
	}
	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: x + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m.Update(tea.MouseMsg{X: x + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if copied != "last" {
		t.Fatalf("copied %q", copied)
	}
	for range 3 {
		m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "last-line") || !strings.Contains(v, "scrollback 9/") {
		t.Fatalf("not scrolled back:\n%s", v)
	}
	for range 3 {
		m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "last-line") || strings.Contains(v, "scrollback") {
		t.Fatalf("not back at the bottom:\n%s", v)
	}
	clickOn(t, m, "agent-here", tea.MouseButtonLeft)
	if m.keyPane() != agent {
		t.Fatal("a click on the agent should give it the keyboard")
	}
}

// A program's OSC 52 reaches the clipboard only from the program with the
// keyboard, just after a click was passed to it or keys typed to it, and
// before the copy came: neither focus, being shown, the mouse moving over
// it, a click it was not told of, nor input after an unasked copy is
// asking.
func TestOSC52OnlyWhenAsked(t *testing.T) {
	m := testModel(t)
	var copied []string
	was := toClipboard
	toClipboard = func(s string) bool { copied = append(copied, s); return true }
	defer func() { toClipboard = was }()
	panes := make([]*term.Pane, 4)
	for i := range panes {
		panes[i] = &term.Pane{Screen: term.NewScreen(40, 5)}
		panes[i].Feed([]byte("\x1b[?1000h\x1b[?1006h")) // takes the mouse, SGR
	}
	tabs := m.home().Core.Tabs
	tabs[0].Pane, tabs[1].Pane, tabs[2].Pane = panes[0], panes[1], panes[2]
	tabs[0].PaneList = append(tabs[0].Panes(), &core.PaneTab{Kind: core.PaneShell, Shell: panes[3]})
	m.split = tabs[1]
	m.focus = core.FocusAgent
	if m.keyPane() != core.Program(panes[0]) {
		t.Fatal("the agent of the first tab should have the keyboard")
	}
	all := []*term.Pane{panes[0], panes[1], panes[2], panes[3]}
	feed := func(text string) {
		for _, p := range all {
			p.Feed([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"))
		}
	}
	check := func() {
		m.checkCopied()
		for i, p := range all {
			if got := p.TakeCopied(); got != "" {
				t.Errorf("pane %d retained %q for replay", i, got)
			}
		}
	}
	osc := func(text string) { feed(text); check() }
	release := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	want := func(what string, w ...string) {
		t.Helper()
		if !slices.Equal(copied, w) {
			t.Fatalf("%s: copied %q, want %q", what, copied, w)
		}
	}

	// Input that went to nerdr, not typed to the program: not asking.
	m.router = input.New(strings.NewReader("x"), 0x11)
	read := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, m.router.Reader()); close(read) }()
	m.router.Run()
	<-read
	if m.router.LastInput().IsZero() {
		t.Fatal("input was not recorded")
	}
	osc("unasked")
	// Nor is the mouse moving over it, or the wheel.
	m.sendMouse(panes[0], tea.MouseMsg{Action: tea.MouseActionMotion}, 1, 1)
	m.sendMouse(panes[0], tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}, 1, 1)
	osc("moved")
	want("unasked")

	// A button let go in the agent: its copy is the user's, and only its.
	m.sendMouse(panes[0], release, 1, 1)
	osc("selected")
	want("after a release", "selected")
	// Long after, not.
	m.clicks = input.Inputs{}
	m.clicks.Note(panes[0], time.Now().Add(-2*copyWindow))
	osc("later")
	// A click in a program without the keyboard counts for nothing.
	m.sendMouse(panes[1], release, 1, 1)
	osc("other")
	want("stale or elsewhere", "selected")
	// A copy made unasked is not made asked by a click after it.
	m.clicks = input.Inputs{}
	feed("before the click")
	m.sendMouse(panes[0], release, 1, 1)
	check()
	want("click after", "selected")
	// A release in X10 mode is not reported, so it does not count.
	m.clicks = input.Inputs{}
	panes[0].Feed([]byte("\x1b[?1000l\x1b[?1006l\x1b[?9h"))
	if !m.sendMouse(panes[0], release, 1, 1) {
		t.Fatal("a program in X10 mode takes the mouse")
	}
	osc("x10 release")
	want("x10 release", "selected")
	panes[0].Feed([]byte("\x1b[?9l\x1b[?1000h\x1b[?1006h"))

	// Keys typed to the agent count for it, and for nothing else: not for
	// the pane the keyboard moves to next, and keys typed to a dialog not
	// for the agent.
	m.clicks = input.Inputs{}
	pr, pw := io.Pipe()
	defer pw.Close()
	m.router = input.New(pr, 0x11)
	got := make(chan []byte, 1)
	go m.router.Run()
	typeTo := func(owner any) {
		m.router.PassthroughTo(func(b []byte) { got <- append([]byte(nil), b...) }, owner)
		_, _ = pw.Write([]byte("/copy\r"))
		<-got
	}
	typeTo(panes[0])
	osc("typed")
	want("typed", "selected", "typed")
	// Typing on before the copy is taken does not undo it.
	typeTo(panes[0])
	feed("typed on")
	typeTo(panes[0])
	check()
	want("typed on", "selected", "typed", "typed on")
	m.tl(tabs[0]).on = tabs[0].PaneList[1]
	if m.keyPane() != core.Program(panes[3]) {
		t.Fatal("the terminal should have the keyboard")
	}
	osc("moved on")
	m.tl(tabs[0]).on = tabs[0].PaneList[0]
	// From here, with no keys typed to the agent yet.
	m.router.PassthroughTo(nil, nil)
	r2, w2 := io.Pipe()
	defer w2.Close()
	m.router = input.New(r2, 0x11)
	go m.router.Run()
	pw = w2
	typeTo(&loginDialog{})
	osc("to a dialog")
	want("typed elsewhere", "selected", "typed", "typed on")
	// Keys typed after an unasked copy do not make it asked.
	feed("before the keys")
	typeTo(panes[0])
	check()
	want("keys after", "selected", "typed", "typed on")

	// A click an editor did not ask for is not passed to it, nor counts.
	m.router.PassthroughTo(nil, nil)
	ed := &term.Pane{Screen: term.NewScreen(40, 5)}
	m.editor = ed
	all = append(all, ed)
	m.handleMouse(tea.MouseMsg{X: 1, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	ed.Feed([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("editor")) + "\x07"))
	check()
	want("editor unasked", "selected", "typed", "typed on")
	// One that asked for the mouse: the click is its, and so its copy.
	ed.Feed([]byte("\x1b[?1000h\x1b[?1006h"))
	m.handleMouse(tea.MouseMsg{X: 1, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	ed.Feed([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("editor")) + "\x07"))
	check()
	want("editor asked", "selected", "typed", "typed on", "editor")
}

// sendingProgram copies at once when it is sent a click, before
// SendMouse is back, and notes when it was sent it.
type sendingProgram struct {
	*term.Pane
	sent time.Time
}

func (p *sendingProgram) SendMouse(ev term.MouseEvent) bool {
	p.sent = time.Now()
	time.Sleep(5 * time.Millisecond)
	return p.Pane.SendMouse(ev)
}

// A click is timed before it is sent: a program that copies before
// SendMouse is back still copies after the click.
func TestClickTimedBeforeSent(t *testing.T) {
	m := testModel(t)
	p := &sendingProgram{Pane: &term.Pane{Screen: term.NewScreen(40, 5)}}
	p.Feed([]byte("\x1b[?1000h\x1b[?1006h"))
	m.sendMouse(p, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}, 1, 1)
	if !m.clicks.To(p, p.sent, copyWindow) {
		t.Fatal("the click was timed after it was sent")
	}
}

// TestClickPRNumber: on the active tab, a click on its PR's number opens
// the PR in the browser, with a tip saying so; the right and the middle
// button still do what they do on the tab, and on another tab a click
// goes to the tab.
func TestClickPRNumber(t *testing.T) {
	opened := filepath.Join(t.TempDir(), "opened")
	script := filepath.Join(t.TempDir(), "browser")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" >> "+opened+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := browser.Setting
	browser.Setting = script
	t.Cleanup(func() { browser.Setting = old })

	m := testModel(t)
	m.showDetails = true
	for i, n := range []int{7, 8} {
		m.home().Core.Tabs[i].Info = core.TabInfo{At: time.Now(), Wts: []core.WtInfo{{HasPR: true,
			Pr: github.Status{Number: n, State: "OPEN", URL: "https://github.com/o/app/pull/" + strconv.Itoa(n)}}}}
	}
	m.selectTab(0)
	m.View()

	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, "#7"); i >= 0 {
			m.Update(tea.MouseMsg{X: ansi.StringWidth(line[:i]), Y: y, Action: tea.MouseActionMotion})
			break
		}
	}
	if !strings.Contains(m.hover.tip, "open it in the browser") {
		t.Fatalf("tip over the PR number %q", m.hover.tip)
	}
	clickOn(t, m, "#7", tea.MouseButtonRight)
	if _, ok := m.dialog.(*menu); !ok {
		t.Fatalf("right click on the PR number: dialog %T, want the tab's menu", m.dialog)
	}
	m.dialog = nil
	clickOn(t, m, "#7", tea.MouseButtonMiddle)
	if m.dialog == nil {
		t.Fatal("middle click on the PR number did not ask to close the tab")
	}
	m.dialog = nil
	clickOn(t, m, "#8", tea.MouseButtonLeft)
	if m.Cur().Name != "logout" {
		t.Fatalf("a click on another tab's PR number should go to it, active %q", m.Cur().Name)
	}
	if _, err := os.Stat(opened); err == nil {
		t.Fatal("a click on another tab's PR number opened the browser")
	}
	clickOn(t, m, "#8", tea.MouseButtonLeft)
	var got []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, _ = os.ReadFile(opened); len(got) > 0 {
			break
		}
	}
	if strings.TrimSpace(string(got)) != "https://github.com/o/app/pull/8" {
		t.Fatalf("opened %q, want #8", got)
	}
}
