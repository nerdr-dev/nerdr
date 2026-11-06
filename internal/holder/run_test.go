package holder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// roleArg has the test binary play a part: the holder, a core or an agent,
// in processes of their own as nerdr will run them.
const roleArg = "holder-test-role"

func init() {
	if len(os.Args) > 2 && os.Args[1] == roleArg {
		os.Exit(playRole(os.Args[2], os.Args[3:]))
	}
}

func self() string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return exe
}

func playRole(role string, args []string) int {
	switch role {
	case "holder":
		backoffFirst = 50 * time.Millisecond
		defaultExecArgv = func() ([]string, error) { return []string{self(), roleArg, "holder"}, nil }
		if len(args) > 0 && args[0] == resumeArg {
			return Main(args)
		}
		return Main(append([]string{"--", self(), roleArg}, args...))
	case "core-upgrade":
		return coreUpgradesTheHolder()
	case "core-agent":
		return coreStartsAnAgent()
	case "core-crash":
		return coreCrashesOnce()
	case "agent":
		return agentReachesFor(args)
	}
	fmt.Fprintln(os.Stderr, "no role", role)
	return 2
}

// sockets is the descriptors this process has that are sockets.
func sockets() []int {
	var fds []int
	for fd := 0; fd < 256; fd++ {
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) == nil && st.Mode&syscall.S_IFMT == syscall.S_IFSOCK {
			fds = append(fds, fd)
		}
	}
	return fds
}

// report is what the agent found, as the core prints it.
type report struct {
	HolderPid, CorePid, CoreParent int
	AgentParent                    int
	AgentSockets                   []int
	CoreSockets                    []int
	ProcOpen                       map[int]string
	Getfd                          map[int]string
}

func coreStartsAnAgent() int {
	cl, err := FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r := report{HolderPid: cl.Pid(), CorePid: os.Getpid(), CoreParent: os.Getppid(), CoreSockets: sockets()}
	argv := []string{self(), roleArg, "agent", strconv.Itoa(os.Getpid())}
	for _, fd := range r.CoreSockets {
		argv = append(argv, strconv.Itoa(fd))
	}
	p, err := cl.Spawn(spawn.Spec{Argv: argv, Env: os.Environ()})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out, _ := io.ReadAll(p)
	if err := p.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "agent: %v: %s\n", err, out)
		return 1
	}
	p.Close()
	var a report
	if err := json.Unmarshal(out, &a); err != nil {
		fmt.Fprintf(os.Stderr, "agent said %q\n", out)
		return 1
	}
	r.AgentParent, r.AgentSockets, r.ProcOpen, r.Getfd = a.AgentParent, a.AgentSockets, a.ProcOpen, a.Getfd
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
	cl.Shutdown()
	return 0
}

// agentReachesFor tries what a sandboxed agent could to get the core's
// socket: one of its own, the core's /proc entry, pidfd_getfd.
func agentReachesFor(args []string) int {
	core, _ := strconv.Atoi(args[0])
	r := report{AgentParent: os.Getppid(), AgentSockets: sockets(), ProcOpen: map[int]string{}, Getfd: map[int]string{}}
	for _, a := range args[1:] {
		fd, _ := strconv.Atoi(a)
		r.ProcOpen[fd] = errString(openThroughProc(core, fd))
		r.Getfd[fd] = errString(getfd(core, fd))
	}
	b, _ := json.Marshal(r)
	fmt.Print(string(b))
	return 0
}

func errString(err error) string {
	if err == nil {
		return "GOT IT"
	}
	return err.Error()
}

func coreCrashesOnce() int {
	cl, err := FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	progs, err := cl.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(progs) == 0 {
		p, err := cl.SpawnAs("keep", spawn.Spec{Argv: []string{"sleep", "60"}}, Mark{Sandboxed: true, Secret: "s"}, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("first core %d started %d\n", os.Getpid(), p.Pid())
		os.Exit(3) // a crash, as far as the holder can tell
	}
	p := progs[0]
	fmt.Printf("second core %d found %s %d %s sandboxed=%v secret=%s\n", os.Getpid(), p.ID, p.Pid, p.State, p.Sandboxed, p.Secret)
	cl.Shutdown()
	return 0
}

func runHolder(t *testing.T, core string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self(), roleArg, "holder", core)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("holder: %v\n%s\n%s", err, out, stderr.String())
	}
	return string(out)
}

// A process where an agent is, a child of the holder beside the core,
// cannot reach the socket the holder and core share: it does not inherit
// it, cannot open it through the core's /proc entry, nor take it with
// pidfd_getfd.
func TestAnAgentCannotReachTheHoldersSocket(t *testing.T) {
	// A socket the holder is given without close-on-exec, as a careless
	// parent might, does not reach the agent either.
	syscall.ForkLock.Lock()
	leak, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	syscall.ForkLock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(leak[0])
	defer syscall.Close(leak[1])
	out := runHolder(t, "core-agent")
	var r report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("core said %q", out)
	}
	if r.CoreParent != r.HolderPid || r.AgentParent != r.HolderPid {
		t.Errorf("holder %d; the core's parent is %d, the agent's %d", r.HolderPid, r.CoreParent, r.AgentParent)
	}
	if len(r.CoreSockets) == 0 {
		t.Fatal("the core has no socket")
	}
	if len(r.AgentSockets) != 0 {
		t.Errorf("the agent has sockets %v", r.AgentSockets)
	}
	for fd, err := range r.ProcOpen {
		if err == "GOT IT" {
			t.Errorf("the agent opened the core's descriptor %d through /proc", fd)
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope"); err == nil && strings.TrimSpace(string(b)) == "0" {
		// Yama does not stop a process of the same user here; a sandbox
		// must (nerdr is to warn of it, in a later step).
		t.Logf("ptrace_scope is 0: pidfd_getfd gave %v", r.Getfd)
		r.Getfd = nil
	}
	for fd, err := range r.Getfd {
		if err == "GOT IT" {
			t.Errorf("the agent took the core's descriptor %d with pidfd_getfd", fd)
		}
	}
	t.Logf("%+v", r)
}

// A core that crashes is started again, on a new socket pair, and finds
// the program it started alive and as it marked it.
func TestACoreStartedAgainFindsItsPrograms(t *testing.T) {
	out := runHolder(t, "core-crash")
	var core1, pid1, core2, pid2 int
	var id, state, rest string
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("cores said %q", out)
	}
	fmt.Sscanf(lines[0], "first core %d started %d", &core1, &pid1)
	fmt.Sscanf(lines[1], "second core %d found %s %d %s %s", &core2, &id, &pid2, &state, &rest)
	if core1 == 0 || core1 == core2 || pid1 == 0 || pid1 != pid2 || id != "keep" || state != Running {
		t.Errorf("cores said %q", out)
	}
	if !strings.Contains(lines[1], "sandboxed=true secret=s") {
		t.Errorf("the mark did not stay: %q", lines[1])
	}
	if err := syscall.Kill(pid1, 0); err != syscall.ESRCH {
		t.Errorf("%d outlived the holder's shutdown: %v", pid1, err)
	}
}

func coreUpgradesTheHolder() int {
	fail := func(f string, a ...any) int {
		fmt.Fprintf(os.Stderr, "core: "+f+"\n", a...)
		return 1
	}
	cl, err := FromEnv()
	if err != nil {
		return fail("%v", err)
	}
	holder := cl.Pid()
	sh, err := cl.SpawnAs("sh", spawn.Spec{Argv: []string{"sh", "-c", "read x; echo got $x; read y; exit 5"}, PTY: true, W: 80, H: 24},
		Mark{Sandboxed: true, Secret: "s3cret", Label: "agent"}, 0)
	if err != nil {
		return fail("%v", err)
	}
	z, err := cl.SpawnAs("z", spawn.Spec{Argv: []string{"true"}}, Mark{}, 0)
	if err != nil {
		return fail("%v", err)
	}
	<-z.exited // its exit is in, not acked: it stays a zombie
	if err := cl.Checkpoint("sh", 0, []byte("screen")); err != nil {
		return fail("%v", err)
	}
	// A read waiting in the holder through the upgrades.
	got := make(chan string, 1)
	go func() {
		var b []byte
		buf := make([]byte, 1024)
		for !strings.Contains(string(b), "got x") {
			n, err := sh.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				break
			}
		}
		got <- string(b)
	}()
	time.Sleep(100 * time.Millisecond)
	for i := range 2 {
		if err := cl.Upgrade(); err != nil {
			return fail("upgrade %d: %v", i, err)
		}
	}
	args, _ := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(holder)).Output()
	if !strings.Contains(string(args), resumeArg) {
		return fail("holder %d runs %q", holder, args)
	}
	if os.Getppid() != holder {
		return fail("parent %d, holder %d", os.Getppid(), holder)
	}
	progs, err := cl.List()
	if err != nil {
		return fail("%v", err)
	}
	for _, p := range progs {
		switch p.ID {
		case "sh":
			if p.State != Running || p.Pid != sh.Pid() || !p.Sandboxed || p.Secret != "s3cret" || p.Label != "agent" || p.Checkpoint != 0 {
				return fail("sh after: %+v", p)
			}
		case "z":
			if p.State != Exited {
				return fail("z after: %+v", p)
			}
		}
	}
	if syscall.Kill(z.Pid(), 0) != nil {
		return fail("z was reaped before its exit was acked")
	}
	if err := z.Wait(); err != nil {
		return fail("z: %v", err)
	}
	if err := syscall.Kill(z.Pid(), 0); err != syscall.ESRCH {
		return fail("z not reaped after the ack: %v", err)
	}
	if _, err := sh.Write([]byte("x\r")); err != nil {
		return fail("write: %v", err)
	}
	select {
	case out := <-got:
		if !strings.Contains(out, "got x") {
			return fail("read %q", out)
		}
	case <-time.After(5 * time.Second):
		return fail("the read from before the upgrade never came")
	}
	sh.Write([]byte("y\r"))
	if err := sh.Wait(); err == nil || err.Error() != "exit status 5" {
		return fail("sh: %v", err)
	}
	fmt.Println("ok", holder)
	cl.Shutdown()
	return 0
}

// The holder execs itself again with the same PID, and its programs,
// their terminals, their marks, a read the core was waiting on, an exit
// not yet acked and the socket to the core are all as they were.
func TestUpgradeKeepsTheProgramsAsTheyWere(t *testing.T) {
	out := runHolder(t, "core-upgrade")
	if !strings.HasPrefix(out, "ok ") {
		t.Fatalf("core said %q", out)
	}
}
