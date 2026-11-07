package sandboxcheck

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestArgsComeBackAsTheyWent(t *testing.T) {
	for _, in := range []Target{
		{Nonce: "n1", ClientDir: "/run/user/1/nerdr", Sock: "/run/user/1/nerdr/core-a.sock", Key: "/run/user/1/nerdr/core-a.key", PidNS: "pid:[4026531836]", Pids: []int{10, 11}},
		{Nonce: "n2"}, // empty ones survive a wrapper that drops empty arguments
	} {
		args := in.Args()
		if slices.Contains(args, "") {
			t.Errorf("%+v: an empty argument in %q", in, args)
		}
		out, err := ParseArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(out.Pids, in.Pids) {
			t.Errorf("pids %v, want %v", out.Pids, in.Pids)
		}
		out.Pids, in.Pids = nil, nil
		if !reflect.DeepEqual(out, in) {
			t.Errorf("got %+v, want %+v", out, in)
		}
	}
	if _, err := ParseArgs([]string{"n", "-", "-", "-", "-", "x"}); err == nil {
		t.Error("a bad process id was taken")
	}
}

// The answer is found in whatever else the sandbox prints around it, and
// only the one with the nonce asked for.
func TestParseFindsTheAnswerWithItsNonce(t *testing.T) {
	out := []byte("starting sandbox...\r\nNERDR-SANDBOX-CHECK other {\"client\":[\"key\"]}\r\n" +
		"NERDR-SANDBOX-CHECK abc {\"client\":[\"socket\"],\"holder\":[\"ptrace 12\"]}\r\nbye\r\n")
	r, ok := Parse(out, "abc")
	if !ok || !slices.Equal(r.Client, []string{"socket"}) || !slices.Equal(r.Holder, []string{"ptrace 12"}) {
		t.Errorf("got %+v, %v", r, ok)
	}
	if _, ok := Parse([]byte("sandbox: nerdr: not found\r\n"), "abc"); ok {
		t.Error("an answer found where there is none")
	}
	if r, ok := Parse([]byte("NERDR-SANDBOX-CHECK abc {}\r\n"), "abc"); !ok || r.Reached() {
		t.Errorf("nothing reached: got %+v, %v", r, ok)
	}
}

func TestWrapperIsWhatComesBeforeTheAgent(t *testing.T) {
	for _, c := range []struct {
		command []string
		want    []string
		ok      bool
	}{
		{[]string{"my-sandbox", "--", "copilot"}, []string{"my-sandbox", "--"}, true},
		{[]string{"copilot"}, []string{}, true},
		{[]string{"/usr/bin/bwrap", "--ro-bind", "/", "/", "/opt/bin/copilot", "--yolo"}, []string{"/usr/bin/bwrap", "--ro-bind", "/", "/"}, true},
		{[]string{"npx", "-y", "@github/copilot"}, []string{"npx", "-y"}, true},
		{[]string{"my-sandboxed-copilot"}, nil, false},
	} {
		got, ok := Wrapper(c.command, "copilot")
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, %v; want %q, %v", c.command, got, ok, c.want, c.ok)
		}
	}
	argv := Argv([]string{"sb", "--"}, "/bin/nerdr", Target{Nonce: "n"})
	if !slices.Equal(argv[:4], []string{"sb", "--", "/bin/nerdr", Arg}) {
		t.Errorf("argv %q", argv)
	}
}

// With nothing in the way, the check reaches the client socket's
// folder, the socket and the key.
func TestProbeReachesAnOpenClientSocket(t *testing.T) {
	dir := t.TempDir()
	sock, key := filepath.Join(dir, "core.sock"), filepath.Join(dir, "core.key")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if err := os.WriteFile(key, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Probe(Target{ClientDir: dir, Sock: sock, Key: key})
	if !slices.Equal(r.Client, []string{"folder", "socket", "key"}) {
		t.Errorf("reached %v", r.Client)
	}
	// Gone, or shut away: nothing.
	ln.Close()
	os.Remove(key)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if os.Getuid() == 0 {
		t.Skip("root reads what it likes")
	}
	if r := Probe(Target{ClientDir: dir, Sock: sock, Key: key}); r.Reached() {
		t.Errorf("reached %+v of a folder shut", r)
	}
}

// A process the check may trace, as one it started may be under Yama,
// is reached; one in another pid namespace than the core's is not tried.
func TestProbeReachesAProcessItMayTrace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	if b, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope"); err == nil && strings.TrimSpace(string(b)) >= "2" {
		t.Skip("ptrace_scope " + strings.TrimSpace(string(b)) + " lets no process trace its child")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	pid := cmd.Process.Pid
	r := Probe(Target{PidNS: PidNS(), Pids: []int{pid}})
	for _, way := range []string{"pidfd_getfd", "ptrace"} {
		if !slices.ContainsFunc(r.Holder, func(s string) bool { return strings.HasPrefix(s, way+" ") }) {
			t.Errorf("not reached by %s: %v", way, r.Holder)
		}
	}
	// Let go of: it runs on, traced by none, and not stopped.
	st, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(st), "TracerPid:\t0\n") || strings.Contains(string(st), "State:\tt") {
		t.Errorf("still traced or stopped:\n%s", st)
	}
	if r := Probe(Target{PidNS: "pid:[1]", Pids: []int{pid}}); r.Reached() {
		t.Errorf("tried in another pid namespace: %+v", r)
	}
}
