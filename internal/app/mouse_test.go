package app

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nerdr/internal/config"
	"nerdr/internal/input"
	"nerdr/internal/state"
	"nerdr/internal/term"
	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestScanZones(t *testing.T) {
	m := &Model{}
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

// testModel is a model with two tabs in repo "app" and one in "lib",
// drawn at 120×30, with nothing running.
func testModel(t *testing.T) *Model {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	yes, no := true, false
	cfg := config.Config{Prefix: "ctrl+q", PanelWidth: 40, Layout: "agent", TabDetails: &no, Mouse: &yes}
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
func clickOn(t *testing.T, m *Model, s string, b tea.MouseButton) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, s); i >= 0 {
			x := ansi.StringWidth(line[:i])
			m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
			m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionRelease})
			m.View()
			return
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", s, ansi.Strip(m.View()))
}

func TestClickTabsAndGroups(t *testing.T) {
	m := testModel(t)
	clickOn(t, m, "logout", tea.MouseButtonLeft)
	if m.cur().Name != "logout" {
		t.Fatalf("active %q after clicking logout", m.cur().Name)
	}
	clickOn(t, m, "lib (1)", tea.MouseButtonLeft)
	if m.cur().Name != "parse" {
		t.Fatalf("active %q after clicking group lib", m.cur().Name)
	}
	// The wheel over the group row steps through the groups.
	m.View()
	m.Update(tea.MouseMsg{X: 100, Y: 0, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cur().group() != "app" {
		t.Fatalf("group %q after the wheel", m.cur().group())
	}
}

// TestWheelBurst: a burst of the wheel over the tabs — a wheel spinning
// on, or a trackpad gliding — steps one tab, not one for every report.
func TestWheelBurst(t *testing.T) {
	m := testModel(t)
	m.View()
	y := m.tabRow()
	before := m.cur().Name
	for range 30 {
		m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	after := m.cur().Name
	if after == before {
		t.Fatal("the wheel over the tabs did not step")
	}
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cur().Name != after {
		t.Fatalf("stepped on to %s straight after", m.cur().Name)
	}
	// Once the gap has passed, the next notch steps again.
	m.lastWheelStep = m.lastWheelStep.Add(-wheelGap)
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cur().Name == after {
		t.Fatal("the wheel did not step after the gap")
	}
}

// TestWheelAfterFocus: the wheel is not heard while nerdr is in the
// background, nor after it gets the focus back while the wheel goes on;
// once it has been still, it is.
func TestWheelAfterFocus(t *testing.T) {
	m := testModel(t)
	no := false
	m.cfg.HerdrFocusJump = &no
	m.View()
	wheel := func() {
		m.lastWheelStep = time.Time{} // not what this is about
		m.Update(tea.MouseMsg{X: 5, Y: m.tabRow(), Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	start := m.cur().Name
	m.Update(tea.BlurMsg{})
	wheel()
	m.Update(tea.FocusMsg{})
	wheel()
	if m.cur().Name != start {
		t.Fatalf("the wheel stepped to %s while gliding in from the background", m.cur().Name)
	}
	m.lastWheel = m.lastWheel.Add(-wheelQuiet)
	wheel()
	if m.cur().Name == start {
		t.Fatal("the wheel is not heard after being still")
	}
}

func TestTabMenu(t *testing.T) {
	m := testModel(t)
	clickOn(t, m, "logout", tea.MouseButtonRight)
	mu, ok := m.dialog.(*menu)
	if !ok || m.cur().Name != "logout" {
		t.Fatalf("right click: dialog %T, tab %q", m.dialog, m.cur().Name)
	}
	if m.mouse != mouseAll {
		t.Fatal("the menu does not follow the pointer")
	}
	// Hovering moves the highlight.
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, "Rename…"); i >= 0 {
			m.Update(tea.MouseMsg{X: ansi.StringWidth(line[:i]), Y: y, Action: tea.MouseActionMotion})
		}
	}
	if mu.items[mu.hi].label != "Rename…" {
		t.Fatalf("highlighted %q", mu.items[mu.hi].label)
	}
	clickOn(t, m, "Rename…", tea.MouseButtonLeft)
	r, ok := m.dialog.(*renamer)
	if !ok || r.t.Name != "logout" {
		t.Fatalf("dialog %T after Rename…", m.dialog)
	}
	// A click outside a dialog holding typed text does not close it; its
	// buttons work.
	m.Update(tea.MouseMsg{X: 0, Y: 29, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.dialog == nil {
		t.Fatal("click outside closed the rename dialog")
	}
	clickOn(t, m, "esc cancel", tea.MouseButtonLeft)
	if m.dialog != nil {
		t.Fatalf("still open: %T", m.dialog)
	}
}

func TestMenuClosesOnClickOutside(t *testing.T) {
	m := testModel(t)
	clickOn(t, m, "≡ menu", tea.MouseButtonLeft)
	if _, ok := m.dialog.(*menu); !ok {
		t.Fatalf("dialog %T after ≡", m.dialog)
	}
	m.Update(tea.MouseMsg{X: 0, Y: 29, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.dialog != nil {
		t.Fatalf("still open: %T", m.dialog)
	}
}

func TestPlusOpensNewTabForTheGroup(t *testing.T) {
	m := testModel(t)
	// The + after the tabs: a new tab in the active group's repository.
	// The repository is not on disk here, so the wizard stays at the
	// first step, where it lists what it found.
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	x := strings.LastIndex(lines[m.tabRow()], " + ")
	m.Update(tea.MouseMsg{X: ansi.StringWidth(lines[m.tabRow()][:x]) + 1, Y: m.tabRow(), Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if _, ok := m.dialog.(*wizard); !ok {
		t.Fatalf("dialog %T after +", m.dialog)
	}
}

func TestDragTheRule(t *testing.T) {
	m := testModel(t)
	m.runCommand("z e") // show the review panel
	m.View()
	aw := m.agentWidth()
	m.Update(tea.MouseMsg{X: aw, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: 60, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m.Update(tea.MouseMsg{X: 60, Y: 10, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	// Within a column: the width is kept in percent.
	if aw := m.agentWidth(); aw < 59 || aw > 61 {
		t.Fatalf("review %d%%, agent %d columns", m.reviewWidth, m.agentWidth())
	}
}

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
	if s, _ := state.Load(m.root); s.SideWidth != auto+9 {
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
	if m.hover.shown || m.cur().Name != "logout" {
		t.Fatalf("after a click: tip shown %v, tab %q", m.hover.shown, m.cur().Name)
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
	m.prWatch.at = time.Now() // known already: GitHub is not asked
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
	m.cfg.Shell = script
	m.cur().Dir = dir
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
	m.cfg.Shell = script
	m.cur().Dir = dir
	agent, err := term.Start([]string{"sh", "-c", "echo agent-here; sleep 5"}, dir, os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	m.cur().pane = agent
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

// A program's OSC 52 reaches the clipboard only from the pane the user is
// in, just after they did something: a program elsewhere, or one copying
// unasked, does not put what the user pastes next there.
func TestOSC52OnlyFromThePaneInUse(t *testing.T) {
	m := testModel(t)
	var copied []string
	was := toClipboard
	toClipboard = func(s string) bool { copied = append(copied, s); return true }
	defer func() { toClipboard = was }()
	copying := func(text string) *term.Pane {
		p, err := term.Start([]string{"sh", "-c", `printf '\033]52;c;` + base64.StdEncoding.EncodeToString([]byte(text)) + `\007'; sleep 5`},
			t.TempDir(), os.Environ(), 40, 5, func() {})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { p.Stop(time.Second) })
		return p
	}
	m.tabs[0].pane, m.tabs[1].pane = copying("here"), copying("elsewhere")
	time.Sleep(300 * time.Millisecond)

	// Nobody has typed: nothing is taken, from the pane in use either.
	m.router = input.New(strings.NewReader(""), 0x11)
	m.checkCopied()
	// Typed just now: the pane in use's next copy is taken, the other's
	// not.
	m.router = input.New(strings.NewReader("x"), 0x11)
	go func() { _, _ = io.Copy(io.Discard, m.router.Reader()) }()
	go m.router.Run()
	for m.router.LastInput().IsZero() {
		time.Sleep(5 * time.Millisecond)
	}
	if len(copied) != 0 {
		t.Fatalf("copied unasked: %q", copied)
	}
	m.tabs[0].pane, m.tabs[1].pane = copying("here"), copying("elsewhere")
	time.Sleep(300 * time.Millisecond)
	m.checkCopied()
	if len(copied) != 1 || copied[0] != "here" {
		t.Errorf("copied %q, want only the pane in use's", copied)
	}
}
