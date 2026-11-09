package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// The programs in no tab are listed, each to be stopped on its own or
// all at once, after asking; with none, nerdr says so.
func TestProgramsInNoTabAreListed(t *testing.T) {
	m := testModel(t)
	m.openUnseen()
	if m.dialog != nil {
		t.Fatal("a list of none")
	}
	now := time.Now()
	m.home().Core.Unseen = []core.UnseenProgram{
		{ID: "agent/1", What: "a reviewer of tab parser's work", Pid: 41, Sandboxed: true, Started: now.Add(-3 * time.Hour)},
		{ID: "shell/2", What: "terminal 1 of a tab since closed", Pid: 42, Started: now.Add(-5 * time.Minute)},
	}
	m.openUnseen()
	mn, ok := m.dialog.(*menu)
	if !ok || len(mn.items) != 4 {
		t.Fatalf("dialog %#v", m.dialog)
	}
	if l := mn.items[1].label; !strings.Contains(l, "pid 41") || !strings.Contains(l, "in a sandbox") || !strings.Contains(l, "started 3 h ago") {
		t.Errorf("line %q", l)
	}
	if l := mn.items[2].label; !strings.Contains(l, "not in a sandbox") || !strings.Contains(l, "started 5 min ago") {
		t.Errorf("line %q", l)
	}
	if mn.items[3].label != "Stop all 2" {
		t.Errorf("last %q", mn.items[3].label)
	}
	mn.items[2].run(m)
	c, ok := m.dialog.(*confirm)
	if !ok || !c.defaultNo || !strings.Contains(c.title, "terminal 1 of a tab since closed") {
		t.Fatalf("asked %#v", m.dialog)
	}
}
