// Package core is what nerdr does whoever is looking: the agents, what state
// they are in, and what is to happen to them next. It draws nothing and knows
// of no user interface; see TestCoreHasNoUserInterface. A client, today's TUI,
// shows what is here and passes on what the user does.
package core

import "time"

// Event is something that has happened, for the client's event loop to pass
// back in.
type Event any

// Cmd is work to do away from the event loop, and the Event that says how it
// went; nil is nothing to do.
type Cmd func() Event

// BatchEvent is several Cmds to run side by side.
type BatchEvent []Cmd

// Batch runs cmds side by side; a nil one is left out.
func Batch(cmds ...Cmd) Cmd {
	var out []Cmd
	for _, c := range cmds {
		if c != nil {
			out = append(out, c)
		}
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	}
	return func() Event { return BatchEvent(out) }
}

// Tick is what fn makes of the time, d from now.
func Tick(d time.Duration, fn func(time.Time) Event) Cmd {
	return func() Event {
		time.Sleep(d)
		return fn(time.Now())
	}
}

// After is ev, d from now.
func After(d time.Duration, ev Event) Cmd {
	return func() Event {
		time.Sleep(d)
		return ev
	}
}
