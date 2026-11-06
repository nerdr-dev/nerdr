package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/usage"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func NewTabID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// TabVars are the variables nerdr gives an agent or terminal of a tab.
// Inherited from a nerdr this one runs in, they are left out: an agent is
// to report to this one, and one in a sandbox never to a socket it was
// not given. The user's own, such as NERDR_DEBUG_LOG, are passed on.
var TabVars = []string{"NERDR", "NERDR_SOCKET", "NERDR_TAB", "NERDR_NO_SOCKET", "NERDR_SANDBOXED", "NERDR_OPENCODE_HOOK", "NERDR_OPENCODE_NOTE", "NERDR_AGY_HOOK", "NERDR_GROK_HOOK",
	"NERDR_SHELL", "NERDR_WORKTREES", "NERDR_BIN", "NERDR_DATA_DIR"}

// HostVars are left out of what an agent or terminal of a tab inherits,
// as herdr leaves them out of its panes. The first say nerdr was started
// by an agent, and a tab's agent would take itself for one of its
// subagents, or carry on its session; the rest name the terminal or
// multiplexer nerdr runs in, which a pane is not: an agent that finds
// TMUX or KITTY_WINDOW_ID talks past nerdr to it.
var HostVars = []string{
	"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_MESSAGING_TOKEN", "CODEX_THREAD_ID", "OMPCODE",
	"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID", "ITERM_SESSION_ID",
	"LC_TERMINAL", "LC_TERMINAL_VERSION", "WEZTERM_PANE", "KITTY_WINDOW_ID", "WT_SESSION",
	"TMUX", "TMUX_PANE", "STY", "ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID",
}

// ChildEnv is the environment an agent runs with.
func ChildEnv(t *Tab) []string {
	env := slices.DeleteFunc(herdr.ChildEnv(os.Environ()), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(TabVars, name) || slices.Contains(HostVars, name)
	})
	var paths []string
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	encoded, _ := json.Marshal(paths)
	env = append(env, "NERDR_WORKTREES="+string(encoded))
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"TERM_PROGRAM=nerdr",
		"NERDR=1",
		"NERDR_TAB="+t.HookKey(),
		// For the skills: the nerdr to record notes with, and where they
		// go, which a sandboxed agent's nerdr could not read from the
		// config. See skills/.
		store.EnvDataDir+"="+store.DataDir,
	)
	if exe, err := os.Executable(); err == nil {
		env = append(env, "NERDR_BIN="+exe)
	}
	return env
}

// WithUserEnv is env with the variables of the agent's config section
// added, all but nerdr's and herdr's own: those tell the agent which tab
// it is in and where to report, and one set wrong would report for another.
func WithUserEnv(env, user []string) []string {
	out := append([]string{}, env...)
	for _, kv := range user {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "NERDR") || strings.HasPrefix(name, "HERDR_") {
			continue
		}
		out = append(out, kv) // after the inherited one, so it wins
	}
	return out
}

// SpecsFor is how the agents are started for t: the config's, with t's
// own model when it was started with one.
func (m *Core) SpecsFor(t *Tab) map[agents.Kind]agents.Spec {
	return agents.WithModel(m.Cfg.Agents, t.Agent, t.Model)
}

// OthersSessions are the conversations had in t's worktree that are not
// t's to carry on: its reviewers', and those of the other agents of its
// tab, in each kind they have run as.
func (m *Core) OthersSessions(t *Tab) map[string]bool {
	skip := codereview.Sessions(t.Dir, string(t.Agent))
	o := t.Owner()
	for _, a := range append([]*Tab{o}, o.ExtraAgents()...) {
		if a == t {
			continue
		}
		if a.SessionID != "" {
			skip[a.SessionID] = true
		}
		for _, id := range a.Sessions {
			if id != "" {
				skip[id] = true
			}
		}
	}
	delete(skip, t.SessionID)
	return skip
}

// SharesKind reports whether another agent of t's tab runs as t's kind:
// then the worktree's latest conversation of the kind may be the other's,
// which an agent that reports no id (codex) cannot tell from its own.
func (m *Core) SharesKind(t *Tab) bool {
	o := t.Owner()
	for _, a := range append([]*Tab{o}, o.ExtraAgents()...) {
		if a != t && a.Agent == t.Agent {
			return true
		}
	}
	return false
}

// CarryOn starts a new tab's agent on the conversation it had in the
// tab's worktree before, as resuming does, when there is one: a worktree
// an agent worked in outside nerdr, or in a tab since closed. Looking
// for it reads the agent's logs, so it is done in the background.
func (m *Core) CarryOn(t *Tab) Cmd {
	t.Light = StStarting
	t.Err = ""
	specs, kind, dir := m.SpecsFor(t), t.Agent, t.Dir
	return func() Event {
		skip := codereview.Sessions(dir, string(kind))
		if agents.HasConversation(specs, kind, dir, skip) {
			l := agents.Resume(specs, kind, "", dir, skip)
			return LaunchMsg{T: t, Kind: kind, L: l, Resume: !l.New}
		}
		return LaunchMsg{T: t, Kind: kind, L: agents.NewSessionIn(specs, kind, dir)}
	}
}

// TrustWorktrees tells gitsafe which repository each of t's worktrees
// belongs to, so git run there is not steered by what the agent wrote in
// the worktree, and records each one's origin, which a push must keep to.
func TrustWorktrees(t *Tab) {
	for i := range t.Worktrees {
		workspace.Trust(&t.Worktrees[i])
	}
}

// LaunchMsg carries a resolved command line for a tab.
type LaunchMsg struct {
	T      *Tab
	Kind   agents.Kind // the agent it is for; the tab may have switched since
	L      agents.Launch
	Resume bool
	// Handoff is the first message for an agent switched to: what the
	// one before it did; sandboxed is whether that agent was in a sandbox.
	Handoff   string
	Sandboxed bool
}

// BeforePrompt is argv with opts added before the "--" a prompt put on
// the command line follows (see agents.StartPrompt), or at the end when
// there is none: after it, codex takes them for words of the prompt.
// commandLen skips the configured command, whose wrapper may have its
// own "--" before codex's arguments even begin.
func BeforePrompt(argv, opts []string, commandLen int) []string {
	start := min(commandLen, len(argv))
	i := slices.Index(argv[start:], "--")
	if i < 0 {
		return append(argv, opts...)
	}
	i += start
	return slices.Concat(argv[:i], opts, argv[i:])
}

// agentID is the agent t's screen is read as and its integration reports
// as: detectAs, or the tab's kind before its agent has been started.
// tabIndex is where t is in the tab row, or -1 once it has been closed.
func (m *Core) TabIndex(t *Tab) int {
	for i, x := range m.Tabs {
		if x == t {
			return i
		}
	}
	return -1
}

// Notify is called by panes when they draw. It must not block: at most one
// wake-up is queued, and the frame loop picks up everything that changed.
func (m *Core) Notify() {
	select {
	case m.Wake <- struct{}{}:
	default:
	}
}

// start launches the tab's agent: a new conversation, or the one the tab
// had before when resume is set. Finding the conversation to resume can
// mean asking the agent's CLI (opencode), so that part runs in the
// background and the agent is started when launchMsg comes back.
func (m *Core) Start(t *Tab, resume bool) Cmd {
	t.Light = StStarting
	t.Err = ""
	if t.InSandbox || m.SandboxedKind(t.Agent) {
		t.ResumeArgv = nil
	}
	m.MarkSandboxed(t)
	beside := t.PartOf != nil
	known := t.SessionID != "" || len(t.ResumeArgv) > 0
	if beside && !known || !known && m.SharesKind(t) {
		// The worktree's last conversation could be another agent's in the
		// tab: one not known by its id starts afresh.
		resume = false
	}
	if !resume {
		if t.Agent == agents.Cursor {
			// Its chat is made first, by running it.
			specs, kind, dir := m.SpecsFor(t), t.Agent, t.Dir
			return func() Event { return LaunchMsg{T: t, Kind: kind, L: agents.NewSessionIn(specs, kind, dir)} }
		}
		return m.Spawn(t, agents.NewSession(m.SpecsFor(t), t.Agent), false)
	}
	specs, kind, id, dir, skip := m.SpecsFor(t), t.Agent, t.SessionID, t.Dir, m.OthersSessions(t)
	reported := t.ResumeArgv
	return func() Event {
		if l, ok := agents.ResumeReported(specs, kind, reported); ok {
			return LaunchMsg{T: t, Kind: kind, L: l, Resume: true}
		}
		l := agents.Resume(specs, kind, id, dir, skip)
		if beside && l.SessionID != id {
			// Its own conversation is gone: never another one in its place.
			return LaunchMsg{T: t, Kind: kind, L: agents.NewSessionIn(specs, kind, dir)}
		}
		return LaunchMsg{T: t, Kind: kind, L: l, Resume: !l.New}
	}
}

// firstAsArgs takes t's first message off its list and returns it, and
// the arguments that start t's agent with it, when that agent takes one so
// (agents.StartPrompt) and the message is to go in by itself. Typed in,
// it could land in copilot while it was still starting and be lost.
func (m *Core) FirstAsArgs(t *Tab) ([]string, state.Message) {
	if len(t.Messages) == 0 || !t.Messages[0].Auto {
		return nil, state.Message{}
	}
	msg := t.Messages[0]
	args := agents.StartPrompt(t.Agent, msg.Text)
	if args == nil {
		return nil, state.Message{}
	}
	t.DropMessage(0)
	m.SetGoal(t, msg.Text)
	m.SetLast(t, msg.Text)
	m.SaveSession()
	return args, msg
}

// spawn runs the command line in a new pane for the tab.
func (m *Core) Spawn(t *Tab, l agents.Launch, resume bool) Cmd {
	if l.SessionID != t.SessionID && (l.SessionID != "" || !resume) {
		// A new conversation is the agent's now: with no id from nerdr,
		// none, until it is found (claimSession), not the one before.
		m.SetSession(t, l.SessionID)
	}
	if _, err := os.Stat(t.Dir); err != nil {
		t.Err = "the worktree is gone: " + t.Dir
		t.Light = StExited
		return nil
	}
	m.InstallProjectSkills(t)
	// Before the first message can go on the command line.
	t.InSandbox = m.SandboxedKind(t.Agent)
	if t.InSandbox {
		t.Sandboxed = true
		t.Owner().Sandboxed = true
		m.MarkSandboxed(t)
	} else {
		m.HoldSandboxed(t)
	}
	reports := false
	if t.Agent == agents.Claude && m.HookSock != "" {
		// Claude says itself when it is working and when it is done; see
		// hooks.ClaudeSettings.
		l.Argv = append(l.Argv, "--settings", hooks.ClaudeSettings(IsOn(m.Cfg.ClaudeStatusLine)))
		reports = true
	}
	if t.Agent == agents.Codex && m.HookSock != "" && slices.Contains(l.Argv, "--no-daemon") {
		// So does codex, once it trusts nerdr's hooks; see
		// hooks.CodexArgs. Only in a codex of its own: one with the
		// shared server runs hooks with another tab's variables.
		command, _ := agents.Command(m.SpecsFor(t), t.Agent)
		args := m.CodexHookArgs()
		l.Argv = BeforePrompt(l.Argv, args, len(command))
		reports = len(args) > 0
	}
	if t.Agent == agents.Copilot && m.HookSock != "" {
		// And copilot, with nerdr's plugin; see hooks.InstallCopilotPlugin.
		if dir := m.CopilotPlugin(t); dir != "" && agents.HasFlag(m.Cfg.Agents, agents.Copilot, "--plugin-dir") {
			l.Argv = append(l.Argv, "--plugin-dir", dir)
			reports = true
		}
	}
	switch t.Agent {
	case agents.Claude:
		l.Argv = append(l.Argv, m.ClaudeNote(t)...)
	case agents.Codex:
		if args := m.CodexNote(t, l.Argv, WithUserEnv(m.AgentEnv(t), l.Env)); args != nil {
			command, _ := agents.Command(m.SpecsFor(t), t.Agent)
			l.Argv = BeforePrompt(l.Argv, args, len(command))
		}
	case agents.Copilot:
		l.Env = m.CopilotNote(t, l.Env)
	case agents.Antigravity:
		l.Argv = append(l.Argv, m.AgyNote(t)...)
	case agents.Grok:
		l.Argv = append(l.Argv, m.GrokNote(t)...)
	}
	var first []state.Message
	if !resume {
		if args, msg := m.FirstAsArgs(t); args != nil {
			first = []state.Message{msg}
			l.Argv = append(l.Argv, args...)
		}
	}
	w, h := m.Client.TabSize(m.Ref(t))
	p, err := term.StartWith(m.Programs(), spawn.Spec{Argv: l.Argv, Dir: t.Dir, Env: WithUserEnv(m.AgentEnv(t), l.Env), W: w, H: h}, m.Notify)
	if err != nil {
		t.Messages = append(first, t.Messages...) // for the next start
		t.Err = fmt.Sprintf("could not start %s: %v", l.Argv[0], err)
		t.Light = StExited
		return nil
	}
	t.Pane = p
	t.Gen = lastGen.Add(1)
	t.StartedAt, t.Said, t.SessionUnsure = time.Now(), nil, l.Unsure
	t.Hook = HookAuthority{} // a new process: its integration reports afresh
	t.Reports, t.Heard, t.ToldCutOff = reports, false, false
	t.DetectAs = agents.DetectID(m.Cfg.Agents, t.Agent)
	t.Resumed = resume
	t.Light = StStarting
	t.Raw = detect.Unknown
	t.Worked = false
	t.Tasked = len(first) > 0
	t.Err = ""
	t.Changed = time.Now()
	m.Tell(StartedMsg{A: m.Ref(t)})
	return nil
}

// Tab is one agent in one set of worktrees.
type Tab struct {
	AgentState
	FocusVisit *FocusVisit
	Unseen     map[string]map[string]bool
	// InfoAsks and prAsks count the fresh looks at the branches, and at
	// their pull requests, asked for; see lookAgain.
	InfoAsks, PrAsks int

	// ShownAt is when the tab was last on screen, alone or in a split;
	// see sleep.go.
	ShownAt time.Time

	// Resumed is set when the agent was started to continue an earlier
	// conversation; fallbackTried when that failed and it was started
	// afresh, so a broken agent is not restarted in a loop.
	Resumed       bool
	FallbackTried bool
	// StartedAt is when the agent's process was started, the earliest its
	// conversation can have been; said, what nerdr has typed in to it
	// since, and sessionUnsure, that SessionID is one nerdr gave it that
	// it may not have taken. See claimSession.
	StartedAt     time.Time
	Said          []string
	SessionUnsure bool

	Err string

	// PaneList is the tab's panes, its own agent first.
	// partOf is, on an agent in another's pane, the tab it is in. See
	// panes.go.
	PaneList []*PaneTab
	PartOf   *Tab

	// Info is what the background look at the tab's branches found; see
	// info.go.
	Info TabInfo

	// Reports is set when the agent was started with an integration that
	// reports to nerdr as it works, heard once anything it ran has reached
	// nerdr's socket, and toldCutOff once the user was told nothing did;
	// see cutOff.
	Reports, Heard, ToldCutOff bool

	// Notified is the state last notified of, and when; see notify.go.
	Notified   Status
	NotifiedAt time.Time

	// What was going on when you last left the tab, what its agent did
	// while you were away, and the summary made when you came to it, for
	// H; see attention.go.
	Visit   *Visit
	Away    AwayLog
	Summary *Summary

	// The task wrapped for the panel; whether its agent's log is being
	// read for it; and what is typed to an agent whose log is not read,
	// to take the task from. See panel.go.
	GoalWrap    *GoalWrap
	GoalLooking bool
	Typed       *TypedLine
	// LastAt is when Last was set from what was typed, so the agent's log,
	// read a moment later, cannot put back the prompt before it.
	LastAt time.Time

	// SpinoffBusy says what is being made for the part of Spinoff asked
	// about, while it is; see spinoff.go. spinoffSince is when
	// SpinoffReport began to wait for the tabs made.
	SpinoffBusy  string
	SpinoffSince time.Time

	// Usage is what the agent last said of its context window and its
	// account, and when; see usage.go.
	Usage   *usage.Report
	UsageAt time.Time

	// Handoff is the switch of agent waiting for the old agent's handoff,
	// handoffReady a handoff the agent sent with none waiting; see
	// agentswitch.go.
	Handoff      *HandoffWait
	HandoffReady *HandoffNote

	// ReviewAsk is set when the agent has said its work is ready for a
	// review, to what it wants looked at most and who it asked to review
	// it, until the review starts; see onReady.
	ReviewAsk *ReadyAsk

	// HookID is the id the tab's agent reports to nerdr's socket under;
	// see hookKey.
	HookID string

	// Review is the code review going on in the tab; reviewOf, on a
	// reviewer, the tab whose work it reviews. See codereview.go.
	Review   *ReviewRun
	ReviewOf *Tab
	// ReposBusy is set while the tab's repositories are being changed, in
	// the background (see repos.go): nothing else may start in, close or
	// change its worktrees until it is done.
	ReposBusy bool
}

// HookKey is the id the tab's agent is given to report to nerdr with, as
// HERDR_PANE_ID and NERDR_TAB: a secret made for this run, not the tab's
// id. Every agent can write to the socket, and the tab ids are no secret
// (they name the handoff files beside the notes), so with those an agent
// could report for another tab: plant a handoff its next agent is given,
// or a spinoff, or change the conversation it resumes.
func (t *Tab) HookKey() string {
	if t.HookID == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		t.HookID = hex.EncodeToString(b[:])
	}
	return t.HookID
}

// Attention ranks how much a tab needs the user: blocked first, then
// finished work, then work in progress, then a turn over with a process
// left running: not idle, or herdr is told so and tells of it as done.
func (t *Tab) Attention() int {
	switch t.Lit() {
	case StBlocked:
		return 4
	case StDone:
		return 3
	case StWorking:
		return 2
	case StBackground:
		return 1
	}
	return 0
}

// lastGen is the last Agent.Gen given out.
var lastGen atomic.Uint64
