package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// SetHookSocket gives the agents the socket their integrations report to.
func (m *Core) SetHookSocket(path string) {
	m.HookSock = path
	if exe, err := spawn.Bin(); err == nil {
		m.HookBin = exe
	}
}

// CodexHookArgs are the arguments that give codex nerdr's hooks, or none
// while codex does not trust them: an untrusted hook stops codex at start
// to ask about it. codex is asked once; where the skills go in the home
// folder, applySkills has it trust them.
//
// Asking runs codex, which can take seconds, so it is done away from Mu
// (see SpawnPrep), and none are given while it has not been.
func (m *Core) CodexHookArgs() []string {
	if !m.CodexAsked {
		Debugf("codex hooks: codex not asked yet whether it trusts them")
	}
	return m.CodexHooks
}

// TrustCodexHooks asks codex whether it trusts nerdr's hooks, and with
// write has it trust them, and reports whether that wrote its config.
// It runs codex: not with m.Mu held, outside the tests.
func (m *Core) TrustCodexHooks(write bool) (bool, error) {
	ask := m.AskCodexTrust(write)
	trusted, wrote, err := ask()
	m.SetCodexTrust(trusted)
	return wrote, err
}

// AskCodexTrust is how to ask codex, as the config runs it, whether it
// trusts nerdr's hooks, with m.Mu let go; SetCodexTrust takes the answer.
// m.Mu is held.
func (m *Core) AskCodexTrust(write bool) func() (trusted, wrote bool, err error) {
	argv, env := agents.Command(m.Cfg.Agents, agents.Codex)
	env = WithUserEnv(m.BaseEnv(), env)
	return func() (bool, bool, error) { return TrustCodex(argv, env, write) }
}

// SetCodexTrust takes codex's answer to whether it trusts nerdr's hooks.
// m.Mu is held.
func (m *Core) SetCodexTrust(trusted bool) {
	m.CodexAsked = true
	m.CodexHooks = nil
	if trusted {
		m.CodexHooks = hooks.CodexArgs()
	}
}

// CopilotPlugin is the folder of nerdr's plugin for t's copilot, where the
// install setting put it, or "" for none.
func (m *Core) CopilotPlugin(t *Tab) string {
	var dir string
	switch m.Cfg.Install {
	case config.InstallHome:
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = hooks.CopilotPluginHome(home)
	case config.InstallWorktree:
		if t.InSandbox {
			dir = hooks.CopilotPluginProject(t.Dir)
		} else {
			// A copilot outside a sandbox must not be given --plugin-dir
			// in an agent-writable worktree, where a sandboxed agent can
			// add files or replace hooks.json after a check; use nerdr's
			// own copy in its state folder instead.
			dir = filepath.Join(filepath.Dir(state.Dir()), "copilot-plugin")
			if _, err := hooks.InstallCopilotPlugin(dir); err != nil {
				return ""
			}
		}
	default:
		return ""
	}
	if !hooks.HasCopilotPlugin(dir) {
		return ""
	}
	return dir
}

// TrustCodex is hooks.TrustCodexHooks; the tests keep the user's codex
// out of it.
var TrustCodex = hooks.TrustCodexHooks

// NestedSession reports whether r is the start of a conversation that is
// not the tab's: a `claude -p` the agent runs inherits the tab's
// environment, and herdr's integration in it reports its start to nerdr.
// The tab's own agent starts a new conversation with clear or resume, or
// with startup only before its first is known. nerdr's own hooks are not
// inherited (they come on the command line, or ask for a variable only
// the tab's agent gets), so theirs is the tab's.
func NestedSession(r hooks.Report, t *Tab) bool {
	return r.SessionSource == "startup" && t.SessionID != "" && !strings.HasPrefix(r.Source, "nerdr:")
}

// CutOff says why nothing t ran has reached nerdr, when that is the likely
// reason for it: t runs in a sandbox, was started with an integration that
// reports to nerdr as it works, and has ended a turn with none of it come.
// Its integration and nerdr's commands in it run the same nerdr, through
// the same socket, so neither works: the sandbox drops NERDR_BIN, or keeps
// the agent from running nerdr or from its socket. "" when t was heard.
func (m *Core) CutOff(t *Tab) string {
	if !t.InSandbox || !t.Reports || t.Heard {
		return ""
	}
	bin := m.HookBin
	if bin == "" {
		bin = "nerdr"
	}
	why := "nothing it ran reached nerdr, so it can neither report to nerdr nor use its commands (send, ready, review). " +
		"Its sandbox must let the NERDR_* variables through, NERDR_BIN among them, allow running " + bin
	if m.HookSock != "" {
		why += " and allow connecting to " + m.HookSock
	}
	return why + "; then restart the agent. See \"Agents in a sandbox\" in nerdr's docs"
}

// SetHookSocketError is why nerdr has no socket, shown now and given to
// the agents, so that nerdr's commands in them say so rather than blame a
// sandbox. Another nerdr having it is the likely one.
func (m *Core) SetHookSocketError(err error) {
	m.HookSockErr = err.Error()
	if errors.Is(err, hooks.ErrTaken) {
		m.HookSockErr += "; quit that nerdr and restart this one, or its tabs cannot ask, try, review, spin off or hand off"
	}
	m.Tell(NoticeMsg("no socket for the agents: " + m.HookSockErr))
}

// SetSocketShared says another nerdr has the socket a sandbox is told
// of, so this one's agents report to one of its own (see hooks.ForRoot):
// all works but for an agent in a sandbox that lets it reach only the
// usual socket.
func (m *Core) SetSocketShared(taken error) {
	note := taken.Error() + ": this nerdr's agents report to " + m.HookSock + " instead"
	for _, s := range m.Cfg.Agents {
		if s.Sandboxed {
			note += "; an agent in a sandbox reaches this nerdr only if its sandbox lets it reach that socket too"
			break
		}
	}
	m.Tell(NoticeMsg(note))
}

// agentEnv is the environment an agent runs with: childEnv, the socket
// nerdr's own commands report to, and herdr's variables pointing its
// integrations at nerdr. The tab's id stands for herdr's pane id.
func (m *Core) AgentEnv(t *Tab) []string {
	env := m.TabEnv(t)
	sock, sockErr := m.HookSock, m.HookSockErr
	if t.InSandbox {
		env = append(env, "NERDR_SANDBOXED=1")
	}
	if t.Agent == agents.OpenCode {
		// For nerdr's plugin, which adds it to opencode's system prompt.
		if note := m.AgentNote(t); note != "" {
			env = append(env, "NERDR_OPENCODE_NOTE="+note)
		}
	}
	if sock == "" {
		if sockErr != "" {
			env = append(env, "NERDR_NO_SOCKET="+sockErr)
		}
		return env
	}
	env = append(env,
		"NERDR_SOCKET="+sock,
		"HERDR_ENV=1",
		"HERDR_PANE_ID="+t.HookKey(),
		"HERDR_SOCKET_PATH="+sock,
	)
	if m.HookBin != "" {
		env = append(env, "HERDR_BIN_PATH="+m.HookBin)
	}
	if t.Agent == agents.OpenCode {
		// For nerdr's plugin, which reports for this opencode alone: it
		// takes the variable out, so another opencode run in the tab
		// does not report as the tab's agent. See hooks.OpenCodePlugin.
		env = append(env, "NERDR_OPENCODE_HOOK=1")
	}
	if t.Agent == agents.Antigravity {
		// For nerdr's hooks, which report for an agy whose parent is the
		// process that starts the programs: the one nerdr starts, not one
		// that agent runs. See hooks.AgyCommand.
		env = append(env, "NERDR_AGY_HOOK="+strconv.Itoa(m.ProgramsParent()))
	}
	if t.Agent == agents.Grok {
		// The same for nerdr's hooks in grok. See hooks.GrokCommand.
		env = append(env, "NERDR_GROK_HOOK="+strconv.Itoa(m.ProgramsParent()))
	}
	return env
}

// ProgramsParent is the process the tabs' programs are children of: the
// holder, when nerdr runs over one, else this one.
func (m *Core) ProgramsParent() int {
	if m.Held != nil {
		return m.Held.Client.Pid()
	}
	return os.Getpid()
}

// onHook takes a report from an integration.
func (m *Core) OnHook(r hooks.Report) Cmd {
	t := m.HookSession(r)
	if t == nil {
		return nil
	}
	switch r.Method {
	case hooks.ReportAgent:
		if r.State == "" {
			return nil
		}
		t.Hook.Source = r.Source
		t.Hook.Msg = r.Message
		t.Hook.At = time.Now()
		t.Hook.Background = r.Background
		t.Hook.Processes = r.Processes
		switch r.State {
		case "working":
			t.Tasked = true
			t.Hook.State = detect.Working
			if r.Processes == 0 {
				t.Hook.WorkingAt = t.Hook.At
			}
		case "blocked":
			t.Hook.State = detect.Blocked
		case "idle":
			t.Hook.State = detect.Idle
		default:
			t.Hook.State = detect.Unknown
		}
		return m.DetectAll(time.Now()) // at once: a hook is news the screen may not show yet
	case hooks.ReleaseAgent:
		if r.Source == t.Hook.Source {
			t.Hook.Source, t.Hook.Msg = "", ""
		}
	}
	return nil
}

// maxHookSources bounds sequence bookkeeping even for short, unique sources.
const maxHookSources = 32

// hookSession takes the conversation report r says its agent is in, and
// returns the agent, or nil when the report is not one to take: for no
// agent running, another agent than it, or older than one taken. It is
// all of a report that counts while nerdr quits: the conversation to
// resume next time.
func (m *Core) HookSession(r hooks.Report) *Tab {
	if len(r.Source) > hooks.MaxSource {
		return nil
	}
	t := m.AgentByHook(r.Pane)
	if t == nil || t.Pane == nil {
		return nil
	}
	// A report for another agent than the tab's (a subagent, a tool the
	// agent ran) is not about the tab.
	if r.Agent != "" && r.Agent != t.AgentID() {
		return nil
	}
	if t.Hook.Seq == nil {
		t.Hook.Seq = map[string]int64{}
	}
	if r.Seq > 0 {
		last, known := t.Hook.Seq[r.Source]
		if !known && len(t.Hook.Seq) >= maxHookSources {
			return nil
		}
		if r.Seq <= last {
			return nil // out of order: an older report arriving late
		}
		t.Hook.Seq[r.Source] = r.Seq
	}
	// Asked once, of the tab as it was before the report: taking its
	// conversation below would make the rest of it look nested.
	if NestedSession(r, t) {
		return t
	}
	if r.SessionID != "" && r.SessionID != t.SessionID && agents.ValidSessionID(r.SessionID) {
		// The conversation the agent is really in — after /clear too — so
		// a restart resumes this one, not a guess. A command reported for
		// the one before reopens that one: it goes, unless this report
		// brings the new one's.
		t.ResumeArgv = nil
		m.SetSession(t, r.SessionID)
	}
	if len(r.ResumeArgv) > 0 && !m.Confined(r, t) && !slices.Equal(r.ResumeArgv, t.ResumeArgv) {
		t.ResumeArgv = r.ResumeArgv
		m.SaveSession()
	}
	return t
}

// tellCutOff tells the user, once for the agent's run, that nothing t ran
// has reached nerdr; see cutOff.
func (m *Core) TellCutOff(t *Tab) {
	if why := m.CutOff(t); why != "" && !t.ToldCutOff {
		t.ToldCutOff = true
		m.Tell(NoticeMsg(m.AgentName(t) + ": " + why))
	}
}
