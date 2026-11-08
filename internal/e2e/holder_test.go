//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/spawn"
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

// TestAgentsTakeTheClientsEnv: an agent started over a core that another
// nerdr started runs with the environment of the nerdr connected, as it
// would without the core, not with the one the core was started from.
func TestAgentsTakeTheClientsEnv(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	first := start(t, bin, root, append(env, "FAKE_FROM=first"))
	first.waitFor("Repositories in", 15*time.Second)
	endClient(t, first, "c")
	if len(pgrep("-f", "^"+bin+" holder -- ")) != 1 {
		t.Fatal("no core runs on")
	}

	second := start(t, bin, root, append(env, "FAKE_FROM=second"))
	newCrashTab(second)
	second.keys("from\r")
	second.waitFor("from is", 3*time.Second)
	if scr := second.screen(); !strings.Contains(scr, "from is second") {
		t.Errorf("the agent did not run with the connected nerdr's environment:\n%s", scr)
	}
	endClient(t, second, "q")
}

// TestANerdrWithNoCoreInTheFolder: with a nerdr in the folder that runs
// its programs itself (NERDR_HOLDER=0, or one from before the core), a
// nerdr started there says to quit that one first, at once, rather than
// start a core that finds the session taken. The one there runs on, and
// quits as before the core.
func TestANerdrWithNoCoreInTheFolder(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	old := start(t, bin, root, append(env, "NERDR_HOLDER=0"))
	old.waitFor("Repositories in", 15*time.Second)

	started := time.Now()
	second := start(t, bin, root, env)
	select {
	case <-second.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("the second nerdr did not end; screen:\n%s", second.screen())
	}
	if scr := second.screen(); !strings.Contains(scr, "no core of it answers here") || !strings.Contains(scr, "quit it there first") {
		t.Errorf("not told to quit the other:\n%s", scr)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("took %v to say so", took)
	}
	if hs := pgrep("-f", "^"+bin+" holder -- "); len(hs) != 0 {
		t.Errorf("holders %v left running", hs)
	}

	old.keys(prefix + "q")
	old.waitFor("Quit nerdr?", 2*time.Second)
	old.keys("y")
	select {
	case <-old.p.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("did not quit; screen:\n%s", old.screen())
	}
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

// TestAnUpdateUnderARunningAgent: nerdr is updated, as Homebrew does it,
// while an agent runs: the link nerdr was started by is moved to the new
// version, and the old one's file removed. The agent keeps the NERDR_BIN
// and HERDR_BIN_PATH it was given, the link, and its hooks and nerdr's
// commands still reach the core: the old one, and the one the holder
// starts after it is killed, from the new file.
func TestAnUpdateUnderARunningAgent(t *testing.T) {
	built := build(t)
	root, scratch := setup(t)
	cellar := filepath.Join(scratch, "cellar")
	install := func(version string) string {
		t.Helper()
		p := filepath.Join(cellar, version, "nerdr")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(built)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := filepath.Join(scratch, "bin", "nerdr")
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(install("1"), link); err != nil {
		t.Fatal(err)
	}
	env := holderEnv(t, link, scratch)
	fake, _ := filepath.Abs("fakeagent.sh")
	h := start(t, link, root, env)
	newCrashTab(h)
	agent := pgrep("-f", fake)
	if len(agent) != 1 {
		t.Fatalf("agents %v", agent)
	}
	environ := readFile(fmt.Sprintf("/proc/%d/environ", agent[0]))
	for _, name := range []string{"NERDR_BIN", "HERDR_BIN_PATH"} {
		if !strings.Contains(environ, "\x00"+name+"="+link+"\x00") {
			t.Fatalf("%s is not the link nerdr was started by:\n%s", name, strings.ReplaceAll(environ, "\x00", "\n"))
		}
	}

	// The update: the link moved to the new version, the old one gone.
	moved := link + ".new"
	if err := os.Symlink(install("2"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, link); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cellar, "1")); err != nil {
		t.Fatal(err)
	}
	check := func(state string) {
		t.Helper()
		h.keys("hook-" + state + "\r")
		h.waitFor("reported "+state, 5*time.Second)
		h.keys("bin-status-" + state + "\r")
		h.waitFor("status "+state+" set", 5*time.Second)
		h.waitFor("set by NERDR_BIN "+state, 5*time.Second) // shown above the agent
	}
	check("blocked")

	// The core killed: the holder starts it again by the link, the new
	// nerdr, and the agent's hooks reach that one.
	holder := pgrep("-f", "^"+link+" holder -- ")
	if len(holder) != 1 {
		t.Fatalf("holders %v", holder)
	}
	core := pgrep("-P", strconv.Itoa(holder[0]), "-f", " core$")
	if len(core) != 1 {
		t.Fatalf("cores %v", core)
	}
	time.Sleep(2500 * time.Millisecond) // the screen goes to the holder
	if err := syscall.Kill(core[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	h.waitFor("connected to nerdr's core again", 15*time.Second)
	again := pgrep("-P", strconv.Itoa(holder[0]), "-f", " core$")
	if len(again) != 1 || again[0] == core[0] {
		t.Fatalf("core not started again: %v", again)
	}
	if exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", again[0])); exe != filepath.Join(cellar, "2", "nerdr") {
		t.Errorf("the core started again runs %s", exe)
	}
	check("idle")
	endClient(t, h, "q")
}

// TestAPluginRunOutlivesACoreCrash: an agent's run of a plugin goes on in
// the holder when the core is killed. The agent's nerdr plugin run waits
// for the core started again, which takes the run up and gives it what
// the run prints after. The run started in the folder given it as
// descriptor 3, and one started by the new core does too; a plugin
// changed since it was allowed is not run.
func TestAPluginRunOutlivesACoreCrash(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)
	dir := filepath.Join(scratch, "config", "nerdr", "plugins", "slow")
	_ = os.MkdirAll(dir, 0o755)
	pidFile, goFile := filepath.Join(scratch, "slow.pid"), filepath.Join(scratch, "go")
	_ = os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte("description = \"Takes a while.\"\nrun = [\"./run.sh\"]\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho $$ > "+pidFile+
		"\necho \"in-$(basename \"$(pwd)\")\"\necho before-crash\nwhile [ ! -e "+goFile+" ]; do sleep 0.1; done\necho after-crash\nexit 4\n"), 0o755)
	p, key, err := plugin.Fresh(plugin.Load(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Allow(plugin.AllowedFile(filepath.Join(scratch, "state", "nerdr", "sessions")), p, key); err != nil {
		t.Fatal(err)
	}

	h := start(t, bin, root, env)
	newCrashTab(h)
	h.keys("plugin-run-slow\r")
	h.waitFor("in-crash", 5*time.Second) // the worktree, by descriptor 3
	h.waitFor("before-crash", 5*time.Second)
	b, _ := os.ReadFile(pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal("the plugin's program did not start:", err)
	}

	holder := pgrep("-f", "^"+bin+" holder -- ")
	if len(holder) != 1 {
		t.Fatalf("holders %v", holder)
	}
	core := pgrep("-P", strconv.Itoa(holder[0]), "-f", " core$")
	if len(core) != 1 {
		t.Fatalf("cores %v", core)
	}
	if err := syscall.Kill(core[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	h.waitFor("connected to nerdr's core again", 15*time.Second)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the run ended with the core")
	}
	_ = os.WriteFile(goFile, nil, 0o644)
	h.waitFor("after-crash", 10*time.Second)
	h.waitFor("plugin-exit-4", 5*time.Second)

	// The core started again runs it as the one before did.
	_ = os.Remove(pidFile)
	h.keys("plugin-run-slow\r")
	h.waitFor("plugin-exit-4", 5*time.Second)
	if strings.Count(h.screen(), "in-crash") != 2 {
		t.Errorf("the second run was not in the worktree:\n%s", h.screen())
	}
	// Changed since it was allowed: not run.
	_ = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho changed-run\n"), 0o755)
	h.keys("plugin-run-slow\r")
	h.waitFor("not allowed it as it is now", 5*time.Second)
	if strings.Contains(h.screen(), "changed-run") {
		t.Fatal("a plugin changed since it was allowed ran")
	}
	endClient(t, h, "q")
}

// TestANerdrAnAgentStarts: an agent in a tab that starts nerdr in another
// folder, in a terminal of its own, gets a nerdr there that runs its
// programs itself, as with NERDR_HOLDER=0: no holder that would keep them
// unseen once the agent is gone. endAll fails the test for what is left
// in the folder.
func TestANerdrAnAgentStarts(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	elsewhere := filepath.Join(scratch, "elsewhere")
	env := append(holderEnv(t, bin, scratch), "FAKE_ELSEWHERE="+elsewhere)

	h := start(t, bin, root, env)
	newCrashTab(h)
	if hs := pgrep("-f", "^"+bin+" holder -- "); len(hs) != 1 {
		t.Fatalf("holders %v", hs)
	}
	h.keys("nerdr-elsewhere\r")
	// 124: it ran until the agent's timeout ended it, not stopped at once.
	h.waitFor("elsewhere-exit-124", 15*time.Second)
	if hs := pgrep("-f", "^"+bin+" holder -- "); len(hs) != 1 {
		t.Errorf("holders %v: the agent's nerdr started one", hs)
	}
	if b, err := os.ReadFile(filepath.Join(scratch, "agent.log")); err != nil || strings.Count(string(b), "\n") != 1 {
		t.Errorf("agents started:\n%s", b)
	}
	endClient(t, h, "q")
}

// TestAnAgentConnectsToTheNerdrOfAnotherFolder: the nerdr of another
// folder, which knows nothing of the agent, refuses it as a client all
// the same, for running under a nerdr, whether that nerdr runs its
// programs in a holder or itself (NERDR_HOLDER=0). The test's own client
// is taken, though an agent runs the test, and the other nerdr then
// counts as nested: it was started from where that client runs.
func TestAnAgentConnectsToTheNerdrOfAnotherFolder(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("NERDR_HOLDER="+mode, func(t *testing.T) {
			bin := build(t)
			root, scratch := setup(t)
			elsewhere := filepath.Join(scratch, "elsewhere")
			_ = os.MkdirAll(elsewhere, 0o755)
			env := append(holderEnv(t, bin, scratch), "FAKE_ELSEWHERE="+elsewhere)

			other := start(t, bin, elsewhere, env)
			other.waitFor("Repositories in", 15*time.Second)
			h := start(t, bin, root, append(env, "NERDR_HOLDER="+mode))
			newCrashTab(h)
			if mode == "0" {
				if ps := pgrep("-f", "^"+bin+" "+spawn.LocalArg+"$"); len(ps) != 1 {
					t.Errorf("nerdrs that say they run their programs: %v", ps)
				}
			}
			h.keys("connect-elsewhere\r")
			h.waitFor("connect ", 15*time.Second)
			if !strings.Contains(h.screen(), "connect refused") {
				t.Errorf("the agent was taken as a client of the other folder's nerdr:\n%s\n%s", h.screen(), readFile(elsewhere+".out"))
			}
			if mode == "1" {
				endClient(t, h, "q")
			} else {
				h.keys(prefix + "q")
				h.waitFor("Quit nerdr?", 2*time.Second)
				h.keys("y")
				select {
				case <-h.p.Done():
				case <-time.After(15 * time.Second):
					t.Fatalf("did not quit; screen:\n%s", h.screen())
				}
			}
			endClient(t, other, "q")
		})
	}
}
