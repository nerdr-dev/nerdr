package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// senderEnv tells a copy of this test binary to report to the socket it
// names, as an agent's `nerdr pane report-agent` does, and exit.
const senderEnv = "NERDR_TEST_REPORT_TO"

func TestMain(m *testing.M) {
	if path := os.Getenv(senderEnv); path != "" {
		if err := Send(path, ReportAgent, map[string]any{"pane_id": "p", "agent": "claude", "state": "idle"}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestPeerChainPlacesARealProcess: with real processes, one started under
// an agent has that agent in its chain, and one started beside it, though
// it knows everything the agent knows, does not. app's vouch rests on this.
func TestPeerChainPlacesARealProcess(t *testing.T) {
	if !peerSupported {
		t.Skip("nerdr cannot ask the kernel here")
	}
	s, got := listen(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), senderEnv+"="+s.Path())

	// The shell stands for the agent. The command after it keeps the shell
	// from becoming the sender by exec.
	agent := exec.Command("sh", "-c", `"$0"; status=$?; exit $status`, self)
	agent.Env = env
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	under := next(t, got)
	if err := agent.Wait(); err != nil {
		t.Fatal(err)
	}
	if !under.Peer.Known || !slices.Contains(under.Peer.Chain, agent.Process.Pid) || under.Peer.Chain[0] == agent.Process.Pid {
		t.Fatalf("a process under the agent (%d): %+v", agent.Process.Pid, under.Peer)
	}
	if !slices.Contains(under.Peer.Chain, os.Getpid()) {
		t.Fatalf("the chain stops short of this process (%d): %+v", os.Getpid(), under.Peer)
	}

	// Another agent is running while a process beside it reports.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	}()
	beside := exec.Command(self)
	beside.Env = env
	if err := beside.Run(); err != nil {
		t.Fatal(err)
	}
	r := next(t, got)
	if !r.Peer.Known || r.Peer.Chain[0] != beside.Process.Pid {
		t.Fatalf("the process beside it (%d): %+v", beside.Process.Pid, r.Peer)
	}
	if slices.Contains(r.Peer.Chain, other.Process.Pid) {
		t.Fatalf("a process not under the agent (%d) has it in its chain: %+v", other.Process.Pid, r.Peer)
	}
}

// TestSocketIsOnlyTheUsers: only the user can connect to the socket, or
// list the folder it is in.
func TestSocketIsOnlyTheUsers(t *testing.T) {
	base, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	dir := filepath.Join(base, "state")
	path := filepath.Join(dir, "n.sock")
	s, err := Listen(path, func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	for _, c := range []struct {
		path string
		want os.FileMode
	}{{dir, 0o700}, {path, 0o600}, {path + ".lock", 0o600}} {
		st, err := os.Stat(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != c.want {
			t.Errorf("%s has mode %o, want %o", c.path, st.Mode().Perm(), c.want)
		}
	}
}
