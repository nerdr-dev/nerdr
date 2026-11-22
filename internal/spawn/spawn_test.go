package spawn

import (
	"errors"
	"io"
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

// TestLocalSpawnWithoutProgram: a Spec with no Argv is an error, not a
// panic.
func TestLocalSpawnWithoutProgram(t *testing.T) {
	if _, err := (Local{}).Spawn(Spec{}); err == nil {
		t.Fatal("spawning no program did not fail")
	}
}

// TestLocalSignalAfterWait: once Wait has reaped the program and its
// group is seen gone, the group is signalled no more, its id being free
// for another's.
func TestLocalSignalAfterWait(t *testing.T) {
	p, err := Local{}.Spawn(Spec{Argv: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	pid := p.Pid()
	_, _ = io.Copy(io.Discard, p)
	_ = p.Wait()
	if err := p.Signal(pid, syscall.Signal(0)); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal after Wait: %v, want ESRCH", err)
	}
	if !p.(*local).pgGone {
		t.Fatal("the group seen gone was not noted")
	}
	p.Close()
}

// TestLocalSignalWhileWaiting: Signal while Wait waits reaches the
// program, and does not race Wait's reap.
func TestLocalSignalWhileWaiting(t *testing.T) {
	p, err := Local{}.Spawn(Spec{Argv: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	if err := p.Signal(p.Pid(), syscall.SIGKILL); err != nil {
		t.Fatalf("signal while running: %v", err)
	}
	var ee *ExitError
	if err := <-done; !errors.As(err, &ee) || ee.Signal != syscall.SIGKILL {
		t.Fatalf("Wait: %v, want killed", err)
	}
}
