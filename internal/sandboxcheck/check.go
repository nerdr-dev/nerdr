// Package sandboxcheck looks from inside an agent's sandbox at what of
// nerdr it can reach that it must not: the client socket, which can do
// anything the user can, and the holder, which runs every program. nerdr
// runs the check through the agent's own command, with nerdr in place of
// the agent, when an agent set sandboxed starts (see core's
// CheckSandbox), and warns when the sandbox lets it through.
//
// This catches a sandbox set up wrong, not an agent that means harm: one
// that gets past the sandbox some other way is not stopped by it. Nothing
// here depends on which sandbox it is; each is asked the same.
package sandboxcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Arg is the first argument that runs nerdr as the check: nerdr
// --nerdr-sandbox-check, then Target's Args.
const Arg = "--nerdr-sandbox-check"

// marker starts the line the check answers on, which the nonce follows,
// so that nothing else the sandbox prints is taken for it.
const marker = "NERDR-SANDBOX-CHECK"

// Target is what the check is to try to reach: the client socket's
// folder, the socket and its key, and the processes holding the holder's
// socket, the holder and the core. PidNS is the core's pid namespace, as
// /proc/self/ns/pid links to it, "" when not known: the process ids mean
// those processes only in it.
type Target struct {
	Nonce     string
	ClientDir string
	Sock, Key string
	PidNS     string
	Pids      []int
	// Writes are folders and files the agent must not be able to write:
	// nerdr's config and state, the main checkouts' .git/config and
	// .git/hooks, the folder the other tabs' worktrees are in.
	Writes []string
	// Warns are folders and files the agent had better not be able to
	// write, but that do not keep it from starting: those nerdr puts its
	// skills, plugins and hooks in in the home folder, for every agent.
	Warns []string
}

// writesSep comes before Writes in Args, warnsSep before Warns; each
// after the process ids, Writes first. A check of a nerdr from before
// Warns takes them for more Writes, or warnsSep for a bad process id: it
// refuses more than it needs to, not less.
const (
	writesSep = "--"
	warnsSep  = "--warn"
)

// none stands for an empty argument.
const none = "-"

// Args are t as the check's arguments.
func (t Target) Args() []string {
	out := []string{t.Nonce}
	for _, s := range []string{t.ClientDir, t.Sock, t.Key, t.PidNS} {
		if s == "" {
			s = none // a wrapper that runs the command through a shell may drop an empty one
		}
		out = append(out, s)
	}
	for _, p := range t.Pids {
		out = append(out, strconv.Itoa(p))
	}
	if len(t.Writes) > 0 {
		out = append(append(out, writesSep), t.Writes...)
	}
	if len(t.Warns) > 0 {
		out = append(append(out, warnsSep), t.Warns...)
	}
	return out
}

// ParseArgs is the Target that Args made args of.
func ParseArgs(args []string) (Target, error) {
	if len(args) < 5 {
		return Target{}, errors.New("sandbox check: too few arguments")
	}
	opt := func(s string) string {
		if s == none {
			return ""
		}
		return s
	}
	t := Target{Nonce: args[0], ClientDir: opt(args[1]), Sock: opt(args[2]), Key: opt(args[3]), PidNS: opt(args[4])}
	// Paths are absolute, so never one of the separators.
	var into *[]string
	for _, a := range args[5:] {
		switch {
		case a == writesSep && into == nil:
			into = &t.Writes
		case a == warnsSep && (into == nil || into == &t.Writes):
			into = &t.Warns
		case into != nil:
			*into = append(*into, a)
		default:
			p, err := strconv.Atoi(a)
			if err != nil || p <= 0 {
				return Target{}, fmt.Errorf("sandbox check: bad process id %q", a)
			}
			t.Pids = append(t.Pids, p)
		}
	}
	return t, nil
}

// Result is what the check reached. Client and Holder are the ways in it
// found, empty when it found none.
type Result struct {
	// Client: "folder" (it could see into the socket's folder), "socket"
	// (connect to it) and "key" (read the key).
	Client []string `json:"client,omitempty"`
	// Holder: "pidfd_getfd", "ptrace" and "/proc", each with the process
	// it reached.
	Holder []string `json:"holder,omitempty"`
	// Writes are those of Target's Writes the check could write.
	Writes []string `json:"writes,omitempty"`
	// Warns are those of Target's Warns it could write. A check of a
	// nerdr from before them answers without any.
	Warns []string `json:"warns,omitempty"`
}

// Reached reports whether the check got to anything it must not.
func (r Result) Reached() bool { return len(r.Client) > 0 || len(r.Holder) > 0 }

// Main runs the check, inside the sandbox, and prints what it found on
// the line Parse reads: the exit status is always 0, for a sandbox that
// passes on only some.
func Main(args []string) int {
	t, err := ParseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	r := Probe(t)
	b, _ := json.Marshal(r)
	fmt.Printf("\r\n%s %s %s\r\n", marker, t.Nonce, b)
	// A terminal may not pass on the last of what was written before it
	// is gone.
	time.Sleep(100 * time.Millisecond)
	return 0
}

// Probe tries each way to t, from where it runs.
func Probe(t Target) Result {
	var r Result
	if seesInto(t.ClientDir, t.Sock) {
		r.Client = append(r.Client, "folder")
	}
	if t.Sock != "" {
		if c, err := net.DialTimeout("unix", t.Sock, 2*time.Second); err == nil {
			_ = c.Close()
			r.Client = append(r.Client, "socket")
		}
	}
	if t.Key != "" {
		if b, err := os.ReadFile(t.Key); err == nil && len(b) > 0 {
			r.Client = append(r.Client, "key")
		}
	}
	r.Holder = probeHolder(t)
	for _, p := range t.Writes {
		if canWrite(p, t.Nonce) {
			r.Writes = append(r.Writes, p)
		}
	}
	for _, p := range t.Warns {
		if canWrite(p, t.Nonce) {
			r.Warns = append(r.Warns, p)
		}
	}
	return r
}

// canWrite reports whether path can be written from here: a folder by
// making a file in it, which is removed again, a file by opening it to
// append to, with nothing written. One that is not there cannot.
func canWrite(path, nonce string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !st.IsDir() {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return false
		}
		_ = f.Close()
		return true
	}
	probe := filepath.Join(path, ".nerdr-sandbox-check-"+filepath.Base(nonce))
	f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

// seesInto reports whether the folder dir, the client socket's, can be
// looked into from here: whether sock in it is there to see, or listed.
// A folder of the same name the sandbox put in its place, empty, is not
// it.
func seesInto(dir, sock string) bool {
	if sock != "" {
		if _, err := os.Lstat(sock); err == nil {
			return true
		}
	}
	if dir == "" {
		return false
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(names, func(e os.DirEntry) bool { return e.Name() == filepath.Base(sock) })
}

// samePidNS reports whether this process is in the pid namespace ns, the
// core's; true when either is not known, for the processes to be tried.
func samePidNS(ns string) bool {
	if ns == "" {
		return true
	}
	own, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return true
	}
	return own == ns
}

// PtraceOpen reports whether Yama lets a process trace any other of its
// user's (kernel.yama.ptrace_scope is 0): then only a sandbox keeps an
// agent from the holder.
func PtraceOpen() bool {
	b, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
	return err == nil && strings.TrimSpace(string(b)) == "0"
}

// PidNS is this process's pid namespace, "" when it cannot tell.
func PidNS() string {
	ns, _ := os.Readlink("/proc/self/ns/pid")
	return ns
}

// Parse finds the answer of the check that had nonce in what it printed;
// ok is false when there is none: it did not run, or did not get far.
func Parse(out []byte, nonce string) (r Result, ok bool) {
	head := marker + " " + nonce + " "
	i := strings.Index(string(out), head)
	if i < 0 {
		return Result{}, false
	}
	rest := string(out[i+len(head):])
	if j := strings.IndexAny(rest, "\r\n"); j >= 0 {
		rest = rest[:j]
	}
	if err := json.Unmarshal([]byte(rest), &r); err != nil {
		return Result{}, false
	}
	return r, true
}

// Wrapper is what of command, an agent's, comes before the agent's
// program, one of names, so that the check runs as the agent would: the
// sandbox and what it is given. ok is false when no word of command is
// one of names: nerdr cannot tell what part of it is the sandbox. A
// command that is only the program has no wrapper, and the check runs
// as the agent does, outside any sandbox.
func Wrapper(command []string, names ...string) (wrapper []string, ok bool) {
	for i := len(command) - 1; i >= 0; i-- {
		if slices.Contains(names, filepath.Base(command[i])) {
			return slices.Clone(command[:i]), true
		}
	}
	return nil, false
}

// Argv is the check's command line: wrapper, nerdr at bin, and t.
func Argv(wrapper []string, bin string, t Target) []string {
	out := append(slices.Clone(wrapper), bin, Arg)
	return append(out, t.Args()...)
}
