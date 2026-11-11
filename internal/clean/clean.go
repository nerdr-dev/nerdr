// Package clean makes text an agent wrote safe to draw on the terminal.
//
// Agents write text nerdr shows: why notes, the reading order, a spinoff's
// summary and prompt, a handoff, an integration's message. An escape
// sequence in it would reach the terminal as one — recolouring the screen,
// setting the clipboard (OSC 52), or asking the terminal for an answer it
// then types into nerdr's input, and from there into the tab's agent. So
// control characters are dropped from such text before nerdr keeps it.
package clean

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

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

// Marked is s with each character that would not show for what it is
// drawn by mark as its escape, such as \u001b, and how many there were.
// It is for text that is shown to be allowed, a command that will run:
// dropping such characters, as Text does, would show something other
// than what runs. Marked are control characters, new lines and tabs too
// (a caller showing several lines splits s first), invisible format
// characters such as the bidirectional overrides and isolates
// (U+202A-202E, U+2066-2069), zero-width spaces and joiners and the byte
// order mark, the line and paragraph separators, the other characters
// Unicode says to draw as nothing, and bytes that are not UTF-8.
func Marked(s string, mark func(string) string) (string, int) {
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteString(mark(fmt.Sprintf(`\x%02x`, s[i])))
			n++
		case hidden(r):
			b.WriteString(mark(escape(r)))
			n++
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String(), n
}

// hidden is whether r would not show for what it is on a terminal.
func hidden(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp,
		unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector)
}

// escape is r as it is written in a TOML or Go string.
func escape(r rune) string {
	switch r {
	case '\n':
		return `\n`
	case '\t':
		return `\t`
	case '\r':
		return `\r`
	}
	if r > 0xffff {
		return fmt.Sprintf(`\U%08x`, r)
	}
	return fmt.Sprintf(`\u%04x`, r)
}
