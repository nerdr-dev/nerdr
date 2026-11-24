package core

import (
	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// The requests of what the user does in a program's screen: the keys, the
// mouse, focus and size. A client in core's process does these to the
// Program core gives it; one in a process of its own sends them.

func init() {
	registerRequests(KeysTo{}, Type{}, WriteRaw{}, Mouse{}, FocusProgram{}, ResizeProgram{})
}

// KeysTo is the keyboard given to program Ref. What is typed to an agent
// other than Claude is followed from then on, to catch the lines sent (see
// TypedLine): Claude's are read from its log.
type KeysTo struct{ Ref ProgramRef }

func (q KeysTo) Do(m *Core) Cmd {
	if q.Ref.Plugin != "" {
		return nil
	}
	if _, p := m.PaneByRef(q.Ref.Pane); p != nil && p.Kind != PaneShell && p.A != nil &&
		p.A.Agent != agents.Claude && p.A.Typed == nil {
		p.A.Typed = &TypedLine{}
	}
	return nil
}

// Type is B typed into program Ref, as Program.Input takes it.
// User is set when the user typed it, in a client: what the program copies
// just after counts as asked for (see term.Screen.MarkAsked).
type Type struct {
	Ref  ProgramRef
	B    []byte
	User bool `json:",omitempty"`
}

func (q Type) Do(m *Core) Cmd {
	KeysTo{q.Ref}.Do(m)
	if p := m.Program(q.Ref); p != nil {
		if q.User {
			markAsked(p)
		}
		p.Input(q.B)
	}
	return nil
}

// markAsked notes that the user's input reaches p now, if p is one of
// core's own programs.
func markAsked(p any) {
	if a, ok := p.(interface{ MarkAsked() }); ok {
		a.MarkAsked()
	}
}

// userMouse notes, before ev is sent to p, that the user clicked in p, if
// p is told of the click.
func userMouse(p Program, ev term.MouseEvent) {
	if term.IsClick(ev) && p.ReportsMouse(ev) {
		markAsked(p)
	}
}

// WriteRaw is B written to program Ref as it is.
type WriteRaw struct {
	Ref ProgramRef
	B   []byte
}

func (q WriteRaw) Do(m *Core) Cmd {
	if p := m.Program(q.Ref); p != nil {
		p.Write(q.B)
	}
	return nil
}

// Mouse is a mouse event for program Ref, in its screen's cells.
// User is set for what the user did, in a client, as Type's.
type Mouse struct {
	Ref  ProgramRef
	Ev   term.MouseEvent
	User bool `json:",omitempty"`
}

func (q Mouse) Do(m *Core) Cmd {
	if p := m.Program(q.Ref); p != nil {
		if q.User {
			userMouse(p, q.Ev)
		}
		p.SendMouse(q.Ev)
	}
	return nil
}

// FocusProgram tells program Ref its window gained or lost focus.
type FocusProgram struct {
	Ref ProgramRef
	In  bool
}

func (q FocusProgram) Do(m *Core) Cmd {
	if p := m.Program(q.Ref); p != nil {
		p.Focus(q.In)
	}
	return nil
}

// ResizeProgram is program Ref shown W×H in the client. The program gets
// that size unless a viewer of the API was active in it since (see
// views.go).
type ResizeProgram struct {
	Ref  ProgramRef
	W, H int
}

func (q ResizeProgram) Do(m *Core) Cmd {
	if p := m.Program(q.Ref); p != nil {
		clientShows(&m.Views, ClientViewer, q.Ref, q.W, q.H, p, false)
	}
	return nil
}
