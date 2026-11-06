package term

import (
	"github.com/charmbracelet/x/ansi"
)

// The mouse as the program in a pane sees it. nerdr has the real mouse
// (see input.Router); a program that asks for mouse reports gets the ones
// that land on its pane, moved to its own coordinates and encoded the way
// it asked for them.

// MouseKind is what happened to the mouse.
type MouseKind int

const (
	MousePress MouseKind = iota
	MouseRelease
	MouseMotion
)

// MouseEvent is a mouse report for a program, in its pane's cells.
type MouseEvent struct {
	Kind   MouseKind
	Button ansi.MouseButton // ansi.MouseNone for motion with no button held
	X, Y   int
	Shift  bool
	Alt    bool
	Ctrl   bool
}

// mouseModes are the mouse tracking modes a program can turn on, from
// least to most it is told about.
var mouseModes = []ansi.DECMode{
	ansi.ModeMouseX10,         // presses
	ansi.ModeMouseNormal,      // presses and releases
	ansi.ModeMouseButtonEvent, // and motion while a button is held
	ansi.ModeMouseAnyEvent,    // and all motion
}

// setMouseMode records a mouse mode being turned on or off; it reports
// whether m was one.
func (s *Screen) setMouseMode(m ansi.Mode, on bool) bool {
	if m == ansi.ModeMouseExtSgr {
		s.mouseSgr = on
		return true
	}
	for _, mm := range mouseModes {
		if m == mm {
			if s.mouseOn == nil {
				s.mouseOn = map[ansi.DECMode]bool{}
			}
			s.mouseOn[mm] = on
			return true
		}
	}
	return false
}

// mouseLevel is the most the program has asked to be told: an index into
// mouseModes, or -1 when it has not asked for the mouse at all.
func (s *Screen) mouseLevel() int {
	lvl := -1
	for i, mm := range mouseModes {
		if s.mouseOn[mm] {
			lvl = i
		}
	}
	return lvl
}

// WantsMouse reports whether the program has turned mouse reporting on.
func (s *Screen) WantsMouse() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited && s.mouseLevel() >= 0
}

// WantsAllMotion reports whether the program wants to hear about the
// mouse moving with no button held, which nerdr only reports while
// someone does.
func (s *Screen) WantsAllMotion() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited && s.mouseLevel() == 3
}

// SendMouse passes a mouse event to the program, if it asked for events of
// that kind. It reports whether the program wants the mouse at all, so the
// caller knows whether to use the event itself instead.
func (p *Pane) SendMouse(ev MouseEvent) bool {
	p.mu.Lock()
	lvl, sgr := p.mouseLevel(), p.mouseSgr
	exited := p.exited
	p.mu.Unlock()
	if exited || lvl < 0 {
		return false
	}
	wheel := ev.Button >= ansi.MouseWheelUp && ev.Button <= ansi.MouseWheelRight
	switch {
	case ev.Kind == MouseRelease && (lvl == 0 || wheel):
		return true // X10 reports presses only; a wheel has no release
	case ev.Kind == MouseMotion && lvl < 2:
		return true
	case ev.Kind == MouseMotion && ev.Button == ansi.MouseNone && lvl < 3:
		return true
	}
	if ev.X < 0 || ev.Y < 0 {
		return true
	}
	btn := ev.Button
	if ev.Kind == MouseRelease && !sgr {
		btn = ansi.MouseNone // the legacy encoding does not say which
	}
	b := ansi.EncodeMouseButton(btn, ev.Kind == MouseMotion, ev.Shift, ev.Alt, ev.Ctrl)
	if b == 0xff {
		return true
	}
	if sgr {
		p.Write([]byte(ansi.MouseSgr(b, ev.X, ev.Y, ev.Kind == MouseRelease)))
	} else if ev.X < 223 && ev.Y < 223 {
		p.Write([]byte(ansi.MouseX10(b, ev.X, ev.Y)))
	}
	return true
}

// Point is a cell in the pane's scrollback and screen together: Line 0 is
// the oldest line kept, the screen's top row comes after the scrollback.
type Point struct{ Line, Col int }

func (a Point) before(b Point) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
}

// Selection is text chosen with the mouse, from one cell to another as
// text runs: to the end of the first line, whole lines between, and the
// start of the last.
type Selection struct{ From, To Point }

func (s Selection) ordered() (Point, Point) {
	if s.To.before(s.From) {
		return s.To, s.From
	}
	return s.From, s.To
}

// Contains reports whether the cell at col of line is chosen.
func (s Selection) Contains(line, col int) bool {
	a, b := s.ordered()
	if line < a.Line || line > b.Line {
		return false
	}
	if line == a.Line && col < a.Col {
		return false
	}
	if line == b.Line && col > b.Col {
		return false
	}
	return true
}

// AltScreen reports whether the program is on the alternate screen, which
// has no scrollback.
func (s *Screen) AltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.IsAltScreen()
}
