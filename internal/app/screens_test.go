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
func withScreens(t *testing.T, m *Model) {
	t.Helper()
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	m.Screens = core.NewScreenServer(a, func(r core.Request) {
		m.Mu.Lock()
		r.Do(&m.Core)
		m.Mu.Unlock()
	})
	m.SetScreens(core.NewScreenClient(b, func() {}))
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
				m.Mu.Lock()
				m.Screens.Tick(&m.Core, false)
				m.Mu.Unlock()
			}
		}
	}()
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
	m.Mu.Lock()
	login.Pane, login.Light = pane, core.StIdle
	m.Mu.Unlock()
	withScreens(t, m)
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
}
