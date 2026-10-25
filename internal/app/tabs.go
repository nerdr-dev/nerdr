package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/usage"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// status is what a tab's traffic light shows.
type status int

const (
	stStarting status = iota
	stIdle
	stWorking
	stBlocked
	stDone // idle after work the user has not looked at yet
	stUnknown
	stExited
	// stAsleep is an agent nerdr stopped, done and seen, while nobody
	// looked at its tab, to carry its conversation on when it is visited:
	// see sleep.go.
	stAsleep
)

// tab is one agent in one set of worktrees.
type tab struct {
	state.Tab
	focusVisit *focusVisit
	unseen     map[string]map[string]bool
	pane       *term.Pane
	// infoAsks and prAsks count the fresh looks at the branches, and at
	// their pull requests, asked for; see lookAgain.
	infoAsks, prAsks int

	status status
	// raw is the state the screen showed at the last check, before the
	// idle debounce; idleSince is when it last turned idle.
	raw       detect.State
	idleSince time.Time
	// changed is when status last changed, to pick the most recent of
	// several tabs that need attention.
	changed time.Time
	// shownAt is when the tab was last on screen, alone or in a split;
	// see sleep.go.
	shownAt time.Time
	// worked is set once the agent has been seen working since the tab was
	// last looked at; only then does going idle mean "done".
	worked bool
	// begun is set once the agent has been seen taking up something it
	// was given, working on it, asking about it or answering it, since it
	// was started; see reportWhenBegun.
	begun bool

	// reading is the last reading of the agent's screen, kept while the
	// screen is the same; see readScreen.
	reading screenReading

	// resumed is set when the agent was started to continue an earlier
	// conversation; fallbackTried when that failed and it was started
	// afresh, so a broken agent is not restarted in a loop.
	resumed       bool
	fallbackTried bool
	// startedAt is when the agent's process was started, the earliest its
	// conversation can have been; said, what nerdr has typed in to it
	// since, and sessionUnsure, that SessionID is one nerdr gave it that
	// it may not have taken. See claimSession.
	startedAt     time.Time
	said          []string
	sessionUnsure bool

	// scroll is how many lines back into the scrollback the pane with the
	// keyboard is shown.
	scroll int
	err    string

	// paneList is the tab's panes, its own agent first, and on the one
	// with the keyboard; paneBar what their row showed when last drawn.
	// partOf is, on an agent in another's pane, the tab it is in. See
	// panes.go.
	paneList []*paneTab
	on       *paneTab
	paneBar  *barRow
	partOf   *tab

	// info is what the background look at the tab's branches found; see
	// info.go.
	info tabInfo

	// awaitWork is set when a queued message has been sent, until the
	// agent is seen working on it (or sentAt is long past), so the next
	// one is not sent into the same idle moment.
	awaitWork bool
	sentAt    time.Time
	// enterAt is when enter was pressed after it; queuePaused is set when
	// the agent did not take the message up, until the user has looked.
	enterAt     time.Time
	queuePaused bool

	// hook is what herdr's integration in the agent has reported; see
	// hooks.go.
	hook hookAuthority
	// reports is set when the agent was started with an integration that
	// reports to nerdr as it works, heard once anything it ran has reached
	// nerdr's socket, and toldCutOff once the user was told nothing did;
	// see cutOff.
	reports, heard, toldCutOff bool
	// detectAs is the agent the screen is read as, and the one its herdr
	// integration reports as: agents.DetectID, set when it is started;
	// see agentID.
	detectAs string

	// notified is the state last notified of, and when; see notify.go.
	notified   status
	notifiedAt time.Time

	// What was going on when you last left the tab, what its agent did
	// while you were away, and the summary made when you came to it, for
	// H; see attention.go.
	visit   *visit
	away    awayLog
	summary *summary

	// The task wrapped for the panel; whether its agent's log is being
	// read for it; and what is typed to an agent whose log is not read,
	// to take the task from. See panel.go.
	goalWrap    *goalWrap
	goalLooking bool
	typed       *typedLine
	// lastAt is when Last was set from what was typed, so the agent's log,
	// read a moment later, cannot put back the prompt before it.
	lastAt time.Time

	// panelScreen is where the review panel was when you last left the tab
	// (F1 to F5), to go back to when you come to it again.
	panelScreen review.Screen

	// spinoffBusy says what is being made for the part of Spinoff asked
	// about, while it is; see spinoff.go. spinoffSince is when
	// SpinoffReport began to wait for the tabs made.
	spinoffBusy  string
	spinoffSince time.Time

	// usage is what the agent last said of its context window and its
	// account, and when; see usage.go.
	usage   *usage.Report
	usageAt time.Time

	// handoff is the switch of agent waiting for the old agent's handoff,
	// handoffReady a handoff the agent sent with none waiting; see
	// agentswitch.go.
	handoff      *handoffWait
	handoffReady *handoffNote

	// reviewAsk is set when the agent has said its work is ready for a
	// review, to what it wants looked at most and who it asked to review
	// it, until the review starts; see onReady.
	reviewAsk *readyAsk

	// hookID is the id the tab's agent reports to nerdr's socket under;
	// see hookKey.
	hookID string
	// sandboxed is set while the agent runs as its kind's sandboxed
	// setting said when it was started: see confined.
	sandboxed bool

	// review is the code review going on in the tab; reviewOf, on a
	// reviewer, the tab whose work it reviews. See codereview.go.
	review   *reviewRun
	reviewOf *tab
	// reviewDone is a review whose findings wait in F3, to be asked about
	// the next time the tab is the one shown; see askReviewDone.
	reviewDone *reviewDone
}

// hookKey is the id the tab's agent is given to report to nerdr with, as
// HERDR_PANE_ID and NERDR_TAB: a secret made for this run, not the tab's
// id. Every agent can write to the socket, and the tab ids are no secret
// (they name the handoff files beside the notes), so with those an agent
// could report for another tab: plant a handoff its next agent is given,
// or a spinoff, or change the conversation it resumes.
func (t *tab) hookKey() string {
	if t.hookID == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		t.hookID = hex.EncodeToString(b[:])
	}
	return t.hookID
}

// screen is the program in the tab's pane with the keyboard: an agent's or
// a terminal's.
func (t *tab) screen() *term.Pane {
	return t.focused().term()
}

func newTabID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// pins is what the review panel shows for the tab.
func (t *tab) pins() []review.Pin {
	var out []review.Pin
	for _, w := range t.Worktrees {
		out = append(out, review.Pin{Repo: w.Repo, Path: w.Path})
	}
	return out
}

// tabVars are the variables nerdr gives an agent or terminal of a tab.
// Inherited from a nerdr this one runs in, they are left out: an agent is
// to report to this one, and one in a sandbox never to a socket it was
// not given. The user's own, such as NERDR_DEBUG_LOG, are passed on.
var tabVars = []string{"NERDR", "NERDR_SOCKET", "NERDR_TAB", "NERDR_NO_SOCKET", "NERDR_SANDBOXED", "NERDR_OPENCODE_HOOK", "NERDR_AGY_HOOK",
	"NERDR_SHELL", "NERDR_WORKTREES", "NERDR_BIN", "NERDR_DATA_DIR"}

// hostVars are left out of what an agent or terminal of a tab inherits,
// as herdr leaves them out of its panes. The first say nerdr was started
// by an agent, and a tab's agent would take itself for one of its
// subagents, or carry on its session; the rest name the terminal or
// multiplexer nerdr runs in, which a pane is not: an agent that finds
// TMUX or KITTY_WINDOW_ID talks past nerdr to it.
var hostVars = []string{
	"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_MESSAGING_TOKEN", "CODEX_THREAD_ID", "OMPCODE",
	"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID", "ITERM_SESSION_ID",
	"LC_TERMINAL", "LC_TERMINAL_VERSION", "WEZTERM_PANE", "KITTY_WINDOW_ID", "WT_SESSION",
	"TMUX", "TMUX_PANE", "STY", "ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID",
}

// childEnv is the environment an agent runs with.
func childEnv(t *tab) []string {
	env := slices.DeleteFunc(herdr.ChildEnv(os.Environ()), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(tabVars, name) || slices.Contains(hostVars, name)
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
		"NERDR_TAB="+t.hookKey(),
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

// withUserEnv is env with the variables of the agent's config section
// added, all but nerdr's and herdr's own: those tell the agent which tab
// it is in and where to report, and one set wrong would report for another.
func withUserEnv(env, user []string) []string {
	out := append([]string{}, env...)
	for _, kv := range user {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "NERDR") || strings.HasPrefix(name, "HERDR_") {
			continue
		}
		out = append(out, kv) // after the inherited one, so it wins
	}
	return out
}

// start launches the tab's agent: a new conversation, or the one the tab
// had before when resume is set. Finding the conversation to resume can
// mean asking the agent's CLI (opencode), so that part runs in the
// background and the agent is started when launchMsg comes back.
func (m *Model) start(t *tab, resume bool) tea.Cmd {
	t.status = stStarting
	t.err = ""
	beside := t.partOf != nil
	known := t.SessionID != "" || len(t.ResumeArgv) > 0
	if beside && !known || !known && m.sharesKind(t) {
		// The worktree's last conversation could be another agent's in the
		// tab: one not known by its id starts afresh.
		resume = false
	}
	if !resume {
		if t.Agent == agents.Cursor {
			// Its chat is made first, by running it.
			specs, kind, dir := m.specsFor(t), t.Agent, t.Dir
			return func() tea.Msg { return launchMsg{t: t, kind: kind, l: agents.NewSessionIn(specs, kind, dir)} }
		}
		return m.spawn(t, agents.NewSession(m.specsFor(t), t.Agent), false)
	}
	specs, kind, id, dir, skip := m.specsFor(t), t.Agent, t.SessionID, t.Dir, m.othersSessions(t)
	reported := t.ResumeArgv
	return func() tea.Msg {
		if l, ok := agents.ResumeReported(specs, kind, reported); ok {
			return launchMsg{t: t, kind: kind, l: l, resume: true}
		}
		l := agents.Resume(specs, kind, id, dir, skip)
		if beside && l.SessionID != id {
			// Its own conversation is gone: never another one in its place.
			return launchMsg{t: t, kind: kind, l: agents.NewSessionIn(specs, kind, dir)}
		}
		return launchMsg{t: t, kind: kind, l: l, resume: !l.New}
	}
}

// specsFor is how the agents are started for t: the config's, with t's
// own model when it was started with one.
func (m *Model) specsFor(t *tab) map[agents.Kind]agents.Spec {
	return agents.WithModel(m.cfg.Agents, t.Agent, t.Model)
}

// othersSessions are the conversations had in t's worktree that are not
// t's to carry on: its reviewers', and those of the other agents of its
// tab, in each kind they have run as.
func (m *Model) othersSessions(t *tab) map[string]bool {
	skip := codereview.Sessions(t.Dir, string(t.Agent))
	o := t.owner()
	for _, a := range append([]*tab{o}, o.extraAgents()...) {
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

// sharesKind reports whether another agent of t's tab runs as t's kind:
// then the worktree's latest conversation of the kind may be the other's,
// which an agent that reports no id (codex) cannot tell from its own.
func (m *Model) sharesKind(t *tab) bool {
	o := t.owner()
	for _, a := range append([]*tab{o}, o.extraAgents()...) {
		if a != t && a.Agent == t.Agent {
			return true
		}
	}
	return false
}

// carryOn starts a new tab's agent on the conversation it had in the
// tab's worktree before, as resuming does, when there is one: a worktree
// an agent worked in outside nerdr, or in a tab since closed. Looking
// for it reads the agent's logs, so it is done in the background.
func (m *Model) carryOn(t *tab) tea.Cmd {
	t.status = stStarting
	t.err = ""
	specs, kind, dir := m.specsFor(t), t.Agent, t.Dir
	return func() tea.Msg {
		skip := codereview.Sessions(dir, string(kind))
		if agents.HasConversation(specs, kind, dir, skip) {
			l := agents.Resume(specs, kind, "", dir, skip)
			return launchMsg{t: t, kind: kind, l: l, resume: !l.New}
		}
		return launchMsg{t: t, kind: kind, l: agents.NewSessionIn(specs, kind, dir)}
	}
}

// trustWorktrees tells gitsafe which repository each of t's worktrees
// belongs to, so git run there is not steered by what the agent wrote in
// the worktree, and records each one's origin, which a push must keep to.
func trustWorktrees(t *tab) {
	for i := range t.Worktrees {
		workspace.Trust(&t.Worktrees[i])
	}
}

// launchMsg carries a resolved command line for a tab.
type launchMsg struct {
	t      *tab
	kind   agents.Kind // the agent it is for; the tab may have switched since
	l      agents.Launch
	resume bool
	// handoff is the first message for an agent switched to: what the
	// one before it did.
	handoff string
}

// firstAsArgs takes t's first message off its list and returns it, and
// the arguments that start t's agent with it, when that agent takes one so
// (agents.StartPrompt) and the message is to go in by itself. Typed in,
// it could land in copilot while it was still starting and be lost.
func (m *Model) firstAsArgs(t *tab) ([]string, state.Message) {
	if len(t.Messages) == 0 || !t.Messages[0].Auto {
		return nil, state.Message{}
	}
	msg := t.Messages[0]
	args := agents.StartPrompt(t.Agent, msg.Text)
	if args == nil {
		return nil, state.Message{}
	}
	t.dropMessage(0)
	m.setGoal(t, msg.Text)
	m.setLast(t, msg.Text)
	m.saveSession()
	return args, msg
}

// beforePrompt is argv with opts added before the "--" a prompt put on
// the command line follows (see agents.StartPrompt), or at the end when
// there is none: after it, codex takes them for words of the prompt.
// commandLen skips the configured command, whose wrapper may have its
// own "--" before codex's arguments even begin.
func beforePrompt(argv, opts []string, commandLen int) []string {
	start := min(commandLen, len(argv))
	i := slices.Index(argv[start:], "--")
	if i < 0 {
		return append(argv, opts...)
	}
	i += start
	return slices.Concat(argv[:i], opts, argv[i:])
}

// spawn runs the command line in a new pane for the tab.
func (m *Model) spawn(t *tab, l agents.Launch, resume bool) tea.Cmd {
	if l.SessionID != t.SessionID && (l.SessionID != "" || !resume) {
		// A new conversation is the agent's now: with no id from nerdr,
		// none, until it is found (claimSession), not the one before.
		m.setSession(t, l.SessionID)
	}
	if _, err := os.Stat(t.Dir); err != nil {
		t.err = "the worktree is gone: " + t.Dir
		t.status = stExited
		return nil
	}
	m.installProjectSkills(t)
	// Before the first message can go on the command line.
	t.sandboxed = m.sandboxedKind(t.Agent)
	if !t.sandboxed {
		m.holdSandboxed(t)
	}
	reports := false
	if t.Agent == agents.Claude && m.hookSock != "" {
		// Claude says itself when it is working and when it is done; see
		// hooks.ClaudeSettings.
		l.Argv = append(l.Argv, "--settings", hooks.ClaudeSettings(isOn(m.cfg.ClaudeStatusLine)))
		reports = true
	}
	if t.Agent == agents.Codex && m.hookSock != "" && slices.Contains(l.Argv, "--no-daemon") {
		// So does codex, once it trusts nerdr's hooks; see
		// hooks.CodexArgs. Only in a codex of its own: one with the
		// shared server runs hooks with another tab's variables.
		command, _ := agents.Command(m.specsFor(t), t.Agent)
		args := m.codexHookArgs()
		l.Argv = beforePrompt(l.Argv, args, len(command))
		reports = len(args) > 0
	}
	if t.Agent == agents.Copilot && m.hookSock != "" {
		// And copilot, with nerdr's plugin; see hooks.InstallCopilotPlugin.
		if dir := m.copilotPlugin(t); dir != "" && agents.HasFlag(m.cfg.Agents, agents.Copilot, "--plugin-dir") {
			l.Argv = append(l.Argv, "--plugin-dir", dir)
			reports = true
		}
	}
	if t.Agent == agents.Claude {
		l.Argv = append(l.Argv, m.claudeNote(t)...)
	}
	var first []state.Message
	if !resume {
		if args, msg := m.firstAsArgs(t); args != nil {
			first = []state.Message{msg}
			l.Argv = append(l.Argv, args...)
		}
	}
	w, h := m.tabSize(t)
	p, err := term.Start(l.Argv, t.Dir, withUserEnv(m.agentEnv(t), l.Env), w, h, m.notify)
	if err != nil {
		t.Messages = append(first, t.Messages...) // for the next start
		t.err = fmt.Sprintf("could not start %s: %v", l.Argv[0], err)
		t.status = stExited
		return nil
	}
	t.pane = p
	t.startedAt, t.said, t.sessionUnsure = time.Now(), nil, l.Unsure
	t.hook = hookAuthority{} // a new process: its integration reports afresh
	t.reports, t.heard, t.toldCutOff = reports, false, false
	t.detectAs = agents.DetectID(m.cfg.Agents, t.Agent)
	t.resumed = resume
	t.status = stStarting
	t.raw = detect.Unknown
	t.worked = false
	t.err = ""
	t.scroll = 0
	t.changed = time.Now()
	if o := t.owner(); o == m.cur() && o.focused() == t.paneOf() {
		p.Focus(true)
	}
	return nil
}

// worktreeNote is told to claude in every tab, in its system prompt, where
// it holds whatever the conversation is about. A skill is only read when
// its description matches what is asked, and "do it in two branches" did
// not: the agent made them itself, switching the tab's own worktree to
// another branch, which the tab then follows.
const worktreeNote = "You run in a nerdr tab, and this worktree is the tab's: never create or switch branches in it. " +
	"When the user asks for work on new or separate branches, worktrees or tabs, use the nerdr-spinoff skill, which has nerdr make a tab for each once the user confirms its branch and agent; " +
	"nerdr tab new makes tabs without asking, and is only for when the user says not to be asked."

// readyNote is told to claude beside worktreeNote when its turns are
// reviewed once it says the work is ready: for the same reason, as "I am
// done" matches no skill's description well enough to be counted on.
const readyNote = "Code review is on in this tab: when you have finished what you were asked to do and it changed code, " +
	"use the nerdr-request-review skill before you end your turn, so a second agent reviews the work."

// claudeNote is the arguments that give t's claude worktreeNote and
// readyNote: none of either when it cannot use the skill it points to, or
// nerdr is not set up yet, and no readyNote for a reviewer.
func (m *Model) claudeNote(t *tab) []string {
	if m.cfg.Install == "" {
		return nil
	}
	var notes []string
	if m.skillOn(t, "nerdr-spinoff") {
		notes = append(notes, worktreeNote)
	}
	if m.cfg.Review.Auto && m.waitsForReady(t) && t.reviewOf == nil {
		notes = append(notes, readyNote)
	}
	if len(notes) == 0 {
		return nil
	}
	return []string{"--append-system-prompt", strings.Join(notes, "\n\n")}
}

// agentID is the agent t's screen is read as and its integration reports
// as: detectAs, or the tab's kind before its agent has been started.
// screenReading is what an agent's screen was found to say, for the pane,
// the manifest and the snapshot it was read with.
type screenReading struct {
	pane *term.Pane
	id   string
	gen  uint64
	res  detect.Result
}

// readScreen is what the agent's screen says by its manifest. The
// snapshot is read again only once it has changed.
func (t *tab) readScreen() detect.Result {
	in, gen := t.pane.SnapshotGen()
	id := t.agentID()
	if r := t.reading; r.pane == t.pane && r.id == id && r.gen == gen {
		return r.res
	}
	res := detect.For(id).Classify(in)
	t.reading = screenReading{pane: t.pane, id: id, gen: gen, res: res}
	return res
}

func (t *tab) agentID() string {
	if t.detectAs != "" {
		return t.detectAs
	}
	return string(t.Agent)
}

// tabIndex is where t is in the tab row, or -1 once it has been closed.
func (m *Model) tabIndex(t *tab) int {
	for i, x := range m.tabs {
		if x == t {
			return i
		}
	}
	return -1
}

// notify is called by panes when they draw. It must not block: at most one
// wake-up is queued, and the frame loop picks up everything that changed.
func (m *Model) notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// setStatus changes a tab's light and records when.
func (t *tab) setStatus(s status) {
	if t.status != s {
		t.status = s
		t.changed = time.Now()
	}
}

// idleDebounce is how long a working agent must look idle before it is
// shown as idle: agents briefly redraw their prompt between steps.
const idleDebounce = 1200 * time.Millisecond

// A working agent animates something — a spinner, a clock — so one that
// has really stopped also stops drawing. Read off the screen, working turns
// idle only once the screen has been still for quietForIdle as well, or has
// looked idle for idleAnyway (an agent that keeps drawing when idle is not
// kept working for ever).
const (
	quietForIdle = 2500 * time.Millisecond
	idleAnyway   = 20 * time.Second
)

// interruptQuiet is how long the screen of an agent whose hooks said it was
// working must be still, and look idle, to be taken as idle after all: a
// turn interrupted with esc ends without the hook that says so.
const interruptQuiet = 6 * time.Second

// staleBlocked is how long after its hooks said the agent asks something
// the screen is believed if it shows no question: by then the dialog has
// been drawn, so one not shown has been answered or dismissed.
const staleBlocked = 2 * time.Second

// classify reads the tab's screen and updates its light. visible is whether
// the user is looking at the tab right now.
func (t *tab) classify(visible bool, now time.Time) {
	if t.pane == nil {
		if t.status == stStarting || t.status == stAsleep {
			return // not started yet: see start, and sleep.go
		}
		t.setStatus(stExited)
		return
	}
	if exited, _, _ := t.pane.Exited(); exited {
		t.setStatus(stExited)
		return
	}
	if t.Spinoff != nil {
		// nerdr itself is waiting for the user, whatever the agent does:
		// to go through the parts it split off.
		t.setStatus(stBlocked)
		return
	}
	var res detect.Result
	hooked := t.hook.source != ""
	if hooked {
		res.State = t.hookState(now)
		if res.State == detect.Idle && t.raw != detect.Idle {
			t.idleSince = now.Add(-idleAnyway)
		}
	} else {
		man := detect.For(t.agentID())
		res = t.readScreen()
		if res.Skip {
			return
		}
		if res.Rule == detect.FallbackRule && t.raw == detect.Working && man.KnowsIdle() {
			// A screen no rule knows says nothing: an agent that was
			// working is not taken to be done because of it. Unless the
			// manifest has no idle rule, as herdr's opencode has not: then
			// the fallback is the idle screen, and the quiet screen below
			// is what tells a pause from the end of the turn.
			res.State = detect.Working
		}
		if res.State == detect.Idle && t.raw != detect.Idle {
			t.idleSince = now
		}
	}
	t.raw = res.State
	switch res.State {
	case detect.Working:
		t.worked, t.begun = true, true
		t.awaitWork, t.queuePaused = false, false
		t.setStatus(stWorking)
	case detect.Blocked:
		t.awaitWork = false // it has taken the message up: it asks about it
		t.begun = true
		t.setStatus(stBlocked)
	case detect.Idle:
		if t.status == stWorking {
			idleFor := now.Sub(t.idleSince)
			if idleFor < idleDebounce || (idleFor < idleAnyway && t.pane.Quiet() < quietForIdle) {
				return
			}
		}
		if t.worked && !visible {
			t.setStatus(stDone)
		} else if t.status != stDone || visible {
			t.setStatus(stIdle)
		}
		if visible {
			t.worked = false
		}
	default:
		t.setStatus(stUnknown)
	}
}

// hookState is the state of an agent whose integration reports it. The
// report needs no debounce, and says when a turn is over, which the screen
// cannot always tell from a pause between steps. But some changes come
// with no report, and then a screen that clearly shows the state (a rule
// marked visible, as herdr has it) wins:
//
//   - a question on the screen is a question, whatever was reported:
//     claude's trust prompt, an MCP server's, have no hook;
//   - a question reported but gone from the screen has been answered, and
//     the tool runs with no hook until it is done, or dismissed with esc,
//     which ends the turn with no Stop;
//   - work reported, but the screen still and at the prompt: interrupted —
//     unless the turn is over and the agent waits on subagents in the
//     background, as the screen does not show.
//
// An agent with no idle rule (copilot, cursor, agy, …) has no screen that
// clearly shows it idle: there a screen no rule knows, still for as long
// as an interrupted one, stands for it. Esc in copilot ends a turn, or
// dismisses its question, with no hook at all.
func (t *tab) hookState(now time.Time) detect.State {
	state := t.hook.state
	s := t.readScreen()
	stillFallback := s.Rule == detect.FallbackRule && !detect.For(t.agentID()).KnowsIdle() && t.pane.Quiet() >= interruptQuiet
	if s.Skip || !s.Visible && !stillFallback {
		return state
	}
	switch {
	case s.State == detect.Blocked:
		return detect.Blocked
	case state == detect.Blocked && now.Sub(t.hook.at) >= staleBlocked:
		t.hook.state = s.State
		return s.State
	case state == detect.Working && s.State == detect.Idle && t.pane.Quiet() >= interruptQuiet && !t.hook.background:
		t.hook.state = detect.Idle
		return detect.Idle
	}
	return state
}

// seen is called when the user looks at a tab: a finished agent's green
// light has done its job.
func (t *tab) seen() {
	if t.status == stDone {
		t.setStatus(stIdle)
	}
	t.worked = false
}

// attention ranks how much a tab needs the user: blocked first, then
// finished work, then work in progress.
func (t *tab) attention() int {
	switch t.lit() {
	case stBlocked:
		return 3
	case stDone:
		return 2
	case stWorking:
		return 1
	}
	return 0
}

func (s status) label() string {
	switch s {
	case stStarting:
		return "starting"
	case stIdle:
		return "idle"
	case stWorking:
		return "working"
	case stBlocked:
		return "needs input"
	case stDone:
		return "done"
	case stExited:
		return "exited"
	case stAsleep:
		return "asleep"
	}
	return "unknown"
}

// saveSession writes the open tabs so the next start restores them.
func (m *Model) saveSession() {
	s := state.Session{Root: m.root, Active: m.active}
	if t := m.splitTab(); t != nil && t.reviewOf == nil {
		s.Split, s.SplitLeft = t.ID, m.splitLeft
	}
	for _, t := range m.tabs {
		st := t.Tab
		st.Agents = t.savedAgents()
		if w := t.handoff; w != nil {
			// The switch it is for does not outlive nerdr.
			if i := t.findMessage(w.asked); i >= 0 {
				st.Messages = append(append([]state.Message{}, st.Messages[:i]...), st.Messages[i+1:]...)
			}
		}
		s.Tabs = append(s.Tabs, st)
	}
	s.PRs = m.savedPRWatch()
	s.Issues = m.savedIssueWatch()
	s.GroupNames = m.groupNames
	s.SideWidth = m.sideWidth
	if err := state.Save(s); err != nil {
		m.toast("could not save session: " + err.Error())
	}
}
