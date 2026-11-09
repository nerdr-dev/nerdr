//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeSSH stands in for ssh, there being no ssh server for the tests: it
// takes ssh's options, logs the host, and runs the command here as the
// user's shell on the host would.
const fakeSSH = `#!/bin/sh
while [ $# -gt 0 ]; do
	case "$1" in
	--) shift; break ;;
	-o) shift 2 ;;
	-*) shift ;;
	*) break ;;
	esac
done
echo "$1 $*" >> "$FAKE_SSH_LOG"
shift
exec /bin/sh -c "$*"
`

// TestConnectOverSSH covers nerdr connect HOST:FOLDER: the TUI here, the
// core of FOLDER started on HOST by nerdr bridge, as ssh runs it. The
// connection lost, as when the network goes, the agent works on there and
// the TUI connects again by itself; closed and connected again, the same
// agent is there.
func TestConnectOverSSH(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	home := filepath.Join(scratch, "home")
	_ = os.MkdirAll(home, 0o755)
	ssh := filepath.Join(scratch, "fakessh")
	if err := os.WriteFile(ssh, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(holderEnv(t, bin, scratch),
		// The login shell on "box" finds the nerdr under test, and reads
		// no profile of the user's that would find another.
		"HOME="+home, "SHELL=/bin/sh",
		"PATH="+filepath.Dir(bin)+":"+os.Getenv("PATH"),
		"NERDR_SSH="+ssh, "FAKE_SSH_LOG="+filepath.Join(scratch, "ssh.log"))
	fake, _ := filepath.Abs("fakeagent.sh")
	bridges := func() []int { return pgrep("-f", "^nerdr bridge "+root+"$") }
	connect := func() *harness {
		return startArgs(t, bin, scratch, isolated(t, env), 160, 40, "connect", "box:"+root)
	}

	h := connect()
	newCrashTab(h)
	h.waitFor(" box ", 2*time.Second)
	h.keys("work\r")
	h.waitFor("done", 5*time.Second)
	agents := pgrep("-f", fake)
	if len(agents) != 1 {
		t.Fatalf("agents %v", agents)
	}
	if log := readFile(filepath.Join(scratch, "ssh.log")); !strings.HasPrefix(log, "box ") {
		t.Fatalf("ssh was asked %q", log)
	}

	// The network goes: the bridge ends, the agent works on, and the TUI
	// connects again.
	bs := bridges()
	if len(bs) != 1 {
		t.Fatalf("bridges %v", bs)
	}
	if err := syscall.Kill(bs[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("bridges now %v; ssh log:\n%s", bridges(), readFile(filepath.Join(scratch, "ssh.log")))
			out, _ := exec.Command("sh", "-c", "ss -xp | grep -E 'nerdr|fakessh|sh\\b' ; pgrep -af '"+bin+"|nerdr bridge'").CombinedOutput()
			t.Logf("sockets:\n%s", out)
		}
	})
	h.waitFor("connected to nerdr's core again", 15*time.Second)
	if now := pgrep("-f", fake); len(now) != 1 || now[0] != agents[0] {
		t.Fatalf("agent %v, was %v", now, agents)
	}
	h.keys("hooked\r")
	h.waitFor("thinking quietly", 5*time.Second)

	// Closed and connected again: the same agent, its work done meanwhile.
	endClient(t, h, "c")
	time.Sleep(5 * time.Second)
	h = connect()
	h.waitTabBar("1:crash", 10*time.Second)
	h.waitFor("hooked over", 5*time.Second)
	if now := pgrep("-f", fake); len(now) != 1 || now[0] != agents[0] {
		t.Fatalf("agent %v, was %v", now, agents)
	}

	endClient(t, h, "q")
	for deadline := time.Now().Add(15 * time.Second); syscall.Kill(agents[0], 0) != syscall.ESRCH; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent lives on after quitting")
		}
	}
}
