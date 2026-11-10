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
	"github.com/nerdr-dev/nerdr/internal/holder"
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
	"NERDR_SHELL", "NERDR_WORKTREES", "NERDR_BIN", "NERDR_DATA_DIR", EnvByAgent, holder.EnvNested}

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

// ChildEnv is the environment an agent runs with, given nerdr's own.
func ChildEnv(t *Tab) []string {
	return ChildEnvFrom(os.Environ(), t)
}

// BaseEnv is the environment the programs the core starts build on: the
// last client's, as nerdr run in its terminal would have, else the
// core's own. The core's is that of the nerdr that started the holder,
// in another terminal or session perhaps, with an ssh agent or display
// since gone; a program started with it would reach what the user's
// terminal does not, and miss what it does. m.Mu is held.
func (m *Core) BaseEnv() []string {
	if m.ClientEnv != nil {
		return slices.Clone(m.ClientEnv)
	}
	return os.Environ()
}

// TabEnv is ChildEnv with m's BaseEnv in place of nerdr's own. m.Mu is
// held.
func (m *Core) TabEnv(t *Tab) []string {
	return ChildEnvFrom(m.BaseEnv(), t)
}

// ChildEnvFrom is the environment an agent of t runs with, built on base.
func ChildEnvFrom(base []string, t *Tab) []string {
	env := slices.DeleteFunc(herdr.ChildEnv(base), func(kv string) bool {
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
	if exe, err := spawn.Bin(); err == nil {
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
// Index is where t is in the tab row, or -1 once it has been closed.
func (l TabList) Index(t *Tab) int {
	for i, x := range l {
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
	if m.takeUpAgent(t) {
		return nil
	}
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
		return m.SpawnNew(t, m.SpecsFor(t), nil)
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

// SpawnNew starts t's agent on a new conversation, from specs: at once,
// or, when its command line needs an answer from its CLI not had yet (see
// agents.NewSessionKnown), once that has been asked with m.Mu let go, by
// LaunchMsg. add, when set, adds to the command line. m.Mu is held.
func (m *Core) SpawnNew(t *Tab, specs map[agents.Kind]agents.Spec, add func(agents.Launch) agents.Launch) Cmd {
	kind := t.Agent
	if add == nil {
		add = func(l agents.Launch) agents.Launch { return l }
	}
	if !agents.NewSessionKnown(specs, kind) {
		return func() Event { return LaunchMsg{T: t, Kind: kind, L: add(agents.NewSession(specs, kind))} }
	}
	return m.Spawn(t, add(agents.NewSession(specs, kind)), false)
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

// SpawnPrep is what starting an agent asks its CLI first: codex whether
// it trusts nerdr's hooks, and for its own developer instructions,
// copilot whether it takes --plugin-dir. Each runs the agent's command,
// which can take seconds, so it is asked away from Mu, and the agent
// started when SpawnReady brings the answers.
type SpawnPrep struct {
	// CodexAsked is set when codex was asked whether it trusts nerdr's
	// hooks, CodexTrusted to its answer.
	CodexAsked, CodexTrusted bool
	// CodexNoted is set when CodexNote was worked out.
	CodexNoted bool
	CodexNote  []string
	// Check is how the check from inside the agent's sandbox went, nil
	// when there was none; see CheckSandbox.
	Check *SandboxCheckedMsg
}

// SpawnReady is a tab's agent to start, with what its CLI said first.
type SpawnReady struct {
	T      *Tab
	Kind   agents.Kind
	L      agents.Launch
	Resume bool
	Prep   SpawnPrep
}

// Prepare is how to ask what starting t's agent with l needs to ask its
// CLI first, and to check its sandbox, with m.Mu let go; nil when there
// is nothing to ask. One the check refuses is not asked more.
func (m *Core) Prepare(t *Tab, l agents.Launch) func() SpawnPrep {
	env := m.LaunchEnv(t, l)
	check := m.CheckSandbox(t, env)
	ask := m.prepareAgent(t, l, env)
	if check == nil && ask == nil {
		return nil
	}
	return func() SpawnPrep {
		var c *SandboxCheckedMsg
		if check != nil {
			ev := check()
			if c = &ev; ev.Refused() {
				return SpawnPrep{Check: c}
			}
		}
		var p SpawnPrep
		if ask != nil {
			p = ask()
		}
		p.Check = c
		return p
	}
}

// LaunchEnv is the environment t's agent starts with from l: in or out of
// its sandbox, as Spawn will put it. m.Mu is held.
func (m *Core) LaunchEnv(t *Tab, l agents.Launch) []string {
	was := t.InSandbox
	t.InSandbox = m.SandboxedKind(t.Agent)
	defer func() { t.InSandbox = was }()
	return WithUserEnv(m.AgentEnv(t), l.Env)
}

// prepareAgent is Prepare for what the agent's CLI is asked, env being
// what it starts with.
func (m *Core) prepareAgent(t *Tab, l agents.Launch, env []string) func() SpawnPrep {
	switch t.Agent {
	case agents.Codex:
		var ask func() (bool, bool, error)
		hooked := m.HookSock != "" && slices.Contains(l.Argv, "--no-daemon")
		if hooked && !m.CodexAsked {
			ask = m.AskCodexTrust(false)
		}
		known := m.CodexHooks
		note := m.AgentNote(t)
		if ask == nil && note == "" {
			return nil
		}
		command, _ := agents.Command(m.SpecsFor(t), t.Agent)
		argv, dir := slices.Clone(l.Argv), t.Dir
		return func() SpawnPrep {
			var p SpawnPrep
			args := known
			if ask != nil {
				trusted, _, err := ask()
				if err != nil {
					Debugf("codex hooks: %v", err)
				}
				p.CodexAsked, p.CodexTrusted = true, trusted
				args = nil
				if trusted {
					args = hooks.CodexArgs()
				}
			}
			if hooked {
				argv = BeforePrompt(argv, args, len(command))
			}
			p.CodexNoted = true
			p.CodexNote = CodexNoteArgs(note, command, argv, env, dir)
			return p
		}
	case agents.Copilot:
		specs := m.Cfg.Agents
		if m.HookSock == "" || m.CopilotPlugin(t) == "" || agents.FlagKnown(specs, agents.Copilot, "--plugin-dir") {
			return nil
		}
		return func() SpawnPrep {
			agents.HasFlag(specs, agents.Copilot, "--plugin-dir") // known from then on
			return SpawnPrep{}
		}
	}
	return nil
}

// Spawn runs the command line in a new pane for the tab: at once, or,
// when where its agent has the skills is to be looked at first (see
// skillsBeforeStart), once it has been, by SpawnLooked, and when its CLI
// must be asked something first (see Prepare), once it has answered, by
// SpawnReady. m.Mu is held.
func (m *Core) Spawn(t *Tab, l agents.Launch, resume bool) Cmd {
	if read := m.skillsBeforeStart(t); read != nil {
		kind := t.Agent
		return func() Event { return SpawnLooked{T: t, Kind: kind, L: l, Resume: resume, Look: read()} }
	}
	return m.spawnLooked(t, l, resume)
}

// SpawnLooked is a tab's agent to start, with where it has the skills.
type SpawnLooked struct {
	T      *Tab
	Kind   agents.Kind
	L      agents.Launch
	Resume bool
	Look   SkillLook
}

// OnSpawnLooked keeps where ev's agent has the skills, and starts it,
// unless its tab closed or switched agent meanwhile.
func (m *Core) OnSpawnLooked(ev SpawnLooked) Cmd {
	if !m.Live(ev.T) || ev.Kind != ev.T.Agent {
		return nil
	}
	m.keepSkillLook(ev.T.ID, ev.Look)
	cmd := m.spawnLooked(ev.T, ev.L, ev.Resume)
	if ev.T.Pane == nil && ev.T.Light == StExited {
		switch {
		case ev.T.ReviewOf != nil:
			m.ReviewerNotStarted(ev.T)
		case ev.T.Err != "":
			m.Toast(ev.T.Name + ": " + ev.T.Err)
		}
	}
	return cmd
}

// spawnLooked is Spawn once where the agent has the skills is known.
func (m *Core) spawnLooked(t *Tab, l agents.Launch, resume bool) Cmd {
	if ask := m.Prepare(t, l); ask != nil {
		kind := t.Agent
		return func() Event { return SpawnReady{T: t, Kind: kind, L: l, Resume: resume, Prep: ask()} }
	}
	return m.SpawnPrepared(t, l, resume, SpawnPrep{})
}

// OnSpawnReady starts the agent ev is for, unless its tab closed or
// switched agent while its CLI was asked.
func (m *Core) OnSpawnReady(ev SpawnReady) Cmd {
	if !m.Live(ev.T) || ev.Kind != ev.T.Agent {
		return nil
	}
	cmd := m.SpawnPrepared(ev.T, ev.L, ev.Resume, ev.Prep)
	switch {
	case ev.T.Pane != nil:
	case ev.T.ReviewOf != nil:
		m.ReviewerNotStarted(ev.T)
	case ev.T.Err != "":
		m.Toast(ev.T.Name + ": " + ev.T.Err)
	}
	return cmd
}

// SpawnPrepared is Spawn, with what the agent's CLI was asked.
func (m *Core) SpawnPrepared(t *Tab, l agents.Launch, resume bool, prep SpawnPrep) Cmd {
	if prep.Check != nil {
		m.OnSandboxChecked(*prep.Check)
		if prep.Check.Refused() {
			t.Err = "not started: its sandbox lets it reach nerdr's client socket or holder, which it must deny"
			t.Light = StExited
			return nil
		}
	}
	if prep.CodexAsked {
		m.SetCodexTrust(prep.CodexTrusted)
	}
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
		if args := prep.CodexNote; prep.CodexNoted && args != nil {
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
	spec := spawn.Spec{Argv: l.Argv, Dir: t.Dir, Env: WithUserEnv(m.AgentEnv(t), l.Env), W: w, H: h}
	m.Hold(&spec, AgentHeldLabel(t), t.InSandbox, t.HookKey())
	p, err := term.StartWith(m.Programs(), spec, m.Notify)
	if err != nil {
		t.Messages = append(first, t.Messages...) // for the next start
		t.Err = fmt.Sprintf("could not start %s: %v", l.Argv[0], err)
		t.Light = StExited
		return nil
	}
	t.Pane = p
	t.PidStart = p.StartTime()
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

	// What was going on when you last left the tab, what its agent did
	// while you were away, and the summary made when you came to it, for
	// H; see attention.go.
	Visit   *Visit
	Away    AwayLog
	Summary *Summary

	// Whether its agent's log is being read for the task; and what is
	// typed to an agent whose log is not read, to take the task from. See
	// panel.go.
	GoalLooking bool
	Typed       *TypedLine `json:"-"`
	// PromptedInto is the program a prompt was last typed into by TypeIn,
	// for the enter after it; see PromptEnter.
	PromptedInto *term.Pane `json:"-"`
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

	// Moving is set while the tab is being moved to another machine;
	// see move.go.
	Moving *MoveWait

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

	// wireRef marks a tab read as a reference to another, by its ID alone,
	// until a Mirror points at the tab itself; see Tab.UnmarshalJSON.
	wireRef bool
	// remote is, in a copy of core's state, whether core runs the agent's
	// program, which the copy has no Pane of; see HasProgram.
	remote remoteProgram
}

// remoteProgram is what a copy of core's state knows of an agent's
// program: none, running, or exited.
type remoteProgram uint8

const (
	remoteNone remoteProgram = iota
	remoteRuns
	remoteExited
)

// HasProgram reports whether the agent has a program, running or exited:
// its Pane, or in a copy of core's state, the one core says it runs.
func (t *Tab) HasProgram() bool { return t.Pane != nil || t.remote != remoteNone }

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

// NewJobID is an id for a request whose answer comes later (see Later),
// for the client to know it by.
func NewJobID() string { return NewTabID() }
