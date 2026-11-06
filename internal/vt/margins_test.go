package vt

// nerdr's test of its change to DECSTBM in handlers.go. See UPSTREAM.md.

import (
	"strings"
	"testing"
)

// A bottom margin below the screen, as a program sets for a size it had a
// moment before, is the screen's: inserting or deleting lines in it is not
// a panic.
func TestBottomMarginBelowScreen(t *testing.T) {
	for _, s := range []string{"\x1b[3L", "\x1b[3M", "\x1b[3S", "\x1b[3T", "\r\n\r\n\r\n\r\n\r\n\r\n"} {
		e := NewEmulator(20, 5)
		_, _ = e.Write([]byte("top\r\n\x1b[2;8r\x1b[2;1H" + s + "x"))
		if !strings.HasPrefix(e.String(), "top") {
			t.Errorf("%q: the line above the region moved: %q", s, e.String())
		}
	}
}
