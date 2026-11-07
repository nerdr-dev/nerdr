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

// TestCoreOfItsOwn is phase 4 of the core: with NERDR_HOLDER=1 the agents
// run in a core of their own, which nerdr starts and is a client of.
// Closing the client leaves the agent at work, its hooks still reaching
// core; nerdr started again shows it as it is, the same process. Killing
// the core leaves the agent running in the holder; the client connects to
// the core started again, which takes it up. Quitting stops it all.
func TestCoreOfItsOwn(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	fake, _ := filepath.Abs("fakeagent.sh")
	runDir := filepath.Join(scratch, "run")
	holders := func() []int { return pgrep("-f", "^"+bin+" holder") }
	coreOf := func(holder int) []int { return pgrep("-P", strconv.Itoa(holder), "-f", " core$") }
	closeClient := func(h *harness) { t.Helper(); endClient(t, h, "c") }

	h := start(t, bin, root, env)
	newCrashTab(h)
	h.keys("work\r")
	h.waitFor("done", 5*time.Second)

	hs := holders()
	agents := pgrep("-f", fake)
	if len(hs) != 1 || len(agents) != 1 || len(coreOf(hs[0])) != 1 {
		t.Fatalf("holders %v, agents %v", hs, agents)
	}
	holderPid, corePid := hs[0], coreOf(hs[0])[0]

	// Closed while the agent works and reports by its hooks: it goes on.
	h.keys("hooked\r")
	h.waitFor("thinking quietly", 3*time.Second)
	closeClient(h)
	if err := syscall.Kill(agents[0], 0); err != nil {
		t.Fatalf("agent gone with the client: %v", err)
	}
	time.Sleep(5 * time.Second) // the agent's work ends meanwhile

	h = start(t, bin, root, env)
	h.waitTabBar("1:crash", 10*time.Second)
	h.waitFor("hooked over", 5*time.Second)
	if now := pgrep("-f", fake); len(now) != 1 || now[0] != agents[0] {
		t.Fatalf("agent %v, was %v", now, agents)
	}
	if now := coreOf(holderPid); len(now) != 1 || now[0] != corePid {
		t.Fatalf("core %v, was %d: connecting started another", now, corePid)
	}
	if strings.Count(readFile(filepath.Join(scratch, "agent.log")), "\n") != 1 {
		t.Fatalf("agent started again:\n%s", readFile(filepath.Join(scratch, "agent.log")))
	}

	// The core killed: the agent lives on in the holder, and the client
	// goes on with the core started again.
	time.Sleep(2500 * time.Millisecond) // the screen goes to the holder
	if err := syscall.Kill(corePid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	h.waitFor("connected to nerdr's core again", 15*time.Second)
	if now := coreOf(holderPid); len(now) != 1 || now[0] == corePid {
		t.Fatalf("core not started again: %v", now)
	}
	if now := pgrep("-f", fake); len(now) != 1 || now[0] != agents[0] {
		t.Fatalf("agent %v, was %v", now, agents)
	}
	h.keys("ask\r")
	h.waitFor("Do you want to proceed?", 5*time.Second)
	h.keys("1\r")
	h.waitFor("answered 1", 3*time.Second)

	h.keys(prefix + "q")
	h.waitFor("Close or quit nerdr?", 2*time.Second)
	h.keys("q")
	select {
	case <-h.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("did not quit; screen:\n%s", h.screen())
	}
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if syscall.Kill(agents[0], 0) == syscall.ESRCH && syscall.Kill(holderPid, 0) == syscall.ESRCH {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent or holder still running after quitting:\n%s", readFile(filepath.Join(scratch, "state", "nerdr", filepath.Base(runDir))))
		}
	}
}

// holderEnv is the environment of nerdr with NERDR_HOLDER=1 in scratch,
// its agent the fake; the holders bin starts are ended after the test.
func holderEnv(t *testing.T, bin, scratch string) []string {
	t.Helper()
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "NERDR") {
			env = append(env, kv)
		}
	}
	t.Cleanup(func() {
		for _, pid := range pgrep("-f", "^"+bin+" holder") {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	return append(env,
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"NERDR_CLIENT_DIR="+filepath.Join(scratch, "run"),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"TERM=xterm-256color",
		"NERDR_HOLDER=1",
	)
}

// newCrashTab makes the first tab, "crash", in the first repository.
func newCrashTab(h *harness) {
	h.waitFor("Repositories in", 15*time.Second)
	h.keys(" ")
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("crash\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)
}

// endClient closes the client (key "c") or quits (key "q").
func endClient(t *testing.T, h *harness, key string) {
	t.Helper()
	h.keys(prefix + "q")
	h.waitFor("Close or quit nerdr?", 2*time.Second)
	h.keys(key)
	select {
	case <-h.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("did not end; screen:\n%s", h.screen())
	}
}
