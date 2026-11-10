//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServersBesideThisMachine covers [[servers]]: nerdr started here shows
// this machine's core and that of a server, "box", reached over (a fake)
// ssh, with a tab on each, the one on box made by moving the new-tab
// dialog there. box's connection lost, its agent works on there and the
// TUI connects to it again, this machine's agent working on meanwhile.
func TestServersBesideThisMachine(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	home := filepath.Join(scratch, "home")
	_ = os.MkdirAll(home, 0o755)
	ssh := filepath.Join(scratch, "fakessh")
	if err := os.WriteFile(ssh, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(holderEnv(t, bin, scratch),
		"HOME="+home, "SHELL=/bin/sh",
		"PATH="+filepath.Dir(bin)+":"+os.Getenv("PATH"),
		"NERDR_SSH="+ssh, "FAKE_SSH_LOG="+filepath.Join(scratch, "ssh.log"))
	// box's folder is repoB: a core of its own, beside this one's of root.
	boxDir := filepath.Join(root, "repoB")
	cfg := filepath.Join(scratch, "config", "nerdr", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n[[servers]]\nname = \"box\"\nfolder = \"" + boxDir + "\"\n")
	f.Close()
	fake, _ := filepath.Abs("fakeagent.sh")

	h := startArgs(t, bin, root, isolated(t, env), 160, 40)
	newCrashTab(h)
	// box is the same machine here, its core another's: it says the
	// socket for the agents' reports is taken, which covers the bar.
	if log := readFile(filepath.Join(scratch, "ssh.log")); !strings.HasPrefix(log, "box ") {
		t.Fatalf("ssh was asked %q", log)
	}
	h.keys("work\r")
	h.waitFor("done", 5*time.Second)

	// A tab on box: the new-tab dialog moved there with ctrl+o.
	h.keys(prefix + "c")
	h.waitFor("New tab · on this machine", 5*time.Second)
	h.keys("\x0f")
	h.waitFor("New tab · on box", 5*time.Second)
	h.waitFor("repoB", 10*time.Second)
	h.keys("\r")
	h.waitFor("branch:", 5*time.Second)
	h.keys("remote\r")
	h.waitFor("Agent:", 5*time.Second)
	h.keys("\r")
	h.waitTabBar("1:remote", 15*time.Second)
	h.waitFor("? for shortcuts", 10*time.Second)
	agents := pgrep("-f", fake)
	if len(agents) != 2 {
		t.Fatalf("agents %v", agents)
	}

	// box's network goes: its bridge ends, both agents work on, and the
	// TUI connects to box again.
	bridges := pgrep("-f", "^nerdr bridge "+boxDir+"$")
	if len(bridges) != 1 {
		t.Fatalf("bridges %v", bridges)
	}
	if err := syscall.Kill(bridges[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	h.waitFor("connected to box again", 15*time.Second)
	for _, a := range agents {
		if syscall.Kill(a, 0) != nil {
			t.Fatalf("agent %d gone; agents now %v", a, pgrep("-f", fake))
		}
	}
	h.keys("hooked\r")
	h.waitFor("thinking quietly", 5*time.Second)

	endClient(t, h, "q")
	for deadline := time.Now().Add(15 * time.Second); len(pgrep("-f", fake)) > 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("agents live on after quitting: %v", pgrep("-f", fake))
		}
	}
}

// fakeSSHLogin is fakeSSH for a host that wants a password: asked on the
// terminal, as ssh does, and refused without one, as with BatchMode.
const fakeSSHLogin = `#!/bin/sh
case " $* " in
*" BatchMode=yes "*) echo "Permission denied (publickey,password)." >&2; exit 255 ;;
esac
while [ $# -gt 0 ]; do
	case "$1" in
	--) shift; break ;;
	-o) shift 2 ;;
	-*) shift ;;
	*) break ;;
	esac
done
printf "%s's password: " "$1" >/dev/tty
stty -echo </dev/tty
read pw </dev/tty
stty echo </dev/tty
printf "\r\n" >/dev/tty
if [ "$pw" != "$FAKE_SSH_PASSWORD" ]; then
	echo "Permission denied, please try again." >&2
	exit 255
fi
echo "$1 $*" >> "$FAKE_SSH_LOG"
shift
exec /bin/sh -c "$*"
`

// TestServerLogin covers a server that wants a password: added from the
// Machines dialog, the TUI pauses for ssh to ask; at the next start ssh
// asks before the TUI is there; and one disconnected from stays so at the
// start after, as the client keeps it, until connected to again.
func TestServerLogin(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	home := filepath.Join(scratch, "home")
	_ = os.MkdirAll(home, 0o755)
	ssh := filepath.Join(scratch, "fakessh")
	if err := os.WriteFile(ssh, []byte(fakeSSHLogin), 0o755); err != nil {
		t.Fatal(err)
	}
	env := isolated(t, append(holderEnv(t, bin, scratch),
		"HOME="+home, "SHELL=/bin/sh",
		"PATH="+filepath.Dir(bin)+":"+os.Getenv("PATH"),
		"NERDR_SSH="+ssh, "FAKE_SSH_LOG="+filepath.Join(scratch, "ssh.log"),
		"FAKE_SSH_PASSWORD=sesame"))
	boxDir := filepath.Join(root, "repoB")
	logins := func() int { return strings.Count(readFile(filepath.Join(scratch, "ssh.log")), "\n") }
	machines := func(h *harness, want string) {
		t.Helper()
		h.keys(prefix + "k")
		h.waitFor("Machines", 5*time.Second)
		h.waitFor(want, 10*time.Second)
	}

	// Added in the TUI: it pauses for ssh's password.
	h := startArgs(t, bin, root, env, 160, 40)
	newCrashTab(h)
	h.keys(prefix + "k")
	h.waitFor("Machines", 5*time.Second)
	h.keys("a")
	h.waitFor("Add a server", 5*time.Second)
	h.keys("box\t\t" + boxDir + "\r")
	h.waitFor("box's password:", 10*time.Second)
	h.keys("sesame\r")
	h.waitFor("connected to box", 10*time.Second)
	if logins() != 1 {
		t.Fatalf("ssh log %q", readFile(filepath.Join(scratch, "ssh.log")))
	}
	machines(h, "· connected")
	h.keys("\x1b")
	endClient(t, h, "c")

	// Started again: ssh asks before the TUI is there.
	h = startArgs(t, bin, root, env, 160, 40)
	h.waitFor("box's password:", 10*time.Second)
	h.keys("sesame\r")
	h.waitTabBar("1:crash", 15*time.Second)
	if logins() != 2 {
		t.Fatalf("ssh log %q", readFile(filepath.Join(scratch, "ssh.log")))
	}
	machines(h, "· connected")
	h.keys("j")
	h.keys("d")
	h.waitFor("disconnected", 5*time.Second)
	h.keys("\x1b")
	endClient(t, h, "c")

	// Disconnected, it is not connected to at the next start, until r.
	h = startArgs(t, bin, root, env, 160, 40)
	h.waitTabBar("1:crash", 15*time.Second)
	machines(h, "· disconnected")
	if logins() != 2 {
		t.Fatalf("ssh log %q", readFile(filepath.Join(scratch, "ssh.log")))
	}
	h.keys("j")
	h.keys("r")
	h.waitFor("box's password:", 10*time.Second)
	h.keys("sesame\r")
	h.waitFor("connected to box", 10*time.Second)
	endClient(t, h, "q")
}
