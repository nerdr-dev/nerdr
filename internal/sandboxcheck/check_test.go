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
		{Nonce: "n3", Pids: []int{7}, Writes: []string{"/home/u/.config/nerdr", "/home/u/dev/repo/.git/config"}},
		{Nonce: "n4", Writes: []string{"/w"}},
		{Nonce: "n5", Pids: []int{7}, Warns: []string{"/home/u/.claude/skills"}},
		{Nonce: "n6", Writes: []string{"/w"}, Warns: []string{"/home/u/.agents/skills", "/home/u/.grok/hooks"}},
		{Nonce: "n7", Pids: []int{7}, APISock: "/run/user/1/nerdr/api-a.sock", APIKey: "/run/user/1/nerdr/api-a.key"},
		{Nonce: "n8", Writes: []string{"/w"}, Warns: []string{"/h"}, APISock: "/run/user/1/nerdr/api-a.sock"},
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
	if _, err := ParseArgs([]string{"n", "-", "-", "-", "-", "--api", "/s"}); err == nil {
		t.Error("the API's socket without its key was taken")
	}
}

// TestProbeReachesAnOpenAPISocket: the API's socket and key are ways in
// as the client socket's are.
func TestProbeReachesAnOpenAPISocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "nck")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock, key := filepath.Join(dir, "api.sock"), filepath.Join(dir, "api.key")
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
	r := Probe(Target{APISock: sock, APIKey: key})
	if !slices.Equal(r.Client, []string{"api-socket", "api-key"}) || !r.Reached() {
		t.Errorf("reached %v", r.Client)
	}
	for _, w := range []string{"api-socket", "api-key"} {
		if !(Result{Client: []string{"folder", w}}).Reached() {
			t.Errorf("%s does not count as reached", w)
		}
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
	// A check of a nerdr from before Warns answers without them.
	if r, ok := Parse([]byte("NERDR-SANDBOX-CHECK abc {\"writes\":[\"/w\"]}\r\n"), "abc"); !ok || !slices.Equal(r.Writes, []string{"/w"}) || r.Warns != nil {
		t.Errorf("an answer without warns: got %+v, %v", r, ok)
	}
	if r, ok := Parse([]byte("NERDR-SANDBOX-CHECK abc {\"warns\":[\"/h\"]}\r\n"), "abc"); !ok || !slices.Equal(r.Warns, []string{"/h"}) || r.Reached() || r.Writes != nil {
		t.Errorf("an answer with warns: got %+v, %v", r, ok)
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
	// Short, for the socket's path: t.TempDir's is too long for one on
	// macOS.
	dir, err := os.MkdirTemp("", "nck")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
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
	if !slices.Equal(r.Client, []string{"folder", "socket", "key"}) || !r.Reached() {
		t.Errorf("reached %v", r.Client)
	}
	if (Result{Client: []string{"folder"}}).Reached() {
		t.Error("seeing into the folder alone counts as reaching the socket")
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

// The check says which of the folders and files it must not write it
// could: a folder by making a file in it, which it leaves nothing of, a
// file by opening it to append to. One that is not there, or is read
// only, it could not.
func TestProbeFindsWhatItCanWrite(t *testing.T) {
	dir := t.TempDir()
	open, closed := filepath.Join(dir, "open"), filepath.Join(dir, "closed")
	file, locked := filepath.Join(dir, "config"), filepath.Join(dir, "locked")
	for _, d := range []string{open, closed} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{file, locked} {
		if err := os.WriteFile(f, []byte("as it was\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getuid() == 0 {
		t.Skip("root writes what is read only")
	}
	_ = os.Chmod(closed, 0o555)
	_ = os.Chmod(locked, 0o444)
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })

	// A read-only file in a folder that can be written can be replaced,
	// and one not there made: both are writes. One not there in a folder
	// that cannot be written cannot be made.
	gone, goneShut := filepath.Join(dir, "gone"), filepath.Join(closed, "gone")
	want := []string{open, file, locked, gone}
	r := Probe(Target{Nonce: "n", Writes: []string{open, closed, file, locked, gone, goneShut}})
	if !slices.Equal(r.Writes, want) {
		t.Fatalf("writes = %q, want %q", r.Writes, want)
	}
	if r.Warns != nil {
		t.Fatalf("warns = %q, with none asked", r.Warns)
	}
	if w := Probe(Target{Nonce: "n", Warns: []string{open, closed, file, locked, gone, goneShut}}); w.Writes != nil || !slices.Equal(w.Warns, want) {
		t.Fatalf("warns = %q, writes = %q: want %q as warns", w.Warns, w.Writes, want)
	}
	for _, p := range []string{gone, goneShut} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("the check left %s: %v", p, err)
		}
	}
	if left, _ := os.ReadDir(dir); len(left) != 4 {
		t.Fatalf("the check left more behind in %s: %v", dir, left)
	}
	if r.Reached() {
		t.Fatal("what it can write is not a way to the client socket or the holder")
	}
	if left, _ := os.ReadDir(open); len(left) != 0 {
		t.Fatalf("the check left %v behind", left)
	}
	for _, f := range []string{file, locked} {
		if b, _ := os.ReadFile(f); string(b) != "as it was\n" {
			t.Fatalf("the check changed %s: %q", f, b)
		}
	}
}

// gitDir makes a repository's .git as the check finds it: config, and
// hooks unless noHooks, in a folder of their own.
func gitDir(t *testing.T, noHooks bool) (git, config, hooks string) {
	t.Helper()
	git = filepath.Join(t.TempDir(), ".git")
	config, hooks = filepath.Join(git, "config"), filepath.Join(git, "hooks")
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !noHooks {
		if err := os.Mkdir(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return git, config, hooks
}

// sameFile fails t when path is not, in all, what before was: the same
// file, with the same content, mode and time.
func sameFile(t *testing.T, path string, before os.FileInfo, content string) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !os.SameFile(st, before) || st.Mode() != before.Mode() || !st.ModTime().Equal(before.ModTime()) || st.Size() != before.Size() {
		t.Fatalf("%s changed: %v %v, was %v %v", path, st.Mode(), st.ModTime(), before.Mode(), before.ModTime())
	}
	if b, _ := os.ReadFile(path); string(b) != content {
		t.Fatalf("%s now holds %q", path, b)
	}
}

// A repository without .git/hooks (git init --template=, or removed) is
// not passed as kept from: when the agent could make it, and put hooks in
// it, that is found, and nothing is left behind. When .git cannot be
// written, it is not.
func TestMissingHooksAreTriedByMakingThem(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes what is read only")
	}
	git, config, hooks := gitDir(t, true)
	r := Probe(Target{Nonce: "n", Writes: []string{config, hooks}})
	if !slices.Contains(r.Writes, hooks) {
		t.Fatalf("a .git/hooks the agent can make passed: %q", r.Writes)
	}
	if _, err := os.Lstat(hooks); !os.IsNotExist(err) {
		t.Fatalf("the check left .git/hooks behind: %v", err)
	}

	if err := os.Chmod(git, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(git, 0o755) })
	r = Probe(Target{Nonce: "n", Writes: []string{hooks}})
	if slices.Contains(r.Writes, hooks) {
		t.Fatalf("a .git/hooks that cannot be made was found writable: %q", r.Writes)
	}
	if left, _ := os.ReadDir(git); len(left) != 1 {
		t.Fatalf("the check left %v in .git", left)
	}
}

// A .git/config the agent cannot open to write, only by its mode, but
// could replace by renaming a file of its own over it, as .git can be
// written, is found; one in a .git that cannot be written is not. The
// check never writes, renames or removes .git/config itself.
func TestAReadOnlyConfigThatCanBeReplacedIsFound(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes what is read only")
	}
	const content = "[core]\n\tbare = false\n"
	git, config, _ := gitDir(t, false)
	if err := os.Chmod(config, 0o444); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(config)
	if err != nil {
		t.Fatal(err)
	}
	if r := Probe(Target{Nonce: "n", Writes: []string{config}}); !slices.Contains(r.Writes, config) {
		t.Fatalf("a .git/config that can be replaced passed: %q", r.Writes)
	}
	sameFile(t, config, before, content)
	if left, _ := os.ReadDir(git); len(left) != 2 {
		t.Fatalf("the check left %v in .git", left)
	}

	if err := os.Chmod(git, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(git, 0o755) })
	if r := Probe(Target{Nonce: "n", Writes: []string{config}}); slices.Contains(r.Writes, config) {
		t.Fatalf("a .git/config in a .git that cannot be written was found writable: %q", r.Writes)
	}
	sameFile(t, config, before, content)

	// One that can be written is found, and left as it was too.
	_ = os.Chmod(git, 0o755)
	if err := os.Chmod(config, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ = os.Lstat(config)
	if r := Probe(Target{Nonce: "n", Writes: []string{config}}); !slices.Contains(r.Writes, config) {
		t.Fatalf("a writable .git/config passed: %q", r.Writes)
	}
	sameFile(t, config, before, content)
}
