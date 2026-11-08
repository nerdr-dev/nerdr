//go:build e2e

// End-to-end test: runs the nerdr binary in a pseudo-terminal against
// throwaway repositories and a fake agent, and drives it with keys.
//
//	go test -tags e2e ./internal/e2e/ -v
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"
)

type harness struct {
	t    *testing.T
	p    *term.Pane
	root string
	env  []string
}

func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nerdr")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/nerdr")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setup makes root/{repoA,repoB}, each with a bare origin holding main.
func setup(t *testing.T) (root, scratch string) {
	scratch = t.TempDir()
	root = filepath.Join(scratch, "code")
	for _, name := range []string{"repoA", "repoB"} {
		origin := filepath.Join(scratch, "origins", name+".git")
		_ = os.MkdirAll(origin, 0o755)
		git(t, origin, "init", "--bare", "-q", "-b", "main")
		repo := filepath.Join(root, name)
		_ = os.MkdirAll(repo, 0o755)
		git(t, repo, "init", "-q", "-b", "main")
		_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# "+name+"\n"), 0o644)
		git(t, repo, "add", ".")
		git(t, repo, "commit", "-q", "-m", "init")
		git(t, repo, "remote", "add", "origin", origin)
		git(t, repo, "push", "-q", "origin", "main")
		git(t, repo, "fetch", "-q", "origin")
	}
	return root, scratch
}

func start(t *testing.T, bin, root string, env []string) *harness {
	t.Helper()
	return startAt(t, bin, root, env, 160, 40)
}

// startAt is start in a terminal w×h.
func startAt(t *testing.T, bin, root string, env []string, w, h int) *harness {
	t.Helper()
	// nerdr checks that the review panel it draws again from its last
	// drawing has not changed meanwhile; see reuseCheck in app.
	env = append(env[:len(env):len(env)], "NERDR_CHECK_REUSE=1")
	env = isolated(t, env)
	p, err := term.Start([]string{bin}, root, env, w, h, func() {})
	if err != nil {
		t.Fatal(err)
	}
	hs := &harness{t: t, p: p, root: root, env: env}
	if !ending[t] {
		ending[t] = true
		t.Cleanup(func() { delete(ending, t); endAll(t, bin, root) })
	}
	t.Cleanup(func() { p.Close() })
	return hs
}

// ending are the tests that end what they started once they are done;
// see endAll.
var ending = map[*testing.T]bool{}

// isolated is env as a nerdr under test is to have it: its state, and
// the sockets of its core and holder, in the test's own temporary
// folders, never the user's, where it could reach the user's own nerdr.
// The client sockets are in a folder of the test's when env names none.
func isolated(t *testing.T, env []string) []string {
	t.Helper()
	get := func(name string) (string, bool) {
		v, ok := "", false
		for _, kv := range env {
			if k, val, _ := strings.Cut(kv, "="); k == name {
				v, ok = val, true // the last wins, as exec has it
			}
		}
		return v, ok
	}
	if _, ok := get("NERDR_CLIENT_DIR"); !ok {
		env = append(env, "NERDR_CLIENT_DIR="+filepath.Join(t.TempDir(), "run"))
	}
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	for _, name := range []string{"XDG_STATE_HOME", "NERDR_CLIENT_DIR"} {
		v, ok := get(name)
		real, _ := filepath.EvalSymlinks(filepath.Dir(v))
		if !ok || !strings.HasPrefix(v, os.TempDir()) && !strings.HasPrefix(real, tmp) {
			t.Fatalf("%s is %q: a nerdr under test must have it in a temporary folder of its own", name, v)
		}
	}
	return env
}

// endAll ends what nerdr at bin, started in root, left running once the
// test is done: its holders are told to stop their programs and end, and
// what has not ended after a while is killed and the test failed. With
// /proc, a process in root's folder, an agent or a terminal, counts too.
func endAll(t *testing.T, bin, root string) {
	mine := "^" + regexp.QuoteMeta(bin) + "( |$)"
	for _, pid := range pgrep("-f", "^"+regexp.QuoteMeta(bin)+" holder") {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	left := func() []int { return append(pgrep("-f", mine), inFolder(filepath.Dir(root))...) }
	deadline := time.Now().Add(15 * time.Second)
	for len(left()) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if pids := left(); len(pids) > 0 {
		var what []string
		for _, pid := range pids {
			cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			what = append(what, fmt.Sprintf("%d %s", pid, strings.ReplaceAll(string(cmd), "\x00", " ")))
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		t.Errorf("left running after the test:\n%s", strings.Join(what, "\n"))
	}
}

// inFolder are the processes, other than this one, whose working folder
// is in dir; none where there is no /proc.
func inFolder(dir string) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err == nil && (cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))) {
			out = append(out, pid)
		}
	}
	return out
}

func (h *harness) screen() string { return h.p.Snapshot().Screen }

func (h *harness) waitFor(what string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(h.screen(), what) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %q; screen:\n%s", what, h.screen())
}

func (h *harness) waitGone(what string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(h.screen(), what) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %q to go; screen:\n%s", what, h.screen())
}

func (h *harness) waitTabBar(what string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(h.bars(), what) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %q in the tab bars:\n%s", what, h.bars())
}

// bars is the rows at the top: the groups, the rule under them, and the
// active group's tabs.
func (h *harness) bars() string {
	lines := strings.SplitN(h.screen(), "\n", 4)
	return strings.Join(lines[:min(3, len(lines))], "\n")
}

// gotoTab moves between groups until the tab named name is the active one.
func (h *harness) gotoTab(name string) {
	h.t.Helper()
	for i := 0; i < 5; i++ {
		if strings.Contains(h.bars(), ":"+name+" claude") {
			return
		}
		h.keys(prefix + "]\r")
	}
	h.t.Fatalf("could not get to tab %s:\n%s", name, h.bars())
}

func (h *harness) keys(s string) {
	h.p.Write([]byte(s))
	time.Sleep(150 * time.Millisecond)
}

const prefix = "\x11" // ctrl+q

func TestEndToEnd(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	agentLog := filepath.Join(scratch, "agent.log")
	herdrLog := filepath.Join(scratch, "herdr.log")
	fake, _ := filepath.Abs("fakeagent.sh")
	fakeHerdr, _ := filepath.Abs("fakeherdr.sh")

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
		"FAKE_LOG="+agentLog,
		"FAKE_HERDR_LOG="+herdrLog,
		"HERDR_ENV=1", "HERDR_PANE_ID=w9:p9", "HERDR_BIN_PATH="+fakeHerdr,
		"TERM=xterm-256color",
	)

	h := start(t, bin, root, env)

	// 4: the folder is scanned and repositories are offered.
	h.waitFor("Repositories in", 10*time.Second)
	h.waitFor("repoA", time.Second)
	h.waitFor("repoB", time.Second)
	h.keys(" ")      // pick repoA
	h.keys("\x1b[B") // down
	h.keys(" ")      // pick repoB
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-x\r")
	// 5: the agent is picked from what is installed.
	h.waitFor("Agent:", 2*time.Second)
	h.waitFor("repoA: new branch from origin/main", time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)
	for _, r := range []string{"repoA", "repoB"} {
		if _, err := os.Stat(filepath.Join(root, "worktrees", "feat-x-repoA-and-1-more-fca4db", r, "README.md")); err != nil {
			t.Fatalf("worktree for %s not made: %v", r, err)
		}
	}
	h.waitTabBar("1:feat-x", time.Second)

	// Typing goes to the agent.
	h.keys("work\r")
	h.waitFor("Working…", 3*time.Second)
	// 6: the traffic light turns to working...
	h.waitTabBar("●", 3*time.Second)
	// ...and to idle once it is done, the user watching it.
	h.waitFor("done", 5*time.Second)
	h.waitTabBar("○", 5*time.Second)

	// 12: the review panel lists the file the agent wrote.
	h.waitFor("new.txt", 5*time.Second)

	// A question from the agent shows as blocked.
	h.keys("ask\r")
	h.waitFor("Do you want to proceed?", 3*time.Second)
	time.Sleep(700 * time.Millisecond)
	if !strings.Contains(readFile(herdrLog), "--state blocked") {
		t.Fatalf("herdr was not told the tab is blocked:\n%s", readFile(herdrLog))
	}
	// herdr's sidebar gets the tabs summed up.
	if !strings.Contains(readFile(herdrLog), "--token summary=1 tab · 1 needs input") {
		t.Fatalf("herdr was not given the summary:\n%s", readFile(herdrLog))
	}
	h.keys("1\r")
	h.waitFor("answered 1", 3*time.Second)

	// 7: a second tab, through the prefix.
	h.keys(prefix + "c")
	h.waitFor("Repositories in", 2*time.Second)
	h.keys("\r") // repoA under the cursor, picked by enter
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-y\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitTabBar("1:feat-y claude", 10*time.Second)
	// Grouped by repository: feat-x spans two, so it is under multi.
	h.waitTabBar("repoA (1)", time.Second)
	h.waitTabBar("multi (1)", time.Second)
	if _, err := os.Stat(filepath.Join(root, "worktrees", "feat-y-repoA", "README.md")); err != nil {
		t.Fatalf("single-repo worktree not made: %v", err)
	}

	// Switching between groups.
	h.keys(prefix + "]\r")
	h.waitTabBar("1:feat-x claude", 2*time.Second)
	h.waitFor("answered 1", 2*time.Second)
	h.keys(prefix + "]\r")
	h.waitTabBar("1:feat-y claude", 2*time.Second)
	h.waitGone("answered 1", 2*time.Second)

	// 10: tab 2 asks a question while tab 1 is shown; herdr focusing
	// nerdr's pane (a click on it in herdr's agent list) opens tab 2.
	h.waitFor("? for shortcuts", 5*time.Second)
	h.keys("ask\r")
	h.waitFor("Do you want to proceed?", 3*time.Second)
	h.keys(prefix + "]\r")
	h.waitTabBar("1:feat-x claude", 2*time.Second)
	time.Sleep(500 * time.Millisecond)
	h.keys("\x1b[O")
	h.keys("\x1b[I")
	h.waitTabBar("1:feat-y claude", 3*time.Second)
	h.keys("2\r")
	h.waitFor("answered 2", 3*time.Second)

	// The review panel takes the keyboard and gives it back.
	h.keys(prefix + "\t")
	h.waitTabBar("review", 2*time.Second)
	h.keys("\x1b")
	h.waitGone("review · esc", 2*time.Second)

	// 9: quit and start again; both tabs come back, resuming.
	quit(t, h)
	if !strings.Contains(readFile(herdrLog), "release-agent") {
		t.Fatalf("herdr was not told nerdr left:\n%s", readFile(herdrLog))
	}

	h = start(t, bin, root, env)
	h.waitTabBar("multi (1)", 10*time.Second)
	h.waitTabBar("repoA (1)", time.Second)
	h.waitFor("resumed conversation", 10*time.Second)
	// The agents are started a moment apart, the tab shown first.
	for deadline := time.Now().Add(5 * time.Second); strings.Count(readFile(agentLog), "--resume") < 2 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	if n := strings.Count(readFile(agentLog), "--resume"); n != 2 {
		t.Fatalf("expected two resumed agents, log:\n%s", readFile(agentLog))
	}

	// 8: closing a tab asks about the worktree; it has a new file in it.
	h.gotoTab("feat-x")
	h.keys(prefix + "x")
	h.waitFor("Close tab", 2*time.Second)
	h.waitFor("uncommitted changes in: repoA", time.Second)
	h.keys("y")
	h.waitFor("will be lost", 2*time.Second)
	h.keys("Y")
	h.waitGone("feat-x", 5*time.Second)
	h.waitGone("multi", time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "worktrees", "feat-x-repoA-and-1-more-fca4db")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("feat-x worktrees not removed; screen:\n%s", h.screen())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Closing and keeping the worktree.
	h.keys(prefix + "x")
	// What the dialog says, not its title: "Close tab" is also among the
	// prefix's hints, there before the dialog takes keys.
	h.waitFor("closes feat-y", 2*time.Second)
	h.keys("k")
	h.waitFor("Repositories in", 5*time.Second) // no tabs left: the wizard
	if _, err := os.Stat(filepath.Join(root, "worktrees", "feat-y-repoA")); err != nil {
		t.Fatalf("feat-y should have been kept: %v", err)
	}
	// It is offered again as an existing worktree.
	h.waitFor("Or open an existing worktree", time.Second)
	h.keys("\x1b")
	quit(t, h)
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// TestRealClaude starts the real claude in a tab, without sending it a
// prompt, and prints the screen: a check of the terminal emulation against
// the agent that matters most. NERDR_E2E_CLAUDE=1 to run.
func TestRealClaude(t *testing.T) {
	if os.Getenv("NERDR_E2E_CLAUDE") == "" {
		t.Skip("set NERDR_E2E_CLAUDE=1")
	}
	bin := build(t)
	root, scratch := setup(t)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "XDG_STATE_HOME=") {
			env = append(env, kv)
		}
	}
	env = append(env, "XDG_STATE_HOME="+filepath.Join(scratch, "state"), "TERM=xterm-256color")
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" \r")
	h.keys("real-claude\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	time.Sleep(6 * time.Second)
	t.Logf("screen:\n%s", h.screen())
	first, _, _ := strings.Cut(h.screen(), "\n")
	t.Logf("tab bar: %q", first)
	quit(t, h)
}

// quit quits nerdr in h, and its agents with it: over its core, nerdr asks
// whether to close or quit, and q quits; with NERDR_HOLDER=0 it asks
// whether to quit, and y does.
func quit(t *testing.T, h *harness) {
	t.Helper()
	h.keys(prefix + "q")
	h.waitFor("uit nerdr?", 2*time.Second)
	if strings.Contains(h.screen(), "Close or quit nerdr?") {
		h.keys("q")
	} else {
		h.keys("y")
	}
	select {
	case <-h.p.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("did not quit; screen:\n%s", h.screen())
	}
}
