package holder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// serve is a holder in this process and a client of it, as a core would
// be.
func serve(t *testing.T) (*Holder, *Client) {
	t.Helper()
	h := New()
	return h, connect(t, h)
}

// connect is a new core's client of h.
func connect(t *testing.T, h *Holder) *Client {
	t.Helper()
	a, b, err := connPair()
	if err != nil {
		t.Fatal(err)
	}
	go h.Serve(a)
	cl, err := newClient(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

// shutdown stops what h still runs once the test is done.
func shutdown(t *testing.T, h *Holder) {
	t.Cleanup(func() { h.shutdown(time.Second) })
}

// readUntil reads p until what it read has want, or fails.
func readUntil(t *testing.T, p spawn.Proc, want string) string {
	t.Helper()
	var got []byte
	buf := make([]byte, 4096)
	done := make(chan struct{})
	timer := time.AfterFunc(5*time.Second, func() { close(done) })
	defer timer.Stop()
	for !bytes.Contains(got, []byte(want)) {
		select {
		case <-done:
			t.Fatalf("read %q, never %q", got, want)
		default:
		}
		n, err := p.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			if !bytes.Contains(got, []byte(want)) {
				t.Fatalf("read %q then %v, never %q", got, err, want)
			}
		}
	}
	return string(got)
}

func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s took over %v", what, d)
	}
}

// A program in the holder ends as exec tells it, as one Local started.
func TestHolderTellsAnExitAsExecDoes(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	for _, script := range []string{"exit 3", "kill -TERM $$", "kill -KILL $$", "true"} {
		for _, pty := range []bool{false, true} {
			want := exec.Command("sh", "-c", script).Run()
			p, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", script}, PTY: pty, W: 80, H: 24})
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, p)
			got := p.Wait()
			p.Close()
			if (got == nil) != (want == nil) || got != nil && got.Error() != want.Error() {
				t.Errorf("%q (pty %v): told as %v, exec tells %v", script, pty, got, want)
			}
			var ee *spawn.ExitError
			if got != nil && !errors.As(got, &ee) {
				t.Errorf("%q: %T, want *spawn.ExitError", script, got)
			}
		}
	}
}

// A terminal program reads what is written to it, at the size it is
// given, and the core reads what it prints; without one, what it is given
// as standard input.
func TestHolderRunsATerminalAndAPipe(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", "stty size; read x; echo got $x; stty size"}, PTY: true, W: 100, H: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	readUntil(t, p, "30 100")
	if err := p.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	if n, err := p.Write([]byte("hello\r")); err != nil || n != 6 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	readUntil(t, p, "20 90")
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}

	q, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", "cat; echo err >&2"}, Stdin: []byte("in\n")})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	out, _ := io.ReadAll(q)
	if string(out) != "in\nerr\n" {
		t.Errorf("read %q", out)
	}
	if _, err := q.Write([]byte("x")); err != os.ErrInvalid {
		t.Errorf("write without a terminal: %v", err)
	}
	if err := q.Wait(); err != nil {
		t.Error(err)
	}
}

// The holder reaps a program only once the core has its exit: until then
// its PID is a zombie's and cannot go to another process, and a signal
// for it is still the holder's to send.
func TestHolderReapsOnlyOnceTheCoreHasTheExit(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	pid := p.Pid()
	pp := p.(*Proc)
	within(t, 5*time.Second, "the exit", func() { <-pp.exited })
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("%d is gone before the core said it had its exit: %v", pid, err)
	}
	progs, _ := cl.List()
	if len(progs) != 1 || progs[0].State != Exited {
		t.Fatalf("list: %+v", progs)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Errorf("%d is still there after the ack: %v", pid, err)
	}
	if err := p.Signal(pid, 0); err != syscall.ESRCH {
		t.Errorf("a reaped program's group can still be signalled: %v", err)
	}
	p.Close()
	if progs, _ := cl.List(); len(progs) != 0 {
		t.Errorf("forgotten program still listed: %+v", progs)
	}
}

// A core that does not read never holds a program up: when the ring is
// full the oldest output goes, and a read says how much.
func TestFullRingNeverHoldsAProgramUp(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	for _, pty := range []bool{false, true} {
		p, err := cl.SpawnAs(newID(), spawn.Spec{
			Argv: []string{"sh", "-c", "head -c 3000000 /dev/zero | tr '\\0' a; echo; echo end"},
			PTY:  pty, W: 80, H: 24,
		}, Mark{}, 64<<10)
		if err != nil {
			t.Fatal(err)
		}
		within(t, 20*time.Second, "a program no one reads", func() { <-p.exited })
		got := readUntil(t, p, "end")
		if p.Lost() < 2_000_000 {
			t.Errorf("pty %v: lost %d bytes, want most of 3 MB", pty, p.Lost())
		}
		if len(got) > 64<<10 {
			t.Errorf("pty %v: read %d bytes from a 64 KiB ring", pty, len(got))
		}
		p.Wait()
		p.Close()
	}
}

// What a spawn marks a program with stays as it was, and nothing spawns
// over it.
func TestMarkStaysAsSpawned(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.SpawnAs("agent", spawn.Spec{Argv: []string{"sleep", "60"}}, Mark{Sandboxed: true, Secret: "s3cret", Label: "claude"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := cl.SpawnAs("agent", spawn.Spec{Argv: []string{"true"}}, Mark{}, 0); err == nil {
		t.Error("a second spawn took the id")
	}
	cl.Close()
	progs, _ := connect(t, h).List()
	if len(progs) != 1 || !progs[0].Sandboxed || progs[0].Secret != "s3cret" || progs[0].Label != "claude" || progs[0].Pid != p.Pid() {
		t.Errorf("list: %+v", progs)
	}
}

// The holder signals a program's own group, and on a terminal the groups
// of the session it leads; no other process.
func TestSignalsGoOnlyToTheProgramsGroups(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", "sleep 60 & wait"}, PTY: true, W: 80, H: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Signal(syscall.Getpgrp(), 0); err != syscall.EPERM {
		t.Errorf("signal to the test's own group: %v", err)
	}
	if err := p.Signal(p.Pid(), 0); err != nil {
		t.Errorf("signal to its own group: %v", err)
	}
	if fg, ok := p.Foreground(); !ok || fg != p.Pid() {
		t.Errorf("foreground %d %v, want %d", fg, ok, p.Pid())
	}
	p.Signal(p.Pid(), syscall.SIGKILL)
	if err := p.Wait(); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Errorf("Wait = %v", err)
	}
}

// With no core, a program goes on and its output is kept; the next core
// reads it from the start, and is told of the exits it missed.
func TestANewCoreFindsWhatTheHolderKept(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.SpawnAs("shell", spawn.Spec{Argv: []string{"sh", "-c", "read x; echo got $x; exit 4"}, PTY: true, W: 80, H: 24}, Mark{Label: "shell"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Checkpoint("shell", 0, []byte("screen")); err != nil {
		t.Fatal(err)
	}
	cl.Close()
	// The core is gone; the program is written to by no one, so poke it
	// here and let it end.
	h.mu.Lock()
	out := h.progs["shell"].out
	h.mu.Unlock()
	if err := control(out, func(fd int) error { _, err := syscall.Write(fd, []byte("x\r")); return err }); err != nil {
		t.Fatal(err)
	}
	_ = p

	cl2 := connect(t, h)
	var q *Proc
	within(t, 5*time.Second, "the exit", func() {
		for {
			progs, err := cl2.List()
			if err != nil {
				t.Error(err)
				return
			}
			if len(progs) == 1 && progs[0].State == Exited {
				if progs[0].Checkpoint != 0 || progs[0].Label != "shell" {
					t.Errorf("list: %+v", progs[0])
				}
				q = cl2.Attach(progs[0], 0)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if pos, blk := h.Checkpoint("shell"); pos != 0 || string(blk) != "screen" {
		t.Errorf("checkpoint %d %q", pos, blk)
	}
	readUntil(t, q, "got x")
	if err := q.Wait(); err == nil || err.Error() != "exit status 4" {
		t.Errorf("Wait = %v", err)
	}
	q.Close()
}

// shutdown stops every program, those that ignore a hangup too.
func TestShutdownStopsEveryProgram(t *testing.T) {
	h, cl := serve(t)
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", "trap '' HUP; echo up; sleep 60"}})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "up")
	pid := p.Pid()
	h.shutdown(100 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Errorf("%d is still there: %v", pid, err)
	}
}

// The holder has no logic: it does not import the core, nor anything
// that draws.
func TestHolderImportsNoCore(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skip("go list:", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range []string{"/internal/core", "/internal/app", "/internal/term", "bubbletea", "lipgloss"} {
			if strings.HasSuffix(dep, bad) || strings.Contains(dep, bad+"/") || strings.Contains(dep, bad) && !strings.Contains(bad, "/") {
				t.Errorf("holder depends on %s", dep)
			}
		}
	}
}

// An upgrade whose exec fails leaves the holder as it was: the program
// runs, a read waiting goes on, and the core is told why.
func TestFailedUpgradeLeavesTheHolderAsItWas(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	h.mu.Lock()
	h.execArgv = func() ([]string, error) { return []string{"/nonexistent/nerdr", "holder"}, nil }
	h.upgrade = h.doUpgrade
	h.mu.Unlock()
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", "read x; echo got $x"}, PTY: true, W: 80, H: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got := make(chan string, 1)
	go func() {
		var b []byte
		buf := make([]byte, 256)
		for !bytes.Contains(b, []byte("got x")) {
			n, err := p.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				break
			}
		}
		got <- string(b)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := cl.Upgrade(); err == nil || !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("Upgrade = %v", err)
	}
	if _, err := p.Write([]byte("x\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-got:
		if !strings.Contains(out, "got x") {
			t.Errorf("read %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read waiting before the upgrade never came")
	}
	if err := p.Wait(); err != nil {
		t.Error(err)
	}
}

// What is left of a program's group once it is reaped can still be
// stopped, as Pane.Stop and RunProc.Stop do; once the group is seen gone,
// its id is never signalled again.
func TestWhatIsLeftOfTheGroupCanBeStopped(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"sh", "-c", "trap '' HUP TERM; sleep 60 & exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	pid := p.Pid()
	if err := p.Signal(pid, 0); err != nil {
		t.Fatalf("the group with a sleep left in it: %v", err)
	}
	if err := p.Signal(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	within(t, 5*time.Second, "the group to go", func() {
		for p.Signal(pid, 0) != syscall.ESRCH {
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err := p.Signal(pid, 0); err != syscall.ESRCH {
		t.Errorf("a group seen gone is signalled again: %v", err)
	}
	p.Close()
	if progs, _ := cl.List(); len(progs) != 0 {
		t.Errorf("still listed: %+v", progs)
	}
}

// A program the core lets go of before it exits is reaped and forgotten
// by the holder when it does, with no ack.
func TestAProgramClosedBeforeItExitsIsReapedWhenItDoes(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	p, err := cl.Spawn(spawn.Spec{Argv: []string{"sleep", "0.2"}})
	if err != nil {
		t.Fatal(err)
	}
	pid := p.Pid()
	p.Close()
	within(t, 5*time.Second, "it to be forgotten", func() {
		for {
			progs, _ := cl.List()
			if len(progs) == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Errorf("%d not reaped: %v", pid, err)
	}
	if err := p.Wait(); err != nil {
		t.Errorf("Wait after Close: %v", err)
	}
}
