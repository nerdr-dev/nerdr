package spawn

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
)

// How a program ended is told as exec tells it, from Local's Wait or from
// an ExitError a holder sent.
func TestExitErrorIsToldAsExecTellsIt(t *testing.T) {
	for _, c := range []struct {
		script string
		status int
	}{
		{"exit 3", 3},
		{"kill -TERM $$", 128 + 15},
		{"kill -KILL $$", 128 + 9},
	} {
		want := exec.Command("sh", "-c", c.script).Run()
		p, err := Local{}.Spawn(Spec{Argv: []string{"sh", "-c", c.script}})
		if err != nil {
			t.Fatal(err)
		}
		got := p.Wait()
		p.Close()
		var ee *ExitError
		if !errors.As(got, &ee) {
			t.Fatalf("%q: Wait = %#v, want an *ExitError", c.script, got)
		}
		if got.Error() != want.Error() {
			t.Errorf("%q: told as %q, exec tells %q", c.script, got, want)
		}
		if ee.Status() != c.status {
			t.Errorf("%q: status %d, want %d", c.script, ee.Status(), c.status)
		}
	}
	if s := (&ExitError{Signal: syscall.SIGSEGV, CoreDumped: true}).Error(); s != "signal: segmentation fault (core dumped)" {
		t.Errorf("core dump told as %q", s)
	}
	p, err := Local{}.Spawn(Spec{Argv: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Wait(); err != nil {
		t.Errorf("exit 0: Wait = %v", err)
	}
}
