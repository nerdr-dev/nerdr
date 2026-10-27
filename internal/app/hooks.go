package app

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
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

// herdr's agent integrations, when installed, report to nerdr (see
// internal/hooks): the exact conversation a tab's agent is in, and for some
// agents its state, which then counts for more than what the screen shows.

// SetHookSocket gives the agents the socket their integrations report to.
func (m *Model) SetHookSocket(path string) {
	m.hookSock = path
	if exe, err := os.Executable(); err == nil {
		m.hookBin = exe
	}
}

// Notice shows a message for longer than a toast: for what went wrong
// before the screen was up.
func (m *Model) Notice(s string) {
	m.toastText = s
	m.toastUntil = time.Now().Add(20 * time.Second)
}

// SetHookSocketError is why nerdr has no socket, shown now and given to
// the agents, so that nerdr's commands in them say so rather than blame a
// sandbox. Another nerdr having it is the likely one.
func (m *Model) SetHookSocketError(err error) {
	m.hookSockErr = err.Error()
	if errors.Is(err, hooks.ErrTaken) {
		m.hookSockErr += "; quit that nerdr and restart this one, or its tabs cannot ask, try, review, spin off or hand off"
	}
	m.Notice("no socket for the agents: " + m.hookSockErr)
}

// SetSocketShared says another nerdr has the socket a sandbox is told
// of, so this one's agents report to one of its own (see hooks.ForRoot):
// all works but for an agent in a sandbox that lets it reach only the
// usual socket.
func (m *Model) SetSocketShared(taken error) {
	note := taken.Error() + ": this nerdr's agents report to " + m.hookSock + " instead"
	for _, s := range m.cfg.Agents {
		if s.Sandboxed {
			note += "; an agent in a sandbox reaches this nerdr only if its sandbox lets it reach that socket too"
			break
		}
	}
	m.Notice(note)
}

// agentEnv is the environment an agent runs with: childEnv, the socket
// nerdr's own commands report to, and herdr's variables pointing its
// integrations at nerdr. The tab's id stands for herdr's pane id.
func (m *Model) agentEnv(t *tab) []string {
	env := childEnv(t)
	sock, sockErr := m.hookSock, m.hookSockErr
	if t.sandboxed {
		env = append(env, "NERDR_SANDBOXED=1")
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
		"HERDR_PANE_ID="+t.hookKey(),
		"HERDR_SOCKET_PATH="+sock,
	)
	if m.hookBin != "" {
		env = append(env, "HERDR_BIN_PATH="+m.hookBin)
	}
	if t.Agent == agents.OpenCode {
		// For nerdr's plugin, which reports for this opencode alone: it
		// takes the variable out, so another opencode run in the tab
		// does not report as the tab's agent. See hooks.OpenCodePlugin.
		env = append(env, "NERDR_OPENCODE_HOOK=1")
	}
	if t.Agent == agents.Antigravity {
		// For nerdr's hooks, which report for an agy whose parent is this
		// nerdr: the one it starts, not one that agent runs. See
		// hooks.AgyCommand.
		env = append(env, "NERDR_AGY_HOOK="+strconv.Itoa(os.Getpid()))
	}
	return env
}

// codexHookArgs are the arguments that give codex nerdr's hooks, or none
// while codex does not trust them: an untrusted hook stops codex at start
// to ask about it. codex is asked once; where the skills go in the home
// folder, applySkills has it trust them.
func (m *Model) codexHookArgs() []string {
	if !m.codexAsked {
		m.trustCodexHooks(false)
	}
	return m.codexHooks
}

// trustCodexHooks asks codex whether it trusts nerdr's hooks, and with
// write has it trust them, and reports whether that wrote its config.
func (m *Model) trustCodexHooks(write bool) (bool, error) {
	m.codexAsked = true
	argv, env := agents.Command(m.cfg.Agents, agents.Codex)
	trusted, wrote, err := trustCodex(argv, withUserEnv(os.Environ(), env), write)
	m.codexHooks = nil
	if trusted {
		m.codexHooks = hooks.CodexArgs()
	}
	return wrote, err
}

// copilotPlugin is the folder of nerdr's plugin for t's copilot, where the
// install setting put it, or "" for none.
func (m *Model) copilotPlugin(t *tab) string {
	var dir string
	switch m.cfg.Install {
	case config.InstallHome:
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = hooks.CopilotPluginHome(home)
	case config.InstallWorktree:
		if t.sandboxed {
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

// trustCodex is hooks.TrustCodexHooks; the tests keep the user's codex
// out of it.
var trustCodex = hooks.TrustCodexHooks

// hookAuthority is what an integration has said about a tab's agent.
type hookAuthority struct {
	source string // the integration reporting state; "" when none is
	state  detect.State
	at     time.Time // when state was reported
	msg    string
	seq    map[string]int64 // the last sequence number taken, per source
	// background says the agent's turn is over, but it waits on subagents
	// it started in the background: its screen is still and at the prompt
	// meanwhile, and says nothing of it.
	background bool
}

// onHook takes a report from an integration.
func (m *Model) onHook(r hooks.Report) tea.Cmd {
	t := m.hookSession(r)
	if t == nil {
		return nil
	}
	switch r.Method {
	case hooks.ReportAgent:
		if r.State == "" {
			return nil
		}
		t.hook.source = r.Source
		t.hook.msg = r.Message
		t.hook.at = time.Now()
		t.hook.background = r.Background
		switch r.State {
		case "working":
			t.hook.state = detect.Working
		case "blocked":
			t.hook.state = detect.Blocked
		case "idle":
			t.hook.state = detect.Idle
		default:
			t.hook.state = detect.Unknown
		}
		return m.detect() // at once: a hook is news the screen may not show yet
	case hooks.ReleaseAgent:
		if r.Source == t.hook.source {
			t.hook.source, t.hook.msg = "", ""
		}
	}
	return nil
}

// hookSession takes the conversation report r says its agent is in, and
// returns the agent, or nil when the report is not one to take: for no
// agent running, another agent than it, or older than one taken. It is
// all of a report that counts while nerdr quits: the conversation to
// resume next time.
func (m *Model) hookSession(r hooks.Report) *tab {
	t := m.agentByHook(r.Pane)
	if t == nil || t.pane == nil {
		return nil
	}
	// A report for another agent than the tab's (a subagent, a tool the
	// agent ran) is not about the tab.
	if r.Agent != "" && r.Agent != t.agentID() {
		return nil
	}
	if t.hook.seq == nil {
		t.hook.seq = map[string]int64{}
	}
	if r.Seq > 0 {
		if r.Seq <= t.hook.seq[r.Source] {
			return nil // out of order: an older report arriving late
		}
		t.hook.seq[r.Source] = r.Seq
	}
	// Asked once, of the tab as it was before the report: taking its
	// conversation below would make the rest of it look nested.
	if nestedSession(r, t) {
		return t
	}
	if r.SessionID != "" && r.SessionID != t.SessionID && agents.ValidSessionID(r.SessionID) {
		// The conversation the agent is really in — after /clear too — so
		// a restart resumes this one, not a guess. A command reported for
		// the one before reopens that one: it goes, unless this report
		// brings the new one's.
		t.ResumeArgv = nil
		m.setSession(t, r.SessionID)
	}
	if len(r.ResumeArgv) > 0 && !m.confined(r, t) && !slices.Equal(r.ResumeArgv, t.ResumeArgv) {
		t.ResumeArgv = r.ResumeArgv
		m.saveSession()
	}
	return t
}

// nestedSession reports whether r is the start of a conversation that is
// not the tab's: a `claude -p` the agent runs inherits the tab's
// environment, and herdr's integration in it reports its start to nerdr.
// The tab's own agent starts a new conversation with clear or resume, or
// with startup only before its first is known. nerdr's own hooks are not
// inherited (they come on the command line, or ask for a variable only
// the tab's agent gets), so theirs is the tab's.
func nestedSession(r hooks.Report, t *tab) bool {
	return r.SessionSource == "startup" && t.SessionID != "" && !strings.HasPrefix(r.Source, "nerdr:")
}

// cutOff says why nothing t ran has reached nerdr, when that is the likely
// reason for it: t runs in a sandbox, was started with an integration that
// reports to nerdr as it works, and has ended a turn with none of it come.
// Its integration and nerdr's commands in it run the same nerdr, through
// the same socket, so neither works: the sandbox drops NERDR_BIN, or keeps
// the agent from running nerdr or from its socket. "" when t was heard.
func (m *Model) cutOff(t *tab) string {
	if !t.sandboxed || !t.reports || t.heard {
		return ""
	}
	bin := m.hookBin
	if bin == "" {
		bin = "nerdr"
	}
	why := "nothing it ran reached nerdr, so it can neither report to nerdr nor use its commands (send, ready, review). " +
		"Its sandbox must let the NERDR_* variables through, NERDR_BIN among them, allow running " + bin
	if m.hookSock != "" {
		why += " and allow connecting to " + m.hookSock
	}
	return why + "; then restart the agent. See \"Agents in a sandbox\" in nerdr's docs"
}

// tellCutOff tells the user, once for the agent's run, that nothing t ran
// has reached nerdr; see cutOff.
func (m *Model) tellCutOff(t *tab) {
	if why := m.cutOff(t); why != "" && !t.toldCutOff {
		t.toldCutOff = true
		m.Notice(m.agentName(t) + ": " + why)
	}
}
