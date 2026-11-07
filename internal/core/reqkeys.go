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
type Type struct {
	Ref ProgramRef
	B   []byte
}

func (q Type) Do(m *Core) Cmd {
	KeysTo{q.Ref}.Do(m)
	if p := m.Program(q.Ref); p != nil {
		p.Input(q.B)
	}
	return nil
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
type Mouse struct {
	Ref ProgramRef
	Ev  term.MouseEvent
}

func (q Mouse) Do(m *Core) Cmd {
	if p := m.Program(q.Ref); p != nil {
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

// ResizeProgram is program Ref shown W×H.
type ResizeProgram struct {
	Ref  ProgramRef
	W, H int
}

func (q ResizeProgram) Do(m *Core) Cmd {
	if p := m.Program(q.Ref); p != nil {
		p.Resize(q.W, q.H)
	}
	return nil
}
