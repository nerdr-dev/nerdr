package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/state"
)

func notifyModel(notify string, tabs ...*tab) *Model {
	m := &Model{tabs: tabs}
	m.cfg.Notify = notify
	m.cfg.Prefix = "ctrl+q"
	return m
}

func doneTab(name string) *tab {
	return &tab{Tab: state.Tab{ID: name, Name: name, Agent: agents.Claude}, status: stDone}
}

// In nerdr, a tab behind the one shown says it is done in the bar, not
// on the desktop; the one shown says nothing, being on screen.
func TestBackgroundTabSaysSoInTheBar(t *testing.T) {
	shown, behind := doneTab("shown"), doneTab("behind")
	m := notifyModel("all", shown, behind)
	now := time.Now()
	m.notifyChange(shown, true, now)
	m.notifyChange(behind, false, now)
	m.flushNotes("")
	if len(m.notes) != 0 {
		t.Fatalf("desktop notes while in nerdr: %+v", m.notes)
	}
	if !strings.Contains(m.toastText, "behind: claude is done") || strings.Contains(m.toastText, "shown") ||
		!strings.Contains(m.toastText, "ctrl+q n") || !m.toastUntil.After(now) {
		t.Fatalf("bar: %q", m.toastText)
	}
}

// Two in one tick share the bar.
func TestBackgroundTabsShareTheBar(t *testing.T) {
	a, b := doneTab("a"), doneTab("b")
	b.status = stBlocked
	m := notifyModel("all", a, b)
	now := time.Now()
	m.notifyChange(a, false, now)
	m.notifyChange(b, false, now)
	m.flushNotes("")
	if !strings.Contains(m.toastText, "a: claude is done") || !strings.Contains(m.toastText, "b: claude needs input") {
		t.Fatalf("bar: %q", m.toastText)
	}
}

// Away from nerdr it is the desktop, for the tab shown too.
func TestAwayItIsTheDesktop(t *testing.T) {
	shown := doneTab("shown")
	m := notifyModel("all", shown)
	m.blurred = true
	m.notifyChange(shown, false, time.Now())
	if len(m.notes) != 1 || len(m.inApp) != 0 {
		t.Fatalf("notes %+v, in nerdr %q", m.notes, m.inApp)
	}
}

// notify = "off" turns the bar's news off too, and "blocked" leaves done out.
func TestNotifySettingCoversTheBar(t *testing.T) {
	for _, c := range []struct {
		notify string
		st     status
		want   bool
	}{{"off", stDone, false}, {"blocked", stDone, false}, {"blocked", stBlocked, true}, {"done", stDone, true}} {
		tb := doneTab("x")
		tb.status = c.st
		m := notifyModel(c.notify, tb)
		m.notifyChange(tb, false, time.Now())
		if got := len(m.inApp) == 1; got != c.want {
			t.Errorf("notify %q, %s: %v", c.notify, c.st.label(), got)
		}
	}
}
