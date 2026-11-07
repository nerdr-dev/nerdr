package core

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
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

// fakeProc is a program run elsewhere, as a holder's would be: it has
// printed out, and ends with exit.
type fakeProc struct {
	out  string
	exit error
}

func (f *fakeProc) Read(b []byte) (int, error) {
	if f.out == "" {
		return 0, io.EOF
	}
	n := copy(b, f.out)
	f.out = f.out[n:]
	return n, nil
}
func (f *fakeProc) Write(b []byte) (int, error)             { return 0, os.ErrInvalid }
func (f *fakeProc) Resize(w, h int) error                   { return nil }
func (f *fakeProc) Wait() error                             { return f.exit }
func (f *fakeProc) Pid() int                                { return 0 }
func (f *fakeProc) StartTime() uint64                       { return 0 }
func (f *fakeProc) Foreground() (int, bool)                 { return 0, false }
func (f *fakeProc) Signal(pg int, sig syscall.Signal) error { return syscall.ESRCH }
func (f *fakeProc) Close() error                            { return nil }

type fakeSpawner struct{ p *fakeProc }

func (s fakeSpawner) Spawn(spawn.Spec) (spawn.Proc, error) { return s.p, nil }

// A run that did not run here, with no exec.Cmd to it, ends with the
// status it was told of.
func TestRunProcExitFromElsewhere(t *testing.T) {
	for _, c := range []struct {
		exit error
		want int
	}{
		{nil, 0},
		{&spawn.ExitError{Code: 3}, 3},
		{&spawn.ExitError{Signal: syscall.SIGTERM}, 128 + 15},
		{errors.New("lost"), 1},
	} {
		p, err := StartProc(fakeSpawner{&fakeProc{out: "out\n", exit: c.exit}}, spawn.Spec{Argv: []string{"x"}}, func() {})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		var done bool
		var exit int
		for until := time.Now().Add(5 * time.Second); !done; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(until) {
				t.Fatalf("%v: did not end", c.exit)
			}
			text, _, done, exit = p.Since(0)
		}
		if text != "out\n" || exit != c.want {
			t.Errorf("%v: printed %q, exited %d, want %d", c.exit, text, exit, c.want)
		}
	}
}
