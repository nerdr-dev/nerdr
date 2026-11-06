package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"
)

// Program is what a client may do with a program core runs, an agent's, a
// terminal's or a plugin's screen: draw it from its frames, send it what the
// user types and does with the mouse, and say how big it is shown and
// whether it has the keyboard. A client gets one by its ProgramRef, from
// Core.Program, and keeps none: the program is core's.
type Program interface {
	DrawGen() uint64
	FrameInto(f *term.Frame, scroll int)
	Frame(scroll int) term.Frame
	Rows(from, to int) (int, [][]term.Cell)
	TopAt(scroll int) int
	ScrollbackLen() int
	AltScreen() bool

	Input(b []byte)
	// Write is b as it is, with nothing put in or around it.
	Write(b []byte)
	SendMouse(ev term.MouseEvent) bool
	WantsAllMotion() bool
	Focus(in bool)
	Resize(w, h int)
	Size() (int, int)

	// TakeCopied is what the program last put on the clipboard, once.
	TakeCopied() string
	Exited() (bool, error, time.Duration)
	// Busy reports whether a terminal's shell runs a command.
	Busy() bool
}

var _ Program = (*term.Pane)(nil)

// ProgramRef names a program: the one in a pane, or a plugin's screen by its
// PluginUIKey.
type ProgramRef struct {
	Pane   PaneRef
	Plugin string
}

// Program is the program r names, nil when there is none running there.
func (m *Core) Program(r ProgramRef) Program {
	var tp *term.Pane
	if r.Plugin != "" {
		if u := m.PluginUIs[r.Plugin]; u != nil {
			tp = u.Pane
		}
	} else if _, p := m.PaneByRef(r.Pane); p != nil {
		tp = p.Term()
	}
	if tp == nil {
		return nil // not a nil *term.Pane in a Program
	}
	return tp
}
