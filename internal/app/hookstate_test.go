package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

const (
	claudePromptScreen = "● Done.\r\n\r\n" +
		"────────────────────────────────────────\r\n" +
		"❯ \r\n" +
		"────────────────────────────────────────\r\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle)\r\n"
	claudePermissionScreen = "Bash command\r\n  rm -rf build\r\n\r\nDo you want to proceed?\r\n" +
		"❯ 1. Yes\r\n  2. No\r\n\r\nEsc to cancel · Tab to amend\r\n"
	// A turn under way: the title's spinner.
	claudeWorkingScreen = "\x1b]0;◑ Fixing the tests\x07● Running the tests…\r\n"
)

// claudeTab is a claude tab whose pane shows screen, as an integration
// reporting to it would see it.
func claudeTab(t *testing.T, screen, want string) *core.Tab {
	t.Helper()
	p, err := term.Start([]string{"sh", "-c", `printf '%s' "$0"; sleep 60`, screen}, t.TempDir(), nil, 60, 12, func() {})
	if err != nil {
		t.Skip("no sh:", err)
	}
	t.Cleanup(p.Close)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(p.Snapshot().Screen, want) {
		if time.Now().After(deadline) {
			t.Fatalf("not drawn: %q", p.Snapshot().Screen)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "t1", Agent: agents.Claude}, Pane: p}}
}

func reported(tb *core.Tab, s detect.State, ago time.Duration) {
	tb.Hook = core.HookAuthority{Source: hooks.ClaudeSource, State: s, At: time.Now().Add(-ago)}
}

// A question dismissed with esc ends the turn with no Stop: once the
// dialog has had time to be drawn, the prompt box on the screen wins.
func TestHookBlockedGivesWayToThePromptBox(t *testing.T) {
	tb := claudeTab(t, claudePromptScreen, "auto mode")
	reported(tb, detect.Blocked, 0)
	if got := tb.HookState(time.Now()); got != detect.Blocked {
		t.Fatalf("just reported: %v, want blocked (the dialog may not be drawn yet)", got)
	}
	reported(tb, detect.Blocked, 3*time.Second)
	if got := tb.HookState(time.Now()); got != detect.Idle || tb.Hook.State != detect.Idle {
		t.Fatalf("stale: %v (hook %v), want idle", got, tb.Hook.State)
	}
}

// An approved tool runs with no hook until it is done: the agent at work on
// the screen is working, not waiting for an answer.
func TestHookBlockedGivesWayToWork(t *testing.T) {
	tb := claudeTab(t, claudeWorkingScreen, "Running")
	reported(tb, detect.Blocked, 3*time.Second)
	if got := tb.HookState(time.Now()); got != detect.Working {
		t.Fatalf("%v, want working", got)
	}
}

// Work no prompt started has no hook: claude takes its turn up again once
// a command it put in the background is done. The spinner on the screen
// is work, once the Stop it may briefly outlast is a moment old.
func TestHookIdleGivesWayToWork(t *testing.T) {
	tb := claudeTab(t, claudeWorkingScreen, "Running")
	reported(tb, detect.Idle, 0)
	if got := tb.HookState(time.Now()); got != detect.Idle {
		t.Fatalf("just reported: %v, want idle (the title may not be redrawn yet)", got)
	}
	reported(tb, detect.Idle, 3*time.Second)
	tb.Light, tb.Raw = core.StDone, detect.Idle
	tb.Classify(false, time.Now())
	if tb.Light != core.StWorking || tb.Hook.State != detect.Working {
		t.Fatalf("stale: %v (hook %v), want working", tb.Light.Label(), tb.Hook.State)
	}
}

// A dialog no hook reports is a question all the same, for as long as it
// is on the screen; the report stands for when it is gone.
func TestDialogOnScreenOverrulesTheHook(t *testing.T) {
	tb := claudeTab(t, claudePermissionScreen, "proceed")
	reported(tb, detect.Working, 0)
	tb.Classify(true, time.Now())
	if tb.Light != core.StBlocked || tb.Hook.State != detect.Working {
		t.Fatalf("status %v, hook %v", tb.Light, tb.Hook.State)
	}
}

// A `claude -p` the agent runs reports its own start through herdr's
// integration; it is not the tab's conversation. A /clear is.
func TestNestedSessionDoesNotReplaceTheTabs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // the session saved is not the user's
	tb := claudeTab(t, claudePromptScreen, "auto mode")
	tb.SessionID = "main"
	m := &Model{Tabs: []*core.Tab{tb}}
	m.Client = m
	m.OnHook(hooks.Report{Method: hooks.ReportAgentSession, Pane: tb.HookKey(), Source: "herdr:claude", Agent: "claude",
		SessionID: "nested", SessionSource: "startup"})
	if tb.SessionID != "main" {
		t.Fatalf("nested start took over: %q", tb.SessionID)
	}
	m.OnHook(hooks.Report{Method: hooks.ReportAgentSession, Pane: tb.HookKey(), Source: "herdr:claude", Agent: "claude",
		SessionID: "cleared", SessionSource: "clear"})
	if tb.SessionID != "cleared" {
		t.Fatalf("/clear not taken: %q", tb.SessionID)
	}
}

// A custom agent's reported resume command is kept with the tab.
func TestReportedResumeCommandIsKept(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tb := claudeTab(t, claudePromptScreen, "auto mode")
	tb.Agent = "my-agent"
	m := &Model{Tabs: []*core.Tab{tb}}
	m.Client = m
	m.OnHook(hooks.Report{Method: hooks.ReportAgent, Pane: tb.HookKey(), Source: "my-agent", Agent: "my-agent",
		State: "idle", ResumeArgv: []string{"my-agent", "--resume", "s-1"}})
	if strings.Join(tb.ResumeArgv, " ") != "my-agent --resume s-1" {
		t.Fatalf("resume command = %q", tb.ResumeArgv)
	}
}

// A conversation's start may bring its id and its resume command in one
// report; both are taken. A new conversation without a command drops the
// old one's, which would reopen the conversation before.
func TestResumeCommandFollowsTheConversation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tb := claudeTab(t, claudePromptScreen, "auto mode")
	tb.Agent = "my-agent"
	m := &Model{Tabs: []*core.Tab{tb}}
	m.Client = m
	m.OnHook(hooks.Report{Method: hooks.ReportAgent, Pane: tb.HookKey(), Source: "my-agent", Agent: "my-agent",
		State: "idle", SessionID: "s1", SessionSource: "startup", ResumeArgv: []string{"my-agent", "--resume", "s1"}})
	if tb.SessionID != "s1" || strings.Join(tb.ResumeArgv, " ") != "my-agent --resume s1" {
		t.Fatalf("after start: %q, %q", tb.SessionID, tb.ResumeArgv)
	}
	m.OnHook(hooks.Report{Method: hooks.ReportAgentSession, Pane: tb.HookKey(), Source: "my-agent", Agent: "my-agent",
		SessionID: "s2", SessionSource: "clear"})
	if tb.SessionID != "s2" || tb.ResumeArgv != nil {
		t.Fatalf("after /clear: %q, %q", tb.SessionID, tb.ResumeArgv)
	}
}

func TestHerdrNotifies(t *testing.T) {
	for _, c := range []struct {
		before, after string
		want          bool
	}{
		{"working", "idle", true},
		{"blocked", "idle", true},
		{"working", "blocked", true},
		{"", "blocked", true},
		{"unknown", "idle", false}, // herdr says nothing: nerdr must
		{"", "idle", false},
		{"idle", "working", false},
		{"idle", "idle", false},
	} {
		if got := core.HerdrNotifies(c.before, c.after); got != c.want {
			t.Errorf("%q → %q: %v", c.before, c.after, got)
		}
	}
}

// opencode's manifest, like herdr's, has no rule for its idle screen: a
// screen no rule knows is how it looks when done, and must not keep a
// working tab working for ever.
func TestOpenCodeTurnsDoneOnTheFallback(t *testing.T) {
	tb := claudeTab(t, "┃ Ask anything…\r\n", "Ask anything")
	tb.Agent = agents.OpenCode
	tb.Raw, tb.Light, tb.Worked = detect.Working, core.StWorking, true
	now := time.Now()
	tb.Classify(false, now)
	time.Sleep(core.QuietForIdle)
	tb.Classify(false, now.Add(core.IdleDebounce+core.QuietForIdle))
	if tb.Light != core.StDone {
		t.Fatalf("%v, want done", tb.Light.Label())
	}
}

// A report naming a tab by its id, not the secret its agent was given, is
// someone else's: another tab's agent, say.
func TestReportUnderTheTabsIDIsIgnored(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tb := claudeTab(t, claudePromptScreen, "auto mode")
	tb.SessionID = "main"
	m := &Model{Tabs: []*core.Tab{tb}}
	m.Client = m
	m.OnHook(hooks.Report{Method: hooks.ReportAgentSession, Pane: tb.ID, Source: "herdr:claude", Agent: "claude",
		SessionID: "other", SessionSource: "clear"})
	m.OnHandoff(hooks.Report{Method: hooks.Handoff, Pane: tb.ID, Handoff: "planted"})
	if tb.SessionID != "main" || tb.HandoffReady != nil {
		t.Fatalf("taken: session %q, handoff %v", tb.SessionID, tb.HandoffReady)
	}
	if tb.HookKey() == tb.ID || len(tb.HookKey()) != 32 {
		t.Fatalf("hook key %q", tb.HookKey())
	}
}

// Esc in copilot ends a turn, or dismisses its question, with no hook,
// and no rule knows copilot's idle screen: a screen no rule knows, still
// for as long as an interrupted one, ends what was reported. Not for an
// agent with an idle rule, whose unknown screen says nothing.
func TestHookGivesWayToAStillUnknownScreenWithoutIdleRule(t *testing.T) {
	t.Parallel()
	const screen = "nothing any rule knows\r\n"
	working := claudeTab(t, screen, "nothing")
	working.Agent = agents.Copilot
	blocked := claudeTab(t, screen, "nothing")
	blocked.Agent = agents.Copilot
	claude := claudeTab(t, screen, "nothing")
	working.Hook = core.HookAuthority{Source: hooks.CopilotSource, State: detect.Working, At: time.Now()}
	blocked.Hook = core.HookAuthority{Source: hooks.CopilotSource, State: detect.Blocked, At: time.Now()}
	reported(claude, detect.Working, 0)
	if got := working.HookState(time.Now()); got != detect.Working {
		t.Fatalf("just reported: %v, want working", got)
	}
	if got := blocked.HookState(time.Now()); got != detect.Blocked {
		t.Fatalf("just reported: %v, want blocked", got)
	}
	time.Sleep(core.InterruptQuiet + 200*time.Millisecond)
	if got := working.HookState(time.Now()); got != detect.Idle {
		t.Errorf("still: %v, want idle", got)
	}
	if got := blocked.HookState(time.Now()); got != detect.Idle {
		t.Errorf("still: %v, want idle", got)
	}
	if got := claude.HookState(time.Now()); got != detect.Working {
		t.Errorf("claude, which has an idle rule: %v, want working", got)
	}
}

// An agent whose turn is over, waiting on a subagent in the background,
// sits at its prompt with a still screen: that is not an interrupted turn,
// and it stays at work until the hooks say otherwise.
func TestWaitingOnBackgroundSubagentsStaysWorking(t *testing.T) {
	t.Parallel()
	waiting := claudeTab(t, claudePromptScreen, "auto mode")
	interrupted := claudeTab(t, claudePromptScreen, "auto mode")
	m := &Model{Tabs: []*core.Tab{waiting}}
	m.Client = m
	m.OnHook(hooks.Report{Method: hooks.ReportAgent, Pane: waiting.HookKey(), Source: hooks.ClaudeSource, Agent: "claude",
		State: "working", Background: true})
	reported(interrupted, detect.Working, 0)
	time.Sleep(core.InterruptQuiet + 200*time.Millisecond)
	if got := waiting.HookState(time.Now()); got != detect.Working {
		t.Errorf("waiting on a subagent: %v, want working", got)
	}
	if got := interrupted.HookState(time.Now()); got != detect.Idle {
		t.Errorf("interrupted: %v, want idle", got)
	}
}

// A turn over with only a process left running gets a light of its own; the
// agent is not done, and takes up its news on the screen with no hook.
func TestBackgroundProcessHasItsOwnLight(t *testing.T) {
	tb := claudeTab(t, claudePromptScreen, "auto mode")
	reported(tb, detect.Working, 3*time.Second)
	tb.Hook.Background, tb.Hook.Processes = true, 2
	tb.Classify(true, time.Now())
	if tb.Light != core.StBackground {
		t.Fatalf("waiting on a process: %v", tb.Light.Label())
	}
	if got := backgroundLine(tb); got != "2 processes running in the background" {
		t.Fatalf("line: %q", got)
	}
	tb.Hook.Processes = 1
	if got := backgroundLine(tb); got != "1 process running in the background" {
		t.Fatalf("line: %q", got)
	}

	// Waiting on a subagent is the agent at work: yellow, and no line.
	tb.Hook.Processes = 0
	tb.Classify(true, time.Now())
	if tb.Light != core.StWorking || backgroundLine(tb) != "" {
		t.Fatalf("waiting on a subagent: %v, line %q", tb.Light.Label(), backgroundLine(tb))
	}
}

func TestBackgroundProcessGivesWayToWork(t *testing.T) {
	tb := claudeTab(t, claudeWorkingScreen, "Running")
	reported(tb, detect.Working, 0)
	tb.Hook.Background, tb.Hook.Processes = true, 1
	tb.Classify(true, time.Now())
	if tb.Light != core.StBackground {
		t.Fatalf("just reported: %v (the title may not be redrawn yet)", tb.Light.Label())
	}
	reported(tb, detect.Working, 3*time.Second)
	tb.Hook.Background, tb.Hook.Processes = true, 1
	tb.Classify(true, time.Now())
	if tb.Light != core.StWorking || tb.Hook.Processes != 0 {
		t.Fatalf("woken by its news: %v, %d processes", tb.Light.Label(), tb.Hook.Processes)
	}
}

// A dialog on the screen is red, with a process running or not.
func TestBackgroundProcessGivesWayToAQuestion(t *testing.T) {
	tb := claudeTab(t, claudePermissionScreen, "proceed")
	reported(tb, detect.Working, 0)
	tb.Hook.Background, tb.Hook.Processes = true, 1
	tb.Classify(true, time.Now())
	if tb.Light != core.StBlocked {
		t.Fatalf("%v", tb.Light.Label())
	}
}

// Red and yellow win over the new light, and it over a green seen.
func TestBackgroundProcessRanksBelowWork(t *testing.T) {
	r := core.StatusRank(core.StBackground)
	for _, s := range []core.Status{core.StBlocked, core.StDone, core.StWorking} {
		if core.StatusRank(s) <= r {
			t.Errorf("%v does not win over a background process", s.Label())
		}
	}
	if core.StatusRank(core.StIdle) >= r {
		t.Error("idle wins over a background process")
	}
	if !core.StBackground.AtWork() || !core.StWorking.AtWork() || core.StIdle.AtWork() {
		t.Error("atWork")
	}
}

// A tab with only a process left running is not idle to herdr: were the
// active idle tab picked instead, herdr would tell of the turn as done. Work
// in another tab still wins over it.
func TestNeediestTakesABackgroundProcess(t *testing.T) {
	idle := &core.Tab{AgentState: core.Agent{Light: core.StIdle}}
	background := &core.Tab{AgentState: core.Agent{Light: core.StBackground}}
	m := &Model{Tabs: []*core.Tab{idle, background}}
	if i, got := m.Neediest(); got != background || i != 1 {
		t.Fatalf("picked tab %d", i)
	}
	working := &core.Tab{AgentState: core.Agent{Light: core.StWorking}}
	working.Changed = time.Now().Add(-time.Hour)
	background.Changed = time.Now()
	m.Tabs = append(m.Tabs, working)
	if i, got := m.Neediest(); got != working || i != 2 {
		t.Fatalf("with one working: picked tab %d", i)
	}
}
