package core

import (
	"slices"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// recorder starts programs as spawn.Local does, and keeps what it was
// asked to start.
type recorder struct{ specs []spawn.Spec }

func (r *recorder) Spawn(s spawn.Spec) (spawn.Proc, error) {
	r.specs = append(r.specs, s)
	return spawn.Local{}.Spawn(s)
}

// TestTerminalStartsThroughSpawner: core starts a tab's programs through
// its Spawner, which a holder can stand in for, not by itself.
func TestTerminalStartsThroughSpawner(t *testing.T) {
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec}
	m.Cfg.Shell = "cat"
	tab := &Tab{}
	tab.Dir = t.TempDir()
	p, _ := m.NewShellIn(tab, true)
	if p == nil || p.Shell == nil {
		t.Fatal("the terminal did not start")
	}
	defer p.Shell.Close()
	if len(rec.specs) != 1 {
		t.Fatalf("spawned %d programs, want 1", len(rec.specs))
	}
	s := rec.specs[0]
	if !s.PTY || !slices.Equal(s.Argv, []string{"cat"}) || s.Dir != tab.Dir || !slices.Contains(s.Env, "NERDR_SHELL=1") {
		t.Fatalf("spawned %+v", s)
	}
	if p.Shell.Pid() == 0 {
		t.Fatal("no process")
	}
}
