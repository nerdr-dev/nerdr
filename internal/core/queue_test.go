package core

import (
	"os"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// silent is an agent whose program draws nothing and echoes nothing, at
// its idle prompt with a screen that has been still long enough.
func silent(t *testing.T, msgs ...state.Message) *Agent {
	t.Helper()
	p, err := term.Start([]string{"sh", "-c", "stty -echo; sleep 30"}, t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	a := &Agent{Tab: state.Tab{ID: "a", Messages: msgs}, Pane: p, Gen: 1, Light: StIdle, Raw: detect.Idle}
	time.Sleep(QueueQuiet + 100*time.Millisecond)
	return a
}

// TestQueueTypesOneMessageAtATime goes through a whole message with no
// event loop: ready, typed in, enter pressed by the Event that comes back,
// nothing more typed in until the agent has taken it up.
func TestQueueTypesOneMessageAtATime(t *testing.T) {
	a := silent(t, state.Message{Text: "first", Auto: true}, state.Message{Text: "kept"}, state.Message{Text: "next", Auto: true})
	if !a.ReadyForMessage(false) {
		t.Fatal("not ready")
	}
	if a.ReadyForMessage(true) {
		t.Fatal("ready though something holds the queue")
	}
	i := a.NextAuto()
	if a.Messages[i].Text != "first" || a.AutoCount() != 2 {
		t.Fatalf("next %d of %+v", i, a.Messages)
	}
	a.DropMessage(i)
	now := time.Now()
	ev, ok := a.TypeMessage("first", now)().(SendEnter)
	if !ok || ev.ID != "a" || ev.Gen != 1 {
		t.Fatalf("event %+v", ev)
	}
	if !a.AwaitWork || !a.EnterAt.IsZero() || a.ReadyForMessage(false) {
		t.Fatalf("after typing: await %v, enter %v", a.AwaitWork, a.EnterAt)
	}
	a.PressEnter(ev.Gen, now)
	if !a.EnterAt.Equal(now) {
		t.Fatal("enter not pressed")
	}
}

// TestPressEnterIsForTheProgramItWasTypedInTo: an enter still on its way
// when the agent is started again does not reach the new program.
func TestPressEnterIsForTheProgramItWasTypedInTo(t *testing.T) {
	a := silent(t)
	before := a.Pane.Inputs()
	a.PressEnter(a.Gen-1, time.Now())
	if a.Pane.Inputs() != before || !a.EnterAt.IsZero() {
		t.Fatal("pressed in the program started since")
	}
}

// TestStalledMessageStopsTheQueue: an agent that has drawn nothing long
// after enter has not taken the message; that is said once, and the rest
// waits for the user.
func TestStalledMessageStopsTheQueue(t *testing.T) {
	a := silent(t, state.Message{Text: "next", Auto: true})
	now := time.Now()
	a.AwaitWork, a.EnterAt = true, now
	if a.CheckSent(now.Add(StallAfter - time.Second)) {
		t.Fatal("stalled too soon")
	}
	if !a.CheckSent(now.Add(StallAfter)) || !a.QueuePaused || a.AwaitWork {
		t.Fatalf("not stalled: paused %v, await %v", a.QueuePaused, a.AwaitWork)
	}
	if a.CheckSent(now.Add(2 * StallAfter)) {
		t.Fatal("said twice")
	}
	if a.ReadyForMessage(false) {
		t.Fatal("the queue goes on after a stall")
	}
}

// hooked is silent with a hook that has reported it idle at its prompt,
// and msg typed in and enter pressed at the time it returns.
func hooked(t *testing.T, msg string) (*Agent, time.Time) {
	t.Helper()
	a := silent(t)
	now := time.Now()
	a.Hook = HookAuthority{Source: hooks.ClaudeSource, State: detect.Idle, At: now.Add(-time.Minute)}
	a.TypeMessage(msg, now)
	a.PressEnter(a.Gen, now)
	return a, now
}

// TestUnsentPromptGetsEnterAgain: claude drops an enter that comes too soon
// after it starts, the prompt left in its input box, drawn there. An agent
// with a hook has taken a prompt only once the hook says so; till then it
// gets enter again, and in the end the prompt counts as not sent.
func TestUnsentPromptGetsEnterAgain(t *testing.T) {
	a, now := hooked(t, "a long prompt")
	inputs := a.Pane.Inputs()
	pressed := func() uint64 { return a.Pane.Inputs() - inputs }
	for _, c := range []struct {
		after time.Duration
		times uint64
	}{
		{time.Second, 0},
		{EnterAgainAfter, 1},
		{EnterAgainAfter + time.Second/2, 1},
		{2 * EnterAgainAfter, 2},
		{3 * EnterAgainAfter, 2}, // no more than EnterAgainMax
		{StallAfter, 2},          // drawing or not, no stall yet
	} {
		if a.CheckSent(now.Add(c.after)) || pressed() != c.times {
			t.Fatalf("after %s: enter pressed %d more times, want %d", c.after, pressed(), c.times)
		}
	}
	if !a.CheckSent(now.Add(UnsentAfter)) || !a.Unsent || !a.QueuePaused || a.AwaitWork || a.Begun {
		t.Fatalf("not unsent: unsent %v, paused %v, await %v, begun %v", a.Unsent, a.QueuePaused, a.AwaitWork, a.Begun)
	}
	if a.CheckSent(now.Add(2 * UnsentAfter)) {
		t.Fatal("said twice")
	}
}

// TestPromptTakenUpByTheHook: the hook's word that the agent took the
// prompt is what counts, and no enter follows it; nor does one go to an
// agent no longer at its prompt.
func TestPromptTakenUpByTheHook(t *testing.T) {
	a, now := hooked(t, "a prompt")
	inputs := a.Pane.Inputs()
	a.Light = StWorking
	if a.CheckSent(now.Add(EnterAgainAfter)); a.Pane.Inputs() != inputs || !a.AwaitWork {
		t.Fatal("enter pressed again while not at the prompt")
	}
	a.Light = StIdle
	a.Hook.WorkingAt = now.Add(time.Second)
	if a.CheckSent(now.Add(2*EnterAgainAfter)) || a.AwaitWork || !a.Begun || a.Pane.Inputs() != inputs {
		t.Fatalf("not taken up: await %v, begun %v", a.AwaitWork, a.Begun)
	}
}

// TestCommandIsReadOffTheScreen: a slash command may run with no prompt
// hook, so whether it was taken is read off the screen as for an agent
// with no hook.
func TestCommandIsReadOffTheScreen(t *testing.T) {
	a, now := hooked(t, "/compact")
	inputs := a.Pane.Inputs()
	if a.CheckSent(now.Add(EnterAgainAfter)); a.Pane.Inputs() != inputs {
		t.Fatal("enter pressed again after a command")
	}
	if !a.CheckSent(now.Add(StallAfter)) || a.Unsent || !a.QueuePaused {
		t.Fatalf("a silent agent: unsent %v, paused %v", a.Unsent, a.QueuePaused)
	}
}

// TestOthersIntegrationIsReadOffTheScreen: an integration not nerdr's own
// is not known to report a prompt taken, so the screen says.
func TestOthersIntegrationIsReadOffTheScreen(t *testing.T) {
	a := silent(t)
	now := time.Now()
	a.Hook = HookAuthority{Source: "someone:else", State: detect.Idle, At: now.Add(-time.Minute)}
	a.TypeMessage("a prompt", now)
	a.PressEnter(a.Gen, now)
	inputs := a.Pane.Inputs()
	if a.CheckSent(now.Add(EnterAgainAfter)); a.Pane.Inputs() != inputs {
		t.Fatal("enter pressed again")
	}
	if !a.CheckSent(now.Add(StallAfter)) || a.Unsent {
		t.Fatalf("a silent agent: unsent %v, paused %v", a.Unsent, a.QueuePaused)
	}
}

// TestQueueDeliversToAnAgentAtItsPromptInTheBackground: an agent whose turn
// is over, with only commands it left running going on (a dev server that
// never ends), takes the next message once its screen shows the prompt;
// one at work does not.
func TestQueueDeliversToAnAgentAtItsPromptInTheBackground(t *testing.T) {
	a := silent(t, state.Message{Text: "next", Auto: true})
	a.Light, a.Raw = StBackground, detect.Working
	a.Hook = HookAuthority{Source: "s", State: detect.Working, At: time.Now(), Background: true, Processes: 1}
	a.Reading = ScreenReading{pane: a.Pane, Res: detect.Result{State: detect.Idle}}
	if !a.ReadyForMessage(false) {
		t.Fatal("not ready at the prompt with a command in the background")
	}
	if a.ReadyForMessage(true) {
		t.Fatal("ready though something holds the queue")
	}
	a.Reading.Res.State = detect.Working
	if a.ReadyForMessage(false) {
		t.Fatal("ready in the background with the screen at work")
	}
	a.Reading.Res.State = detect.Idle
	a.Light = StWorking
	if a.ReadyForMessage(false) {
		t.Fatal("ready while working")
	}
}

// TestBackgroundIsNotTakingTheMessageUp: an agent in the background, sent a
// message, has not taken it up until it works on it; nor does a paused
// queue carry on because of what it left running.
func TestBackgroundIsNotTakingTheMessageUp(t *testing.T) {
	a := silent(t)
	now := time.Now()
	a.Hook = HookAuthority{Source: "s", State: detect.Working, At: now, Background: true, Processes: 1}
	a.AwaitWork, a.QueuePaused = true, true
	if a.Classify(false, now); a.Light != StBackground || !a.AwaitWork || !a.QueuePaused {
		t.Fatalf("background: %s, await %v, paused %v", a.Light.Label(), a.AwaitWork, a.QueuePaused)
	}
	a.QueuePaused = false
	a.Hook = HookAuthority{Source: "s", State: detect.Working, At: now}
	if a.Classify(false, now); a.Light != StWorking || a.AwaitWork {
		t.Fatalf("working: %s, await %v", a.Light.Label(), a.AwaitWork)
	}
}

func TestPasteCannotEndItself(t *testing.T) {
	got := string(Paste("a\r\nb\x1b[201~\rc"))
	if want := "\x1b[200~a\nb[201~\nc\x1b[201~"; got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}

func TestClassifyWithNoProgram(t *testing.T) {
	for _, c := range []struct{ from, want Status }{
		{StStarting, StStarting},
		{StAsleep, StAsleep},
		{StIdle, StExited},
	} {
		a := &Agent{Light: c.from}
		if a.Classify(false, time.Now()); a.Light != c.want {
			t.Errorf("from %s: %s, want %s", c.from.Label(), a.Light.Label(), c.want.Label())
		}
	}
}

// TestClassifyFollowsAHook: with an integration reporting, its word goes
// with no debounce; going idle unseen after work is done, seen it is idle.
func TestClassifyFollowsAHook(t *testing.T) {
	a := silent(t)
	now := time.Now()
	a.Hook = HookAuthority{Source: "s", State: detect.Working, At: now}
	a.Tasked = true // OnHook records a working report
	if a.Classify(false, now); a.Light != StWorking || !a.Worked || !a.Begun {
		t.Fatalf("working: %s, worked %v, begun %v", a.Light.Label(), a.Worked, a.Begun)
	}
	a.Hook.State = detect.Blocked
	a.AwaitWork = true
	if a.Classify(false, now); a.Light != StBlocked || a.AwaitWork {
		t.Fatalf("blocked: %s, await %v", a.Light.Label(), a.AwaitWork)
	}
	a.Hook.State = detect.Idle
	a.Classify(false, now.Add(time.Minute))
	if a.Light != StDone {
		t.Fatalf("idle unseen: %s", a.Light.Label())
	}
	a.Seen()
	if a.Light != StIdle || a.Worked {
		t.Fatalf("seen: %s, worked %v", a.Light.Label(), a.Worked)
	}
}

// TestClassifyWaitingOnSpinoffIsBlocked: nerdr itself is waiting for the
// user then, whatever the agent does.
func TestClassifyWaitingOnSpinoffIsBlocked(t *testing.T) {
	a := silent(t)
	a.Spinoff = &spinoff.Pending{}
	if a.Classify(false, time.Now()); a.Light != StBlocked {
		t.Fatalf("%s", a.Light.Label())
	}
}
