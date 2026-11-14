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
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/statedir"
	"github.com/nerdr-dev/nerdr/skills"
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

// Refused reports whether the agent is not to start: the check found its
// sandbox lets it connect to the client socket or read its key, reach
// the holder or write critical paths, or could not be checked. Seeing
// into the client socket's folder alone is only warned of.
func (ev SandboxCheckedMsg) Refused() bool {
	return !ev.OK || ev.Result.Reached() || len(ev.Result.Writes) > 0
}

// CheckSandbox checks access to the client socket and holder before every
// sandboxed launch. The wrapper runs nerdr in place of the agent: what of
// the agent's command comes before its program, or the agent's
// check_command, for a wrapper that picks the agent some other way (an
// option, or the agent by default). That one may run nerdr under another
// policy than the agent's; it is the user's to say it does not. Unknown
// results refuse the launch too: native tool sandboxes cannot be verified
// by running a probe outside them. Results are not cached because the
// wrapper's policy can depend on the folder, environment or mutable files.
// m.Mu is held; the returned check runs away from it.
func (m *Core) CheckSandbox(t *Tab, env []string) func() SandboxCheckedMsg {
	if !m.SandboxedKind(t.Agent) {
		return nil
	}
	kind := t.Agent
	specs := m.SpecsFor(t)
	command, _ := agents.Command(specs, kind)
	unknown := func(why string) func() SandboxCheckedMsg {
		return func() SandboxCheckedMsg { return SandboxCheckedMsg{Kind: kind, Why: why} }
	}
	wrapper := slices.Clone(specs[kind].CheckCommand)
	if len(wrapper) == 0 {
		names := []string{string(kind)}
		if d := agents.DefaultSpec(kind).Command; len(d) > 0 {
			names = append(names, filepath.Base(d[0]))
		}
		var ok bool
		wrapper, ok = sandboxcheck.Wrapper(command, names...)
		if !ok {
			return unknown(fmt.Sprintf("its command (%s) names no %s to run the check in place of; "+
				"set check_command to a command that runs the program after it in the same sandbox",
				strings.Join(command, " "), strings.Join(names, " or ")))
		}
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
		Pids:      []int{os.Getpid()},
		Writes:    SandboxWrites(m, t),
		Warns:     SandboxWarns(m, t),
	}
	noAnswer := "the check did not answer: the sandbox may not let it run " + bin
	if len(specs[kind].CheckCommand) > 0 {
		noAnswer += ", or check_command does not run the program it is given"
	}
	if m.Held != nil {
		target.Pids = append(target.Pids, m.Held.Client.Pid())
	}
	spec := spawn.Spec{Argv: sandboxcheck.Argv(wrapper, bin, target), Dir: t.Dir, Env: env, PTY: true, W: 120, H: 24}
	m.Hold(&spec, Label{Kind: HeldCheck, Tab: t.ID}, true, "")
	progs := m.Programs()
	return func() SandboxCheckedMsg {
		out, err := RunCheck(progs, spec, SandboxCheckWait)
		if err != nil {
			return SandboxCheckedMsg{Kind: kind, Why: "it could not start: " + err.Error()}
		}
		r, ok := sandboxcheck.Parse(out, target.Nonce)
		if !ok {
			return SandboxCheckedMsg{Kind: kind, Why: noAnswer}
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

// OnSandboxChecked is why a sandboxed launch is refused, "" when it is
// not, for the tab to show until the agent is started again: what the
// check reached, by path, or why it could not tell. It is logged, and
// writable sensitive paths are told of. Every launch is checked again,
// including after an unknown result, so fixing the sandbox needs no core
// restart.
func (m *Core) OnSandboxChecked(ev SandboxCheckedMsg) string {
	why := m.sandboxRefusal(ev)
	if why != "" {
		Logf("sandbox check: %s not started: %s", ev.Kind, why)
	} else if ev.OK {
		Logf("sandbox check: %s's sandbox keeps it from the client socket and the holder", ev.Kind)
	}
	m.sandboxWarn(ev)
	return why
}

// sandboxRefusal is why ev refuses its agent, "" when it does not. It
// names the client socket's folder, socket and key as nerdr has them, for
// the sandbox's rule to deny, and the folder's real path too, when a link
// leads there: the sandbox may go by that one.
func (m *Core) sandboxRefusal(ev SandboxCheckedMsg) string {
	sock, key, _ := attach.Paths(m.Root)
	dir := filepath.Dir(sock)
	folder := quoted(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		folder += " (" + quoted(real) + " once its links are followed)"
	}
	const scope = "kernel.yama.ptrace_scope is 0, so only the sandbox stops a process of yours from tracing nerdr's holder"
	if !ev.OK {
		why := fmt.Sprintf("it could not check that its sandbox denies nerdr's client socket, in the folder %s: %s. Configure a sandbox wrapper that can run the check; %s",
			folder, ev.Why, sandboxDocs)
		if ptraceOpen() {
			why += "; also, " + scope
		}
		return why
	}
	if !ev.Result.Reached() && len(ev.Result.Writes) == 0 {
		return ""
	}
	var parts []string
	if len(ev.Result.Client) > 0 {
		var ways []string
		for _, w := range ev.Result.Client {
			switch w {
			case "folder":
				ways = append(ways, "see into the folder "+quoted(dir))
			case "socket":
				ways = append(ways, "connect to "+quoted(sock))
			case "key":
				ways = append(ways, "read "+quoted(key))
			default:
				ways = append(ways, w)
			}
		}
		parts = append(parts, fmt.Sprintf("nerdr's client socket, which can do anything you can: it could %s. The sandbox must deny the folder %s",
			strings.Join(ways, ", "), folder))
	}
	if len(ev.Result.Holder) > 0 {
		h := fmt.Sprintf("nerdr's holder or core, which run every program (%s)", strings.Join(ev.Result.Holder, ", "))
		if ptraceOpen() {
			h += "; " + scope
		}
		parts = append(parts, h)
	}
	if len(ev.Result.Writes) > 0 {
		parts = append(parts, "critical writable paths ("+strings.Join(ev.Result.Writes, ", ")+")")
	}
	return fmt.Sprintf("its sandbox lets it reach %s. The sandbox must deny them; %s", strings.Join(parts, "; and "), sandboxDocs)
}

// sandboxDocs says where nerdr's docs say how to set a sandbox up.
const sandboxDocs = `see "Agents in a sandbox" in nerdr's docs`

// quoted is path in quotes, for one with spaces in it to be told apart
// from the words around it.
func quoted(path string) string { return "\"" + path + "\"" }

// sandboxWarn tells of what ev's sandbox lets its agent do that does not
// keep it from starting: see into the client socket's folder, and write
// where nerdr installs for every agent.
func (m *Core) sandboxWarn(ev SandboxCheckedMsg) {
	if !ev.OK {
		return
	}
	// Once for each kind of agent, until what it is warned of changes: an
	// agent that starts often would say the same each time. The folder
	// only of one that starts: a refusal names it already.
	folder := ev.Result.SeesFolder() && !ev.Refused()
	warned := strings.Join(ev.Result.Warns, "\x00")
	if folder {
		warned = "folder\x01" + warned
	}
	if m.sandboxWarned[ev.Kind] == warned {
		return
	}
	if m.sandboxWarned == nil {
		m.sandboxWarned = map[agents.Kind]string{}
	}
	m.sandboxWarned[ev.Kind] = warned
	if len(ev.Result.Warns) > 0 {
		note := fmt.Sprintf("%s's sandbox lets it write where nerdr installs its skills, plugins and hooks for every agent (%s): an agent in a sandbox can change what agents outside one load from there. Set install = \"worktree\" in nerdr's config, or have the sandbox deny writing them; %s",
			ev.Kind, strings.Join(ev.Result.Warns, ", "), sandboxDocs)
		Logf("sandbox check: %s", note)
		m.Tell(NoticeMsg(note))
	}
	if folder {
		sock, _, _ := attach.Paths(m.Root)
		note := fmt.Sprintf("%s's sandbox lets it see into nerdr's client socket's folder (%s): it can tell what is there, though not connect to the socket or read its key. If the sandbox can deny the folder's metadata and listing too, have it do so; %s",
			ev.Kind, quoted(filepath.Dir(sock)), sandboxDocs)
		Logf("sandbox check: %s", note)
		m.Tell(NoticeMsg(note))
	}
}

// SandboxWarns is what the check is to try to write and only warn of;
// the tests keep it from the user's own home folder.
var SandboxWarns = (*Core).sandboxWarns

// sandboxWarns are the folders and files nerdr installs its skills,
// plugins and hooks in in the home folder, when the install setting is
// "home", for the agents there are: every agent nerdr starts loads them,
// one outside a sandbox too. A sandbox that lets its agent write them is
// warned of, not refused: they are the agents' own folders, which many
// sandboxes give their agent. For a file, its folder too, in which it can
// be replaced. Those not there are not tried, nor made.
//
// Not copilot's plugin: nerdr gives copilot its own copy, in the state
// folder or the worktree (see CopilotPlugin), nor codex's config, which
// codex reads whatever the setting.
func (m *Core) sandboxWarns(*Tab) []string {
	if m.Cfg.Install != config.InstallHome {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	kinds := m.Available()
	var out []string
	for _, tg := range skills.Home(home, kinds) {
		out = append(out, tg.Dir)
	}
	var files []string
	if slices.Contains(kinds, agents.OpenCode) {
		files = append(files, hooks.OpenCodePluginHome(home))
	}
	if slices.Contains(kinds, agents.Antigravity) {
		files = append(files, hooks.AgyHooksHome(home))
	}
	if slices.Contains(kinds, agents.Grok) {
		files = append(files, hooks.GrokHooksHome(home))
	}
	for _, f := range files {
		out = append(out, f, filepath.Dir(f))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SandboxWrites is what the check is to try to write; the tests keep it
// from the user's own config folder.
var SandboxWrites = (*Core).sandboxWrites

// sandboxWrites are the folders and files t's sandbox must keep its agent
// from writing, for the check to try: nerdr's config and state, the
// .git/config and .git/hooks of the main checkouts of t's worktrees, and
// the folder the tabs' worktrees are in, when t's own is in a folder of
// its own there.
//
// The state folder's temporary folder is tried by itself, and made first,
// the check passing by what is not there: a sandbox may deny the state
// folder and still allow a folder in it, and nerdr checks a new worktree's
// files out from a store in that one (see statedir).
func (m *Core) sandboxWrites(t *Tab) []string {
	out := []string{filepath.Dir(config.Path()), filepath.Dir(state.Dir())}
	if tmp, err := statedir.EnsureTempDir(); err == nil {
		out = append(out, tmp)
	}
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
