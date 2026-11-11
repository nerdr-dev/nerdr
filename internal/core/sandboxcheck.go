package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// HeldCheck is the kind of held program that is a check from inside a
// sandbox: nothing takes it up again.
const HeldCheck = "sandbox-check"

// SandboxCheckWait is how long a check from inside a sandbox has to
// answer, a sandbox that starts slowly included.
var SandboxCheckWait = 30 * time.Second

// ptraceOpen is sandboxcheck.PtraceOpen, for tests to set.
var ptraceOpen = sandboxcheck.PtraceOpen

// SandboxCheckedMsg is how the check from inside kind's sandbox went:
// what it reached, or, when OK is false, why it could not tell. Key is
// the kind and command checked.
type SandboxCheckedMsg struct {
	Kind   agents.Kind
	Key    string
	Result sandboxcheck.Result
	OK     bool
	Why    string
}

// Refused reports whether the agent is not to start: the check found its
// sandbox lets it reach the client socket or the holder.
func (ev SandboxCheckedMsg) Refused() bool { return ev.OK && ev.Result.Reached() }

// CheckSandbox is how nerdr looks from inside t's sandbox, before t starts
// in one, at whether it reaches the client socket or the holder: it runs
// the check through t's command, with nerdr in place of the agent, the
// environment env and in t's folder, as the agent runs. nil when there is
// nothing to check: t is not sandboxed, there is no holder, and so no
// client socket, or its kind and command were checked since the core
// started and reached nothing. One that reached either is checked again
// at each start, so a sandbox put right is found so at once. An agent the
// check finds can reach them is not started; one nerdr could not check
// starts, with a warning (see OnSandboxChecked). m.Mu is held; the check
// runs away from it.
func (m *Core) CheckSandbox(t *Tab, env []string) func() SandboxCheckedMsg {
	if m.Held == nil || !m.SandboxedKind(t.Agent) {
		return nil
	}
	kind := t.Agent
	command, _ := agents.Command(m.SpecsFor(t), kind)
	key := string(kind) + "\x00" + strings.Join(command, "\x00")
	if m.SandboxChecks[key] {
		return nil
	}
	unknown := func(why string) func() SandboxCheckedMsg {
		return func() SandboxCheckedMsg { return SandboxCheckedMsg{Kind: kind, Key: key, Why: why} }
	}
	names := []string{string(kind)}
	if d := agents.DefaultSpec(kind).Command; len(d) > 0 {
		names = append(names, filepath.Base(d[0]))
	}
	wrapper, ok := sandboxcheck.Wrapper(command, names...)
	if !ok {
		return unknown(fmt.Sprintf("its command (%s) names no %s to run the check in place of", strings.Join(command, " "), strings.Join(names, " or ")))
	}
	bin, err := spawn.Bin()
	if err != nil {
		return unknown("nerdr cannot tell where it is: " + err.Error())
	}
	sock, keyFile, err := attach.Paths(m.Root)
	if err != nil {
		return unknown(err.Error())
	}
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	target := sandboxcheck.Target{
		Nonce:     hex.EncodeToString(nonce[:]),
		ClientDir: filepath.Dir(sock),
		Sock:      sock,
		Key:       keyFile,
		PidNS:     sandboxcheck.PidNS(),
		Pids:      []int{m.Held.Client.Pid(), os.Getpid()},
		Writes:    SandboxWrites(m, t),
	}
	spec := spawn.Spec{Argv: sandboxcheck.Argv(wrapper, bin, target), Dir: t.Dir, Env: env, PTY: true, W: 120, H: 24}
	m.Hold(&spec, Label{Kind: HeldCheck, Tab: t.ID}, true, "")
	progs := m.Programs()
	return func() SandboxCheckedMsg {
		out, err := RunCheck(progs, spec, SandboxCheckWait)
		if err != nil {
			return SandboxCheckedMsg{Kind: kind, Key: key, Why: "it could not start: " + err.Error()}
		}
		r, ok := sandboxcheck.Parse(out, target.Nonce)
		if !ok {
			return SandboxCheckedMsg{Kind: kind, Key: key, Why: "the check did not answer: the sandbox may not let it run " + bin}
		}
		return SandboxCheckedMsg{Kind: kind, Key: key, Result: r, OK: true}
	}
}

// RunCheck starts s with progs and is what it printed, once it has
// exited or wait has gone by, when it is ended.
func RunCheck(progs spawn.Spawner, s spawn.Spec, wait time.Duration) ([]byte, error) {
	p, err := progs.Spawn(s)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = io.Copy(&out, io.LimitReader(p, 1<<20))
	}()
	exited := make(chan struct{})
	go func() {
		_ = p.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(wait):
		_ = p.Signal(p.Pid(), syscall.SIGKILL)
		<-exited
	}
	// What it printed last may still be on its way.
	select {
	case <-read:
	case <-time.After(time.Second):
	}
	_ = p.Close()
	<-read
	return out.Bytes(), nil
}

// OnSandboxChecked warns the user, by a notice and in the log, when
// the check from inside kind's sandbox reached the client socket or the
// holder, and the agent is not started, or could not tell. A check that
// reached neither is not run again for the kind and command. With Yama's ptrace_scope at 0 the kernel
// does not stop a process of the user's from tracing the holder, and only
// the sandbox can: that is said too, unless the check found it does.
func (m *Core) OnSandboxChecked(ev SandboxCheckedMsg) {
	if !ev.Refused() && ev.Key != "" {
		if m.SandboxChecks == nil {
			m.SandboxChecks = map[string]bool{}
		}
		m.SandboxChecks[ev.Key] = true
	}
	sock, _, _ := attach.Paths(m.Root)
	dir := filepath.Dir(sock)
	const docs = `see "Agents in a sandbox" in nerdr's docs`
	const scope = "kernel.yama.ptrace_scope is 0, so only the sandbox stops a process of yours from tracing nerdr's holder"
	var note string
	switch {
	case !ev.OK:
		note = fmt.Sprintf("nerdr could not check from inside %s's sandbox that it keeps the agent from nerdr's client socket, in %s: %s. Check it yourself; %s",
			ev.Kind, dir, ev.Why, docs)
		if ptraceOpen() {
			note += "; also, " + scope
		}
	case ev.Result.Reached():
		var parts []string
		if len(ev.Result.Client) > 0 {
			parts = append(parts, fmt.Sprintf("nerdr's client socket, which can do anything you can (%s, in %s)",
				strings.Join(ev.Result.Client, ", "), dir))
		}
		if len(ev.Result.Holder) > 0 {
			h := fmt.Sprintf("nerdr's holder or core, which run every program (%s)", strings.Join(ev.Result.Holder, ", "))
			if ptraceOpen() {
				h += "; " + scope
			}
			parts = append(parts, h)
		}
		note = fmt.Sprintf("nerdr did not start %s: its sandbox lets it reach %s. The sandbox must deny them; %s",
			ev.Kind, strings.Join(parts, ", and "), docs)
	default:
		Logf("sandbox check: %s's sandbox keeps it from the client socket and the holder", ev.Kind)
	}
	if note != "" {
		Logf("sandbox check: %s", note)
		m.Tell(NoticeMsg(note))
	}
	if ev.OK && !ev.Result.Reached() && len(ev.Result.Writes) > 0 {
		// The agent is started all the same: a sandbox that was enough
		// before nerdr looked is not turned off under the user, but they
		// are told what it lets through. One that is not started is told
		// of when it is.
		w := fmt.Sprintf("%s's sandbox lets it write %s. Through nerdr's config and state it can allow itself a plugin or a setup command, "+
			"through a main checkout's .git/config and .git/hooks have git run a command, and in the folder of the other tabs' worktrees "+
			"change the code other agents and setup commands run: all as you, outside the sandbox. The sandbox should deny them; %s",
			ev.Kind, strings.Join(ev.Result.Writes, ", "), docs)
		Logf("sandbox check: %s", w)
		m.Tell(NoticeMsg(w))
	}
}

// SandboxWrites is what the check is to try to write; the tests keep it
// from the user's own config folder.
var SandboxWrites = (*Core).sandboxWrites

// sandboxWrites are the folders and files t's sandbox must keep its agent
// from writing, for the check to try: nerdr's config and state, the
// .git/config and .git/hooks of the main checkouts of t's worktrees, and
// the folder the tabs' worktrees are in, when t's own is in a folder of
// its own there.
func (m *Core) sandboxWrites(t *Tab) []string {
	out := []string{filepath.Dir(config.Path()), filepath.Dir(state.Dir())}
	for _, w := range t.Owner().Worktrees {
		if w.Repo != "" {
			out = append(out, filepath.Join(w.Repo, ".git", "config"), filepath.Join(w.Repo, ".git", "hooks"))
		}
	}
	if root := m.WtRoot; root != "" && !SamePath(root, t.Dir) && !SamePath(root, filepath.Dir(root)) {
		if rel, err := filepath.Rel(root, t.Dir); err == nil && filepath.IsLocal(rel) {
			out = append(out, root)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
