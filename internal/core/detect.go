package core

import (
	"fmt"
	"time"

	"github.com/nerdr-dev/nerdr/internal/detect"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// ScreenReading is what an agent's screen was found to say, for the pane,
// the manifest and the snapshot it was read with.
type ScreenReading struct {
	pane *term.Pane
	id   string
	gen  uint64
	Res  detect.Result
}

// ReadScreen is what the agent's screen says by its manifest. The
// snapshot is read again only once it has changed.
func (a *Agent) ReadScreen() detect.Result {
	in, gen := a.Pane.SnapshotGen()
	id := a.AgentID()
	if r := a.Reading; r.pane == a.Pane && r.id == id && r.gen == gen {
		return r.Res
	}
	res := detect.For(id).Classify(in)
	a.Reading = ScreenReading{pane: a.Pane, id: id, gen: gen, Res: res}
	return res
}

func (a *Agent) AgentID() string {
	if a.DetectAs != "" {
		return a.DetectAs
	}
	return string(a.Agent)
}

// SetStatus changes a tab's light and records when.
func (a *Agent) SetStatus(s Status) {
	if a.Light != s {
		a.Light = s
		a.Changed = time.Now()
	}
}

// IdleDebounce is how long a working agent must look idle before it is
// shown as idle: agents briefly redraw their prompt between steps.
const IdleDebounce = 1200 * time.Millisecond

// A working agent animates something — a spinner, a clock — so one that
// has really stopped also stops drawing. Read off the screen, working turns
// idle only once the screen has been still for QuietForIdle as well, or has
// looked idle for IdleAnyway (an agent that keeps drawing when idle is not
// kept working for ever).
const (
	QuietForIdle = 2500 * time.Millisecond
	IdleAnyway   = 20 * time.Second
)

// InterruptQuiet is how long the screen of an agent whose hooks said it was
// working must be still, and look idle, to be taken as idle after all: a
// turn interrupted with esc ends without the hook that says so.
const InterruptQuiet = 6 * time.Second

// StaleBlocked is how long after its hooks said the agent asks something
// the screen is believed if it shows no question: by then the dialog has
// been drawn, so one not shown has been answered or dismissed.
const StaleBlocked = 2 * time.Second

// StaleIdle is how long after its hooks said the turn is over the screen
// is believed if it shows work: the title's spinner can outlast the Stop
// that ends the turn by a moment.
const StaleIdle = 2 * time.Second

// Classify reads the tab's screen and updates its light. visible is whether
// the user is looking at the tab right now.
func (a *Agent) Classify(visible bool, now time.Time) {
	if a.Pane == nil {
		if a.Light == StStarting || a.Light == StAsleep {
			return // not started yet: see start, and sleep.go
		}
		a.SetStatus(StExited)
		return
	}
	if exited, _, _ := a.Pane.Exited(); exited {
		a.SetStatus(StExited)
		return
	}
	if a.Spinoff != nil {
		// nerdr itself is waiting for the user, whatever the agent does:
		// to go through the parts it split off.
		a.SetStatus(StBlocked)
		return
	}
	var res detect.Result
	hooked := a.Hook.Source != ""
	if hooked {
		res.State = a.HookState(now)
		r := a.Reading.Res
		a.Why = fmt.Sprintf("hook %v %s ago, screen %v by %s, quiet %s", a.Hook.State,
			now.Sub(a.Hook.At).Round(time.Second), r.State, r.Rule, a.Pane.Quiet().Round(100*time.Millisecond))
		if res.State == detect.Idle && a.Raw != detect.Idle {
			a.IdleSince = now.Add(-IdleAnyway)
		}
	} else {
		man := detect.For(a.AgentID())
		res = a.ReadScreen()
		if res.Skip {
			return
		}
		a.Why = fmt.Sprintf("screen %v by %s, quiet %s", res.State, res.Rule, a.Pane.Quiet().Round(100*time.Millisecond))
		if res.Rule == detect.FallbackRule && a.Raw == detect.Working && man.KnowsIdle() {
			// A screen no rule knows says nothing: an agent that was
			// working is not taken to be done because of it. Unless the
			// manifest has no idle rule, as herdr's opencode has not: then
			// the fallback is the idle screen, and the quiet screen below
			// is what tells a pause from the end of the turn.
			res.State = detect.Working
		}
		if res.State == detect.Idle && a.Raw != detect.Idle {
			a.IdleSince = now
		}
	}
	if a.AwaitWork || a.Pane.Inputs() > 0 {
		a.Tasked = true
	}
	a.Raw = res.State
	switch res.State {
	case detect.Working:
		// Agents animate during startup too. Only work after a task
		// was given can leave an unseen result for the user.
		if a.Tasked {
			a.Worked = true
		}
		a.Begun = true
		if hooked && a.Hook.Processes > 0 {
			// The turn is over, and only what it left running goes on:
			// that is not the agent taking up a message sent from the
			// queue, nor a reason to carry on with a queue a stall paused.
			// The prompt it takes reports work with no processes.
			a.SetStatus(StBackground)
		} else {
			a.AwaitWork, a.QueuePaused = false, false
			a.SetStatus(StWorking)
		}
	case detect.Blocked:
		a.AwaitWork = false // it has taken the message up: it asks about it
		a.Begun = true
		a.SetStatus(StBlocked)
	case detect.Idle:
		if a.Light == StWorking {
			idleFor := now.Sub(a.IdleSince)
			if idleFor < IdleDebounce || (idleFor < IdleAnyway && a.Pane.Quiet() < QuietForIdle) {
				return
			}
		}
		if a.Worked && !visible {
			a.SetStatus(StDone)
		} else if a.Light != StDone || visible {
			a.SetStatus(StIdle)
		}
		if visible {
			a.Worked = false
		}
	default:
		a.SetStatus(StUnknown)
	}
}

// HookState is the state of an agent whose integration reports it. The
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
//     background, as the screen does not show;
//   - the turn reported over, but the screen at work: the agent has taken
//     up work no prompt started, and so no hook reports — claude's, once
//     a command it put in the background is done. The same with commands
//     still running: the news of one that ended, with others going on.
//
// An agent with no idle rule (copilot, cursor, agy, …) has no screen that
// clearly shows it idle: there a screen no rule knows, still for as long
// as an interrupted one, stands for it. Esc in copilot ends a turn, or
// dismisses its question, with no hook at all.
func (a *Agent) HookState(now time.Time) detect.State {
	state := a.Hook.State
	s := a.ReadScreen()
	stillFallback := s.Rule == detect.FallbackRule && !detect.For(a.AgentID()).KnowsIdle() && a.Pane.Quiet() >= InterruptQuiet
	if s.Skip || !s.Visible && !stillFallback {
		return state
	}
	switch {
	case s.State == detect.Blocked:
		return detect.Blocked
	case state == detect.Blocked && now.Sub(a.Hook.At) >= StaleBlocked:
		a.Hook.State = s.State
		return s.State
	case state == detect.Working && s.State == detect.Idle && a.Pane.Quiet() >= InterruptQuiet && !a.Hook.Background:
		a.Hook.State = detect.Idle
		return detect.Idle
	case state == detect.Working && a.Hook.Processes > 0 && s.State == detect.Working && now.Sub(a.Hook.At) >= StaleIdle:
		a.Hook.Processes, a.Hook.Background = 0, false
		return detect.Working
	case state == detect.Idle && s.State == detect.Working && now.Sub(a.Hook.At) >= StaleIdle:
		a.Hook.State = detect.Working
		return detect.Working
	}
	return state
}

// Seen is called when the user looks at a tab: a finished agent's green
// light has done its job.
func (a *Agent) Seen() {
	if a.Light == StDone {
		a.SetStatus(StIdle)
	}
	a.Worked = false
}
