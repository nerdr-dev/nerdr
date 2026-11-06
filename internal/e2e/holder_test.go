//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pgrep is the processes pgrep finds with args, by pid.
func pgrep(args ...string) []int {
	out, _ := exec.Command("pgrep", args...).Output()
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// TestCoreCrashOverHolder: with NERDR_HOLDER=1, killing nerdr's core
// leaves the agent running in the holder; the core started again takes it
// up, with its screen, and quitting stops it and the holder.
func TestCoreCrashOverHolder(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"TERM=xterm-256color",
		"NERDR_HOLDER=1",
	)
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" ")
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("crash\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)
	h.keys("work\r")
	h.waitFor("done", 5*time.Second)

	holderPid := h.p.Pid()
	cores := pgrep("-P", strconv.Itoa(holderPid), "-x", "nerdr")
	agents := pgrep("-f", fake)
	if len(cores) != 1 || len(agents) != 1 {
		t.Fatalf("cores %v, agents %v under holder %d", cores, agents, holderPid)
	}
	// Let the screen go quiet and be sent to the holder.
	time.Sleep(2500 * time.Millisecond)
	if err := syscall.Kill(cores[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	var again []int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		again = pgrep("-P", strconv.Itoa(holderPid), "-x", "nerdr")
		if len(again) == 1 && again[0] != cores[0] {
			break
		}
	}
	if len(again) != 1 || again[0] == cores[0] {
		t.Fatalf("core not started again: %v", again)
	}
	h.waitTabBar("1:crash", 10*time.Second)
	h.waitFor("done", 5*time.Second)
	if now := pgrep("-f", fake); len(now) != 1 || now[0] != agents[0] {
		t.Fatalf("agent %v, was %v", now, agents)
	}
	if strings.Count(readFile(filepath.Join(scratch, "agent.log")), "\n") != 1 {
		t.Fatalf("agent started again:\n%s", readFile(filepath.Join(scratch, "agent.log")))
	}
	// It goes on as it was: typing reaches it through the new core, which
	// draws what it shows; nothing of it was on the screen before.
	h.keys("ask\r")
	h.waitFor("Do you want to proceed?", 3*time.Second)
	h.keys("1\r")
	h.waitFor("answered 1", 3*time.Second)

	h.keys(prefix + "q")
	h.waitFor("Quit nerdr?", 2*time.Second)
	h.keys("y")
	select {
	case <-h.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("did not quit; screen:\n%s", h.screen())
	}
	if err := syscall.Kill(agents[0], 0); err != syscall.ESRCH {
		t.Fatalf("agent %d still running after quitting: %v", agents[0], err)
	}
}
