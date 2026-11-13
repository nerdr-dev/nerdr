package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/statedir"
)

// The check from inside a sandbox runs this test binary as nerdr.
func init() {
	if len(os.Args) > 1 && os.Args[1] == sandboxcheck.Arg {
		os.Exit(sandboxcheck.Main(os.Args[2:]))
	}
}

// toldClient keeps the notices it is told.
type toldClient struct {
	NoClient
	notices []string
}

func (c *toldClient) Tell(ev Event) Cmd {
	if n, ok := ev.(NoticeMsg); ok {
		c.notices = append(c.notices, string(n))
	}
	return nil
}

// checkedCore is a core over a holder in this process, with a client
// socket of its own, and one tab whose codex is sandboxed, run by the
// wrapper script body: "$@" is codex, or the check in its place.
func checkedCore(t *testing.T, body string) (*Core, *Tab, *toldClient) {
	t.Helper()
	// Short, for the socket's path: t.TempDir's can be too long for one.
	run, err := os.MkdirTemp("", "nck")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	t.Setenv(attach.EnvDir, filepath.Join(run, "run"))
	wrap := agentScript(t, body)
	c, tb := heldCoreUnasked(t, inProcessHolder(t), wrap)
	c.Root = filepath.Dir(wrap)
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{wrap, "codex"}, Sandboxed: true}
	askedHelp(c.Cfg.Agents)
	sock, key, err := attach.Paths(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := attach.Listen(sock, key, attach.Config{Check: func(hooks.Peer) string { return "a test" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close("") })
	cl := &toldClient{}
	c.Client = cl
	return c, tb, cl
}

// check starts tb's agent, which runs the check first, as the loop
// would, and is the notices it led to; started is whether the agent was.
func check(t *testing.T, c *Core, tb *Tab, cl *toldClient) (notes []string, started bool) {
	t.Helper()
	cmd := c.Start(tb, false)
	if tb.Pane != nil {
		t.Fatal("started before the check")
	}
	if cmd == nil {
		t.Fatalf("no check: %s", tb.Err)
	}
	ev, ok := cmd().(SpawnReady)
	if !ok || ev.Prep.Check == nil {
		t.Fatalf("got %T %+v", ev, ev)
	}
	c.OnSpawnReady(ev)
	started = tb.Pane != nil
	if started != !ev.Prep.Check.Refused() {
		t.Errorf("started %v, the check refused %v: %s", started, ev.Prep.Check.Refused(), tb.Err)
	}
	// Every launch is checked: policy can vary with folder or environment.
	if again := c.CheckSandbox(tb, nil) != nil; !again {
		t.Errorf("started %v, checked again %v", started, again)
	}
	return cl.notices, started
}

// runsCodex has the wrapper run codex as a program that waits; asked for
// its flags, it has none.
const runsCodex = "if [ \"$1\" = codex ]; then case \"$*\" in *--help*) exit 0;; esac; exec sleep 30; fi\n"

// A wrapper that is no sandbox lets the check reach the client socket:
// the agent is not started, and the user is told why.
func TestAWrapperThatLetsTheAgentReachTheClientSocketIsRefused(t *testing.T) {
	c, tb, cl := checkedCore(t, runsCodex+"exec \"$@\"\n")
	notes, started := check(t, c, tb, cl)
	if started || !strings.Contains(tb.Err, "not started") {
		t.Errorf("started %v: %q", started, tb.Err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "nerdr did not start codex: its sandbox lets it reach nerdr's client socket") ||
		!strings.Contains(notes[0], "folder, socket, key") {
		t.Errorf("notices %q", notes)
	}
}

// A sandbox that denies the folder, and has a pid namespace of its own,
// reaches nothing: nothing is said.
func TestASandboxThatDeniesTheClientSocketIsNotWarnedOf(t *testing.T) {
	if err := exec.Command("bwrap", "--dev-bind", "/", "/", "--unshare-pid", "true").Run(); err != nil {
		t.Skip("no bwrap that runs here: ", err)
	}
	c, tb, cl := checkedCore(t, runsCodex+"exec bwrap --dev-bind / / --tmpfs \"$NERDR_CLIENT_DIR\" --unshare-pid -- \"$@\"\n")
	if notes, started := check(t, c, tb, cl); len(notes) != 0 || !started {
		t.Errorf("started %v, notices %q", started, notes)
	}
}

// A sandbox in which nerdr does not run, or whose command names no
// agent to put the check in place of, is said to be unchecked; the agent
// is refused.
func TestASandboxNerdrCannotCheckIsSaidSo(t *testing.T) {
	c, tb, cl := checkedCore(t, runsCodex+"echo no nerdr in here\n")
	notes, started := check(t, c, tb, cl)
	if started {
		t.Fatal("an unchecked sandboxed agent started")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "nerdr did not start codex: it could not check") ||
		!strings.Contains(notes[0], "the check did not answer") {
		t.Errorf("notices %q", notes)
	}

	c, tb, cl = checkedCore(t, runsCodex)
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{filepath.Join(c.Root, "agent.sh")}, Sandboxed: true}
	askedHelp(c.Cfg.Agents)
	tb.Agent = agents.Codex
	notes, started = check(t, c, tb, cl)
	if started {
		t.Fatal("an opaque sandbox wrapper started without verification")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "names no codex") {
		t.Errorf("notices %q", notes)
	}
}

// pickingWrapper is a wrapper that starts codex by default, with no word
// for it in the command, and runs another program only as
// `wrapper exec -- program`, in sandbox, a command to put before it.
func pickingWrapper(sandbox string) string {
	return "case \"$1\" in\n" +
		"exec) shift 2; exec " + sandbox + " \"$@\";;\n" +
		"*) case \"$*\" in *--help*) exit 0;; esac; exec sleep 30;;\n" +
		"esac\n"
}

// A wrapper that picks the agent itself is checked through the agent's
// check_command: the check runs, and what it finds counts as it does
// through command.
func TestACheckCommandRunsTheCheck(t *testing.T) {
	c, tb, cl := checkedCore(t, pickingWrapper(""))
	wrap := filepath.Join(c.Root, "agent.sh")
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{wrap}, Sandboxed: true}
	askedHelp(c.Cfg.Agents)
	notes, started := check(t, c, tb, cl)
	if started || len(notes) != 1 || !strings.Contains(notes[0], "names no codex") || !strings.Contains(notes[0], "check_command") {
		t.Fatalf("without check_command: started %v, notices %q", started, notes)
	}

	cl.notices = nil
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{wrap}, Sandboxed: true, CheckCommand: []string{wrap, "exec", "--"}}
	askedHelp(c.Cfg.Agents)
	notes, started = check(t, c, tb, cl)
	if started || len(notes) != 1 || !strings.Contains(notes[0], "client socket") {
		t.Fatalf("a check_command that is no sandbox: started %v, notices %q", started, notes)
	}

	if err := exec.Command("bwrap", "--dev-bind", "/", "/", "--unshare-pid", "true").Run(); err != nil {
		t.Skip("no bwrap that runs here: ", err)
	}
	c, tb, cl = checkedCore(t, pickingWrapper("bwrap --dev-bind / / --tmpfs \"$NERDR_CLIENT_DIR\" --unshare-pid --"))
	wrap = filepath.Join(c.Root, "agent.sh")
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{wrap}, Sandboxed: true, CheckCommand: []string{wrap, "exec", "--"}}
	askedHelp(c.Cfg.Agents)
	if notes, started := check(t, c, tb, cl); len(notes) != 0 || !started {
		t.Errorf("a check_command that is a sandbox: started %v, notices %q", started, notes)
	}
}

// A check that never ends is ended, and said not to have answered.
func TestACheckThatHangsIsEnded(t *testing.T) {
	old := SandboxCheckWait
	SandboxCheckWait = 300 * time.Millisecond
	t.Cleanup(func() { SandboxCheckWait = old })
	c, tb, cl := checkedCore(t, runsCodex+"exec sleep 30\n")
	start := time.Now()
	notes, started := check(t, c, tb, cl)
	if started {
		t.Fatal("an unchecked sandboxed agent started")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "did not answer") {
		t.Errorf("notices %q", notes)
	}
}

// Only sandboxed agents are checked, with or without a holder.
func TestOnlyASandboxedAgentIsChecked(t *testing.T) {
	c, tb, _ := checkedCore(t, runsCodex+"exec \"$@\"\n")
	sandboxed := c.Cfg.Agents[agents.Codex]
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: sandboxed.Command}
	askedHelp(c.Cfg.Agents)
	if cmd := c.Start(tb, false); cmd != nil || tb.Pane == nil {
		t.Errorf("an agent outside a sandbox checked, or not started: %s", tb.Err)
	}
	c.Cfg.Agents[agents.Codex] = sandboxed
	c.Held = nil
	if c.CheckSandbox(tb, nil) == nil {
		t.Error("not checked with no holder")
	}
}

// With ptrace_scope 0, only the sandbox keeps the agent from tracing the
// holder: said when the check could not tell, or found it could, and not
// when the check found the sandbox does.
func TestPtraceScopeZeroIsWarnedOfUnlessTheCheckFoundItClosed(t *testing.T) {
	old := ptraceOpen
	t.Cleanup(func() { ptraceOpen = old })
	ptraceOpen = func() bool { return true }
	t.Setenv(attach.EnvDir, t.TempDir())
	for _, c := range []struct {
		ev   SandboxCheckedMsg
		want string // "" for no notice
	}{
		{SandboxCheckedMsg{Kind: agents.Codex, Why: "the check did not answer"}, "ptrace_scope is 0"},
		{SandboxCheckedMsg{Kind: agents.Codex, OK: true, Result: sandboxcheck.Result{Holder: []string{"ptrace 12"}}}, "ptrace_scope is 0"},
		{SandboxCheckedMsg{Kind: agents.Codex, OK: true}, ""},
	} {
		cl := &toldClient{}
		m := &Core{Client: cl, Root: t.TempDir()}
		m.OnSandboxChecked(c.ev)
		switch {
		case c.want == "" && len(cl.notices) != 0:
			t.Errorf("%+v: notices %q", c.ev, cl.notices)
		case c.want != "" && (len(cl.notices) != 1 || !strings.Contains(cl.notices[0], c.want)):
			t.Errorf("%+v: notices %q, want %q", c.ev, cl.notices, c.want)
		}
	}
}

// A sandbox exposing critical writable paths refuses the agent.
func TestASandboxThatLetsTheAgentWriteIsRefused(t *testing.T) {
	if err := exec.Command("bwrap", "--dev-bind", "/", "/", "--unshare-pid", "true").Run(); err != nil {
		t.Skip("no bwrap that runs here: ", err)
	}
	open, denied := t.TempDir(), t.TempDir()
	SandboxWrites = func(*Core, *Tab) []string { return []string{open, denied} }
	t.Cleanup(func() { SandboxWrites = func(*Core, *Tab) []string { return nil } })
	c, tb, cl := checkedCore(t, runsCodex+"exec bwrap --dev-bind / / --tmpfs \"$NERDR_CLIENT_DIR\" --ro-bind "+denied+" "+denied+" --unshare-pid -- \"$@\"\n")
	notes, started := check(t, c, tb, cl)
	if started {
		t.Fatal("started with a critical writable path")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "critical writable paths ("+open+")") || strings.Contains(notes[0], denied) {
		t.Fatalf("notices %q", notes)
	}
}

// A sandbox that lets the agent write where install = "home" puts nerdr's
// skills, plugins and hooks is warned of; the agent starts.
func TestASandboxThatLetsTheAgentWriteTheHomeInstallIsWarnedOf(t *testing.T) {
	if err := exec.Command("bwrap", "--dev-bind", "/", "/", "--unshare-pid", "true").Run(); err != nil {
		t.Skip("no bwrap that runs here: ", err)
	}
	open, denied := t.TempDir(), t.TempDir()
	SandboxWarns = func(*Core, *Tab) []string { return []string{open, denied} }
	t.Cleanup(func() { SandboxWarns = func(*Core, *Tab) []string { return nil } })
	c, tb, cl := checkedCore(t, runsCodex+"exec bwrap --dev-bind / / --tmpfs \"$NERDR_CLIENT_DIR\" --ro-bind "+denied+" "+denied+" --unshare-pid -- \"$@\"\n")
	notes, started := check(t, c, tb, cl)
	if !started {
		t.Fatalf("not started: %s, notices %q", tb.Err, notes)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "("+open+")") || strings.Contains(notes[0], denied) ||
		!strings.Contains(notes[0], `install = "worktree"`) {
		t.Fatalf("notices %q", notes)
	}
}

// The folders install = "home" writes for the agents are tried only with
// that setting, and only those that are there are found; none is made.
func TestSandboxWarnsAreTheHomeInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	// An agent found by its own path; not /bin/true, which macOS has not.
	agent := filepath.Join(bin, "agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Core{}
	c.Cfg.Agents = map[agents.Kind]agents.Spec{
		agents.Claude:      {Command: []string{agent}},
		agents.OpenCode:    {Command: []string{agent}},
		agents.Antigravity: {Command: []string{agent}},
	}
	c.Cfg.Install = config.InstallWorktree
	if w := c.sandboxWarns(&Tab{}); w != nil {
		t.Fatalf("warns with install = worktree: %q", w)
	}
	c.Cfg.Install = config.InstallHome
	warns := c.sandboxWarns(&Tab{})
	skillsDir := filepath.Join(home, ".claude", "skills")
	plugin := hooks.OpenCodePluginHome(home)
	for _, want := range []string{skillsDir, plugin, filepath.Dir(plugin), hooks.AgyHooksHome(home)} {
		if !slices.Contains(warns, want) {
			t.Errorf("%s is not among the warns: %q", want, warns)
		}
	}
	if slices.Contains(warns, hooks.CopilotPluginHome(home)) {
		t.Errorf("copilot's old home plugin is among the warns: %q", warns)
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := sandboxcheck.Probe(sandboxcheck.Target{Nonce: "n", Warns: warns})
	if !slices.Equal(r.Warns, []string{skillsDir}) || r.Writes != nil {
		t.Fatalf("found %q, writes %q: want only %s, which is there", r.Warns, r.Writes, skillsDir)
	}
	if _, err := os.Stat(filepath.Dir(plugin)); !os.IsNotExist(err) {
		t.Fatalf("the check made %s: %v", filepath.Dir(plugin), err)
	}
}

// Policy coverage is deterministic even on hosts without bwrap.
func TestSandboxRefusalPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ev      SandboxCheckedMsg
		refused bool
	}{
		{"unknown", SandboxCheckedMsg{}, true},
		{"clear", SandboxCheckedMsg{OK: true}, false},
		{"client", SandboxCheckedMsg{OK: true, Result: sandboxcheck.Result{Client: []string{"socket"}}}, true},
		{"holder", SandboxCheckedMsg{OK: true, Result: sandboxcheck.Result{Holder: []string{"ptrace"}}}, true},
		{"writes", SandboxCheckedMsg{OK: true, Result: sandboxcheck.Result{Writes: []string{"/fixture/config"}}}, true},
		{"writes and client", SandboxCheckedMsg{OK: true, Result: sandboxcheck.Result{Client: []string{"socket"}, Writes: []string{"/fixture/config"}}}, true},
		{"warns", SandboxCheckedMsg{OK: true, Result: sandboxcheck.Result{Warns: []string{"/fixture/skills"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ev.Kind = agents.Codex
			if got := tc.ev.Refused(); got != tc.refused {
				t.Fatalf("refused %v, want %v", got, tc.refused)
			}
			cl := &toldClient{}
			m := &Core{Client: cl, Root: t.TempDir()}
			m.OnSandboxChecked(tc.ev)
			if !tc.refused {
				switch {
				case len(tc.ev.Result.Warns) > 0:
					if len(cl.notices) != 1 || !strings.Contains(cl.notices[0], "codex's sandbox lets it write where nerdr installs") ||
						!strings.Contains(cl.notices[0], "(/fixture/skills)") || !strings.Contains(cl.notices[0], `install = "worktree"`) {
						t.Fatalf("no warning of the home install: %q", cl.notices)
					}
				case len(cl.notices) != 0:
					t.Fatalf("clear check warned: %q", cl.notices)
				}
				return
			}
			if len(cl.notices) != 1 || !strings.Contains(cl.notices[0], "nerdr did not start codex") {
				t.Fatalf("missing refusal: %q", cl.notices)
			}
			if len(tc.ev.Result.Writes) > 0 && !strings.Contains(cl.notices[0], "critical writable paths (/fixture/config)") {
				t.Fatalf("missing writable path: %q", cl.notices)
			}
			tb := &Tab{}
			if cmd := m.SpawnPrepared(tb, agents.Launch{}, false, SpawnPrep{Check: &tc.ev}); cmd != nil || tb.Pane != nil || tb.Light != StExited || !strings.Contains(tb.Err, "writes to critical paths") {
				t.Fatalf("refused check did not stop launch: pane %v, state %v, error %q", tb.Pane, tb.Light, tb.Err)
			}
		})
	}
}

func TestSandboxWritesAreCheckedWithoutHolder(t *testing.T) {
	c, tb, _ := checkedCore(t, runsCodex+"exec \"$@\"\n")
	c.Held = nil
	open := t.TempDir()
	old := SandboxWrites
	SandboxWrites = func(*Core, *Tab) []string { return []string{open} }
	t.Cleanup(func() { SandboxWrites = old })
	check := c.CheckSandbox(tb, os.Environ())
	if check == nil {
		t.Fatal("no check without holder")
	}
	ev := check()
	if !ev.OK || !ev.Refused() || !slices.Contains(ev.Result.Writes, open) {
		t.Fatalf("writable path not checked: %+v", ev)
	}
}

// A sandbox may deny the state folder and still allow a folder in it: the
// temporary folder, where a new worktree's files are checked out from, is
// tried by itself, and is there to be tried.
func TestSandboxWritesTryTheStateTemporaryFolder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tmp := statedir.TempDir()
	c := &Core{}
	writes := c.sandboxWrites(&Tab{})
	if !slices.Contains(writes, tmp) {
		t.Fatalf("%s is not among what the check tries to write: %v", tmp, writes)
	}
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Fatalf("%s is not there for the check to try: %v", tmp, err)
	}
	if r := sandboxcheck.Probe(sandboxcheck.Target{Nonce: "n", Writes: writes}); !slices.Contains(r.Writes, tmp) {
		t.Fatalf("a writable %s is not found: %v", tmp, r.Writes)
	}
}

// An agent that starts often does not say the same at each start: the
// warning comes once for each kind of agent, and again when what its
// sandbox lets it write has changed.
func TestTheHomeInstallIsWarnedOfOnceUntilItChanges(t *testing.T) {
	cl := &toldClient{}
	m := &Core{Client: cl, Root: t.TempDir()}
	warns := func(k agents.Kind, paths ...string) {
		m.OnSandboxChecked(SandboxCheckedMsg{Kind: k, OK: true, Result: sandboxcheck.Result{Warns: paths}})
	}
	for _, step := range []struct {
		what  string
		do    func()
		total int
	}{
		{"the first start", func() { warns(agents.Copilot, "/fixture/skills") }, 1},
		{"the same again", func() { warns(agents.Copilot, "/fixture/skills") }, 1},
		{"another kind", func() { warns(agents.OpenCode, "/fixture/skills") }, 2},
		{"more to write", func() { warns(agents.Copilot, "/fixture/skills", "/fixture/hooks") }, 3},
		{"nothing to write", func() { warns(agents.Copilot) }, 3},
		{"writable once more", func() { warns(agents.Copilot, "/fixture/skills") }, 4},
	} {
		step.do()
		if len(cl.notices) != step.total {
			t.Fatalf("%s: %d notices, want %d: %q", step.what, len(cl.notices), step.total, cl.notices)
		}
	}
}
