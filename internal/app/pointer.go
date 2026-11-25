package app

import "os"

// The pointer's shape: a hand over what a click does something to, the
// terminal's own pointer elsewhere. The terminal is asked with OSC 22,
// which kitty, foot, WezTerm, Ghostty and xterm know; the others pass it
// by and keep their pointer as it was. It is asked only when the shape
// should change, not at every move of the pointer.

const (
	pointerHand    = "\x1b]22;pointer\x1b\\"
	pointerDefault = "\x1b]22;default\x1b\\"
)

// PointerReset gives the terminal its own pointer back; it is written
// once nerdr has stopped drawing, in case it was left a hand.
const PointerReset = pointerDefault

// setPointer is how the shape reaches the terminal; tests put their own.
var setPointer = func(seq string) { _, _ = os.Stdout.WriteString(seq) }

// area is a zone (see zones.go) that takes the mouse without being a
// thing to click: a dialog's box, text to select. The pointer is no hand
// over it.
func (m *Model) area(s string, fn zoneFn) string {
	s = m.zone(s, fn)
	m.zoneAreas[len(m.zoneAreas)-1] = true
	return s
}

// handAt reports whether the pointer at (x, y) is over a thing to click.
func (m *Model) handAt(x, y int) bool {
	id := m.zoneAt(x, y)
	return id >= 0 && (id >= len(m.zoneAreas) || !m.zoneAreas[id])
}

// syncPointer gives the pointer the shape for where it is on the screen
// just drawn. It is done as each screen is drawn, not as the pointer
// moves, for the screen can change under a pointer that stays put: a menu
// closed with Esc, a tab opened. A hand only while nerdr follows every
// movement of the pointer, for elsewhere it would not know to put it
// back, and never over an editor. Otherwise, while a drag goes on it
// stays as it was, and is put right when the drag ends.
func (m *Model) syncPointer() {
	if m.mouse != mouseAll || m.editor != nil {
		m.pointHand(false)
		return
	}
	if m.drag != dragNone {
		return
	}
	m.pointHand(m.hover.seen && m.handAt(m.hover.x, m.hover.y))
}

// pointHand makes the pointer a hand, or the terminal's own, if it is not
// that already.
func (m *Model) pointHand(hand bool) {
	if hand == m.hand {
		return
	}
	m.hand = hand
	if hand {
		setPointer(pointerHand)
	} else {
		setPointer(pointerDefault)
	}
}
