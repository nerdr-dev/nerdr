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
	holders := func() []int { return pgrep("-f", "^"+bin+" holder -- ") }
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
// its agent the fake; start ends the holders bin starts after the test.
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

// TestRestoredAgentSize: started again just after quitting, nerdr's core
// restores the agent, which gets the size it is shown at, though core
// started it before it knew that.
func TestRestoredAgentSize(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	h := start(t, bin, root, env)
	newCrashTab(h)
	h.keys("size\r")
	h.waitFor("size is", 3*time.Second)
	_, size, _ := strings.Cut(h.screen(), "size is ")
	size, _, _ = strings.Cut(size, " │")
	size = strings.TrimSpace(size)
	endClient(t, h, "q")

	h = start(t, bin, root, env)
	h.waitTabBar("1:crash", 15*time.Second)
	h.waitFor("resumed conversation", 10*time.Second)
	h.keys("size\r")
	h.waitFor("size is "+size, 3*time.Second)
}

// TestASecondClientTakesOver: a nerdr started while another is connected
// to the core takes over, in a terminal of another size: the first ends,
// saying why, and the agent, the same process, gets the second's size.
func TestASecondClientTakesOver(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	fake, _ := filepath.Abs("fakeagent.sh")
	first := start(t, bin, root, env)
	newCrashTab(first)
	first.keys("size\r")
	first.waitFor("size is", 3*time.Second)
	agents := pgrep("-f", fake)
	if len(agents) != 1 {
		t.Fatalf("agents %v", agents)
	}
	_, was, _ := strings.Cut(first.screen(), "size is ")
	was, _, _ = strings.Cut(was, " ")

	second := startAt(t, bin, root, env, 120, 30)
	select {
	case <-first.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("the first client did not end; screen:\n%s", first.screen())
	}
	if !strings.Contains(first.screen(), "took over") {
		t.Errorf("the first client did not say why it ended:\n%s", first.screen())
	}
	second.waitTabBar("1:crash", 15*time.Second)
	second.waitFor("size is", 3*time.Second) // the screen as it was
	second.keys("size\r")
	var now string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		scr := second.screen()
		if strings.Count(scr, "size is ") < 2 {
			continue
		}
		now = scr[strings.LastIndex(scr, "size is ")+len("size is "):]
		now, _, _ = strings.Cut(now, " ")
		break
	}
	if now == was || now == "" {
		t.Errorf("the agent's rows are %q, with the first client %q; screen:\n%s", now, was, second.screen())
	}
	if got := pgrep("-f", fake); len(got) != 1 || got[0] != agents[0] {
		t.Errorf("agent %v, was %v", got, agents)
	}
	endClient(t, second, "q")
}

// TestAHolderThatCrashes: kill -9 on the holder ends what it ran, the
// agent and the core with it, and leaves nothing running unseen; nerdr
// started again resumes the agent's conversation from the session.
func TestAHolderThatCrashes(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	fake, _ := filepath.Abs("fakeagent.sh")
	h := start(t, bin, root, env)
	newCrashTab(h)
	h.keys("work\r")
	h.waitFor("done", 5*time.Second)
	hs := pgrep("-f", "^"+bin+" holder -- ")
	agents := pgrep("-f", fake)
	if len(hs) != 1 || len(agents) != 1 {
		t.Fatalf("holders %v, agents %v", hs, agents)
	}
	time.Sleep(500 * time.Millisecond) // the watch is told of the agent
	if err := syscall.Kill(hs[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if len(pgrep("-f", fake)) == 0 && len(pgrep("-f", "^"+bin+" core$")) == 0 && len(pgrep("-f", "^"+bin+" holder")) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("left after the holder crashed: agents %v, cores %v, holders and watches %v",
				pgrep("-f", fake), pgrep("-f", "^"+bin+" core$"), pgrep("-f", "^"+bin+" holder"))
		}
	}
	select {
	case <-h.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("the client did not end with its core; screen:\n%s", h.screen())
	}
	if !strings.Contains(h.screen(), "holder ended") {
		t.Errorf("the client did not say why it ended:\n%s", h.screen())
	}

	h = start(t, bin, root, env)
	h.waitTabBar("1:crash", 15*time.Second)
	h.waitFor("resumed conversation", 10*time.Second)
	endClient(t, h, "q")
}
