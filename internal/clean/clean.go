// Package clean makes text an agent wrote safe to draw on the terminal.
//
// Agents write text nerdr shows: why notes, the reading order, a spinoff's
// summary and prompt, a handoff, an integration's message. An escape
// sequence in it would reach the terminal as one — recolouring the screen,
// setting the clipboard (OSC 52), or asking the terminal for an answer it
// then types into nerdr's input, and from there into the tab's agent. So
// control characters are dropped from such text before nerdr keeps it.
package clean

import "strings"

// Text is s without control characters, but for new lines and tabs. A
// carriage return becomes a new line.
func Text(s string) string {
	if strings.IndexFunc(s, control) < 0 {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case control(r):
			return -1
		}
		return r
	}, s)
}

// Line is s on one line, without control characters: new lines and tabs
// become spaces.
func Line(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case control(r):
			return -1
		}
		return r
	}, s)
}

// control is whether r is a control character other than a new line or a
// tab: C0, DEL and C1.
func control(r rune) bool {
	return r != '\n' && r != '\t' && (r < 0x20 || r >= 0x7f && r < 0xa0)
}
