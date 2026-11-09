package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// withScreens has m draw core's programs from their screens sent as rows
// over a pair of connections, as with NERDR_HOLDER=1; the loop's ticks are
// played by the test.
func withScreens(t *testing.T, m *Model) chan any {
	t.Helper()
	told := make(chan any, 8)
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	m.home().Core.Screens = core.NewScreenServer(a, func(ev core.RequestEv) {
		m.home().Core.Mu.Lock()
		ev.Run(&m.home().Core)
		m.home().Core.Mu.Unlock()
	})
	m.SetScreens(core.NewScreenClient(b, func(msg any) {
		if _, ok := msg.(core.ScreensMsg); !ok {
			told <- msg
		}
	}))
	stop, done := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-done
		a.Close("")
		b.Close("")
	})
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				m.home().Core.Mu.Lock()
				m.home().Core.Screens.Tick(&m.home().Core, false)
				m.home().Core.Mu.Unlock()
			}
		}
	}()
	return told
}

// viewUntil draws m until what it shows has s.
func viewUntil(t *testing.T, m *Model, s string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := ansi.Strip(m.View())
		if strings.Contains(v, s) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("never shown: %q\n%s", s, v)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTUIDrawsFromRows: with the screens sent as rows, the TUI draws an
// agent from its copy, sized as the TUI lays it out, the keys typed go to
// the program, and scrolling back shows its scrollback.
func TestTUIDrawsFromRows(t *testing.T) {
	m := testModel(t)
	login := m.Cur()
	pane, err := term.Start([]string{"sh", "-c", `stty -echo; i=0; while [ $i -lt 60 ]; do echo "row $i"; i=$((i+1)); done; while read l; do echo "got $l"; done`},
		t.TempDir(), nil, 60, 10, func() {})
	if err != nil {
		t.Skip(err)
	}
	defer pane.Close()
	m.home().Core.Mu.Lock()
	login.Pane, login.Light = pane, core.StIdle
	m.home().Core.Mu.Unlock()
	told := withScreens(t, m)
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})

	viewUntil(t, m, "row 59")
	kp := m.keyPane()
	if _, ok := kp.(*term.Copy); !ok {
		t.Fatalf("the keys go to %T, not the copy", kp)
	}
	w, h := m.tabSize(login)
	deadline := time.Now().Add(5 * time.Second)
	for pw, ph := pane.Size(); pw != w || ph != h; pw, ph = pane.Size() {
		if time.Now().After(deadline) {
			t.Fatalf("the program is %d×%d, laid out %d×%d", pw, ph, w, h)
		}
		m.View()
		time.Sleep(5 * time.Millisecond)
	}
	kp.Input([]byte("hello\r"))
	viewUntil(t, m, "got hello")

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.Update(keyMsg("b"))
	for range 10 {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	want := pane.Frame(m.tl(login).scroll)
	// The first row has the scrollback's note over it.
	viewUntil(t, m, strings.TrimRight(ansi.Strip(term.Draw(want, false, nil)[1]), " "))

	// Lines scrolled past, which the copy does not have, are copied once
	// core has sent them; the TUI waits for nothing meanwhile.
	var copied string
	was := toClipboard
	toClipboard = func(s string) bool { copied = s; return true }
	defer func() { toClipboard = was }()
	m.sel = term.Selection{From: term.Point{Line: 0}, To: term.Point{Line: 1, Col: 59}}
	m.copySelection(kp)
	if copied != "" {
		t.Fatalf("copied %q before the lines came", copied)
	}
	select {
	case msg := <-told:
		m.Update(msg)
	case <-time.After(5 * time.Second):
		t.Fatal("the lines asked for never came")
	}
	if copied != "row 0\nrow 1" {
		t.Fatalf("copied %q", copied)
	}
	// Lines shown are copied there and then.
	top := m.screen(login).TopAt(m.tl(login).scroll)
	m.sel = term.Selection{From: term.Point{Line: top + 1}, To: term.Point{Line: top + 1, Col: 59}}
	m.copySelection(kp)
	if copied != strings.TrimRight(ansi.Strip(term.Draw(want, false, nil)[1]), " ") {
		t.Fatalf("copied %q from what is shown", copied)
	}
}
