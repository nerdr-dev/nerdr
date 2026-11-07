package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// Status is what an agent's traffic light shows.
type Status int

const (
	StStarting Status = iota
	StIdle
	StWorking
	StBlocked
	StDone // idle after work the user has not looked at yet
	StUnknown
	StExited
	// StAsleep is an agent nerdr stopped, done and seen, while nobody
	// looked at its tab, to carry its conversation on when it is visited.
	StAsleep
	// StBackground is an agent whose turn is over, with commands or monitors
	// it started still running: a test run it takes up again when it ends,
	// or a dev server that never does. Only the light and the text differ:
	// to everything else in nerdr it is an agent at work (see AtWork), not
	// done until the last of them is over.
	StBackground
)

// AtWork reports whether a turn is still going on, in the agent or in what
// it left running.
func (s Status) AtWork() bool { return s == StWorking || s == StBackground }

// Label is how the status is named to the user.
func (s Status) Label() string {
	switch s {
	case StStarting:
		return "starting"
	case StIdle:
		return "idle"
	case StWorking:
		return "working"
	case StBackground:
		return "background process"
	case StBlocked:
		return "needs input"
	case StDone:
		return "done"
	case StExited:
		return "exited"
	case StAsleep:
		return "asleep"
	}
	return "unknown"
}

// AgentState is Agent under a name that leaves t.Agent, on a Tab that has it
// embedded, the kind of agent, as state.Tab has it.
type AgentState = Agent

// Agent is one agent: what is saved of it, its program, and what is known of
// what it is doing.
type Agent struct {
	state.Tab
	Pane *term.Pane `json:"-"`
	// PidStart is when Pane's process started, as hooks.Started has it:
	// what tells it from a process given its id once it has ended. 0 when
	// not known.
	PidStart uint64 `json:"-"`
	// Gen names the program it runs, different for each program started, so
	// that work for one started before is not done to the one running now,
	// not even by an agent since replaced by another with the same ID, as a
	// reviewer can be.
	Gen uint64

	Light Status
	// Raw is the state the screen showed at the last check, before the
	// idle debounce; IdleSince is when it last turned idle.
	Raw       detect.State
	IdleSince time.Time
	// Why is what the last check went by, for the debug log.
	Why string
	// Changed is when Light last changed, to pick the most recent of
	// several agents that need attention.
	Changed time.Time
	// Tasked records that this process has been given work, by a startup
	// prompt, queued message, typed input or a working hook. Startup screen
	// activity alone does not qualify, and Seen does not clear it.
	Tasked bool
	// Worked is set once the agent has been seen working on a task since it was
	// last looked at; only then does going idle mean "done".
	Worked bool
	// Begun is set once the agent has been seen taking up something it
	// was given, working on it, asking about it or answering it, since it
	// was started.
	Begun bool
	// Reading is the last reading of the agent's screen, kept while the
	// screen is the same; see ReadScreen.
	Reading ScreenReading `json:"-"`

	// AwaitWork is set when a queued message has been sent, until the
	// agent is seen working on it (or SentAt is long past), so the next
	// one is not sent into the same idle moment.
	AwaitWork bool
	SentAt    time.Time
	// EnterAt is when enter was pressed after it; QueuePaused is set when
	// the agent did not take the message up, until the user has looked.
	EnterAt     time.Time
	QueuePaused bool

	// Hook is what the integration in the agent has reported.
	Hook HookAuthority
	// InSandbox is set while the agent runs as its kind's sandboxed setting
	// said when it was started: see Confined.
	InSandbox bool
	// DetectAs is the agent the screen is read as, and the one its
	// integration reports as; see AgentID.
	DetectAs string
}

// HookAuthority is what an integration has said about an agent.
type HookAuthority struct {
	Source string // the integration reporting state; "" when none is
	State  detect.State
	At     time.Time // when state was reported
	Msg    string
	Seq    map[string]int64 // the last sequence number taken, per source
	// Background says the agent's turn is over, but it waits on work it
	// started in the background, subagents or commands: its screen is
	// still and at the prompt meanwhile, and says nothing of it.
	Background bool
	// Processes is how many commands and monitors that work is, when it
	// is nothing else: the agent is not at work itself, StBackground.
	Processes int
}
