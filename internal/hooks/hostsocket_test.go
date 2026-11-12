package hooks

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/rundir"
)

// TestHostSocketIsBesideTheClientSockets: the socket of the agents
// outside a sandbox is the folder's own, in the client sockets' folder,
// not where the sandbox's is, and a second nerdr's is another.
func TestHostSocketIsBesideTheClientSockets(t *testing.T) {
	dir, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	run := filepath.Join(dir, "run")
	t.Setenv(rundir.EnvDir, run)
	a, err := HostSocketPath("/some/repo")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := HostSocketPath("/other/repo")
	if filepath.Dir(a) != run || a == b || filepath.Dir(a) == filepath.Dir(SocketPath()) {
		t.Fatalf("host sockets %s, %s; sandbox's %s", a, b, SocketPath())
	}
	got := make(chan Report, 1)
	s, err := ListenHost("/some/repo", func(r Report) { got <- r })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if fi, err := os.Stat(run); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("its folder: %v %v", fi, err)
	}
	if err := Send(a, ReportAgent, map[string]any{"pane_id": "p", "agent": "claude", "state": "idle"}); err != nil {
		t.Fatal(err)
	}
	next(t, got)
}

// TestReclaimListensAgain: a socket put in the place of nerdr's, as an
// agent whose sandbox lets it write the path could, is found and taken
// back; what connects after that reaches nerdr again.
func TestReclaimListensAgain(t *testing.T) {
	s, got := listen(t)
	if replaced, err := s.Reclaim(); replaced || err != nil {
		t.Fatalf("reclaimed its own: %v %v", replaced, err)
	}
	if err := os.Remove(s.Path()); err != nil {
		t.Fatal(err)
	}
	fake, err := net.Listen("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()
	go func() {
		c, err := fake.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	told := make(chan error, 1)
	s.Guard(10*time.Millisecond, func(err error) { told <- err })
	select {
	case err := <-told:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the replaced socket was not noticed")
	}
	if err := Send(s.Path(), ReportAgent, map[string]any{"pane_id": "p", "agent": "claude", "state": "idle"}); err != nil {
		t.Fatalf("nerdr does not answer at its path again: %v", err)
	}
	next(t, got)
	s.Close()
	if _, err := os.Lstat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("socket left after Close: %v", err)
	}
}
