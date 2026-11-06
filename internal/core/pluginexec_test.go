package core

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// A run's output is read to the end, and its exit status is the shell's:
// the code it exited with, or 128 and the signal that ended it.
func TestRunProcOutputAndExit(t *testing.T) {
	for _, c := range []struct {
		script string
		exit   int
	}{
		{"echo out; echo err >&2; exit 3", 3},
		{"echo out; echo err >&2; kill -TERM $$", 128 + 15},
	} {
		p, err := StartProc(spawn.Local{}, spawn.Spec{Argv: []string{"sh", "-c", c.script}}, func() {})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		var done bool
		var exit int
		for until := time.Now().Add(5 * time.Second); !done; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(until) {
				t.Fatalf("%q did not end", c.script)
			}
			text, _, done, exit = p.Since(0)
		}
		if !strings.Contains(text, "out") || !strings.Contains(text, "err") {
			t.Errorf("%q printed %q", c.script, text)
		}
		if exit != c.exit {
			t.Errorf("%q exited %d, want %d", c.script, exit, c.exit)
		}
	}
}
