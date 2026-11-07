package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
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
	c, tb := heldCore(t, inProcessHolder(t), wrap)
	c.Root = filepath.Dir(wrap)
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{wrap, "codex"}, Sandboxed: true}
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

// check starts tb's agent and runs the check that comes of it, as the
// loop would, and is the notices it led to.
func check(t *testing.T, c *Core, tb *Tab, cl *toldClient) []string {
	t.Helper()
	cmd := c.Start(tb, false)
	if tb.Pane == nil {
		t.Fatalf("not started: %s", tb.Err)
	}
	if cmd == nil {
		t.Fatal("no check")
	}
	ev, ok := cmd().(SandboxCheckedMsg)
	if !ok {
		t.Fatalf("got %T", ev)
	}
	c.OnSandboxChecked(ev)
	// Once a kind and command: not again while the core runs.
	if c.CheckSandbox(tb, nil) != nil {
		t.Error("checked twice")
	}
	return cl.notices
}

// runsCodex has the wrapper run codex as a program that waits; asked for
// its flags, it has none.
const runsCodex = "if [ \"$1\" = codex ]; then case \"$*\" in *--help*) exit 0;; esac; exec sleep 30; fi\n"

// A wrapper that is no sandbox lets the check reach the client socket,
// and the user is warned.
func TestAWrapperThatLetsTheAgentReachTheClientSocketIsWarnedOf(t *testing.T) {
	c, tb, cl := checkedCore(t, runsCodex+"exec \"$@\"\n")
	notes := check(t, c, tb, cl)
	if len(notes) != 1 || !strings.Contains(notes[0], "codex's sandbox lets it reach nerdr's client socket") ||
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
	if notes := check(t, c, tb, cl); len(notes) != 0 {
		t.Errorf("notices %q", notes)
	}
}

// A sandbox in which nerdr does not run, or whose command names no
// agent to put the check in place of, is said to be unchecked.
func TestASandboxNerdrCannotCheckIsSaidSo(t *testing.T) {
	c, tb, cl := checkedCore(t, runsCodex+"echo no nerdr in here\n")
	notes := check(t, c, tb, cl)
	if len(notes) != 1 || !strings.Contains(notes[0], "nerdr could not check from inside codex's sandbox") ||
		!strings.Contains(notes[0], "the check did not answer") {
		t.Errorf("notices %q", notes)
	}

	c, tb, cl = checkedCore(t, runsCodex)
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: []string{filepath.Join(c.Root, "agent.sh")}, Sandboxed: true}
	tb.Agent = agents.Codex
	notes = check(t, c, tb, cl)
	if len(notes) != 1 || !strings.Contains(notes[0], "names no codex") {
		t.Errorf("notices %q", notes)
	}
}

// A check that never ends is ended, and said not to have answered.
func TestACheckThatHangsIsEnded(t *testing.T) {
	old := SandboxCheckWait
	SandboxCheckWait = 300 * time.Millisecond
	t.Cleanup(func() { SandboxCheckWait = old })
	c, tb, cl := checkedCore(t, runsCodex+"exec sleep 30\n")
	start := time.Now()
	notes := check(t, c, tb, cl)
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "did not answer") {
		t.Errorf("notices %q", notes)
	}
}

// An agent outside a sandbox, or a core with no holder, is not checked.
func TestOnlyASandboxedAgentOverAHolderIsChecked(t *testing.T) {
	c, tb, _ := checkedCore(t, runsCodex+"exec \"$@\"\n")
	c.Cfg.Agents[agents.Codex] = agents.Spec{Command: c.Cfg.Agents[agents.Codex].Command}
	if cmd := c.Start(tb, false); cmd != nil || tb.Pane == nil {
		t.Errorf("an agent outside a sandbox checked, or not started: %s", tb.Err)
	}
	tb.InSandbox = true
	c.Held = nil
	if c.CheckSandbox(tb, nil) != nil {
		t.Error("checked with no holder")
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
