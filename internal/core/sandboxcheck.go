package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/spawn"
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
// what it reached, or, when OK is false, why it could not tell.
type SandboxCheckedMsg struct {
	Kind   agents.Kind
	Result sandboxcheck.Result
	OK     bool
	Why    string
}

// CheckSandbox has nerdr look from inside t's sandbox, t having just
// started in one, at whether it reaches the client socket or the holder:
// it runs the check through t's command, with nerdr in place of the
// agent, the environment env and in t's folder, as the agent runs. Each
// kind and command is checked once while the core runs; nothing is
// checked without a holder, when there is no client socket. The agent
// is not held back for it: the user is warned (see OnSandboxChecked).
// m.Mu is held; the check runs away from it.
func (m *Core) CheckSandbox(t *Tab, env []string) Cmd {
	if m.Held == nil || !t.InSandbox {
		return nil
	}
	kind := t.Agent
	command, _ := agents.Command(m.SpecsFor(t), kind)
	key := string(kind) + "\x00" + strings.Join(command, "\x00")
	if m.SandboxChecks[key] {
		return nil
	}
	if m.SandboxChecks == nil {
		m.SandboxChecks = map[string]bool{}
	}
	m.SandboxChecks[key] = true
	unknown := func(why string) Cmd {
		return func() Event { return SandboxCheckedMsg{Kind: kind, Why: why} }
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
	}
	spec := spawn.Spec{Argv: sandboxcheck.Argv(wrapper, bin, target), Dir: t.Dir, Env: env, PTY: true, W: 120, H: 24}
	m.Hold(&spec, Label{Kind: HeldCheck, Tab: t.ID}, true, "")
	progs := m.Programs()
	return func() Event {
		out, err := RunCheck(progs, spec, SandboxCheckWait)
		if err != nil {
			return SandboxCheckedMsg{Kind: kind, Why: "it could not start: " + err.Error()}
		}
		r, ok := sandboxcheck.Parse(out, target.Nonce)
		if !ok {
			return SandboxCheckedMsg{Kind: kind, Why: "the check did not answer: the sandbox may not let it run " + bin}
		}
		return SandboxCheckedMsg{Kind: kind, Result: r, OK: true}
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
// holder, or could not tell. With Yama's ptrace_scope at 0 the kernel
// does not stop a process of the user's from tracing the holder, and only
// the sandbox can: that is said too, unless the check found it does.
func (m *Core) OnSandboxChecked(ev SandboxCheckedMsg) {
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
		note = fmt.Sprintf("%s's sandbox lets it reach %s. The sandbox must deny them; %s",
			ev.Kind, strings.Join(parts, ", and "), docs)
	default:
		Logf("sandbox check: %s's sandbox keeps it from the client socket and the holder", ev.Kind)
		return
	}
	Logf("sandbox check: %s", note)
	m.Tell(NoticeMsg(note))
}
