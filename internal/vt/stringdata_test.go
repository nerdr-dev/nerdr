package vt

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestStringDataAsBefore: with the string data grown as it comes, the
// handlers get what they got from a parser with 4MB set aside: all of a
// string up to that, its start of a longer one, and the same sequences
// after.
func TestStringDataAsBefore(t *testing.T) {
	big := strings.Repeat("A", maxStringData+1000)
	inputs := map[string]string{
		"osc bel":      "\x1b]52;c;" + big + "\x07after\x1b]0;title\x07",
		"osc st":       "\x1b]52;c;" + big + "\x1b\\after\x1b]2;t\x1b\\",
		"osc c1":       "\x1b]52;c;" + big + "\x9c\x1b]0;x\x07",
		"dcs":          "\x1bP1$r" + big + "\x1b\\\x1bPq" + "ok" + "\x1b\\",
		"apc":          "\x1b_G" + big + "\x1b\\\x1b_small\x1b\\",
		"cancelled":    "\x1b]0;" + big + "\x18\x1b]0;next\x07",
		"small ones":   strings.Repeat("\x1b]0;a title\x07text", 100),
		"utf8 in osc":  "\x1b]0;" + strings.Repeat("æøå", maxStringData/4) + "\x07",
		"exactly full": "\x1b]0;" + strings.Repeat("B", maxStringData-2) + "\x07",
	}
	for name, in := range inputs {
		var want, got []string
		record := func(to *[]string) ansi.Handler {
			return ansi.Handler{
				HandleOsc: func(cmd int, data []byte) { *to = append(*to, fmt.Sprintf("osc %d %d %x", cmd, len(data), sum(data))) },
				HandleDcs: func(cmd ansi.Cmd, _ ansi.Params, data []byte) {
					*to = append(*to, fmt.Sprintf("dcs %d %d %x", cmd, len(data), sum(data)))
				},
				HandleApc: func(data []byte) { *to = append(*to, fmt.Sprintf("apc %d %x", len(data), sum(data))) },
				Print:     func(r rune) { *to = append(*to, string(r)) },
			}
		}
		ref := ansi.NewParser()
		ref.SetDataSize(4 << 20)
		ref.SetHandler(record(&want))
		for _, b := range []byte(in) {
			ref.Advance(b)
		}
		e := NewEmulator(80, 24)
		e.parser.SetHandler(record(&got))
		// In pieces, as a pane is fed.
		for b := []byte(in); len(b) > 0; {
			n := min(len(b), 32<<10)
			_, _ = e.Write(b[:n])
			b = b[n:]
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %.300v\nwant %.300v", name, got, want)
		}
	}
}

func sum(b []byte) uint64 {
	var h uint64 = 14695981039346656037
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	return h
}

// TestEmulatorsAreSmall: a terminal sets no room aside for strings that
// have not come, nor keeps a big one's once it is done.
func TestEmulatorsAreSmall(t *testing.T) {
	heap := func() uint64 {
		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before := heap()
	es := make([]*Emulator, 10)
	for i := range es {
		es[i] = NewEmulator(80, 24)
		_, _ = es[i].Write([]byte("\x1b]52;c;" + strings.Repeat("A", 1<<20) + "\x07done"))
	}
	if mb := float64(heap()-before) / (1 << 20); mb > 8 {
		t.Errorf("10 terminals: %.1f MB", mb)
	}
	runtime.KeepAlive(es)
	if !bytes.Contains([]byte(es[0].String()), []byte("done")) {
		t.Error("text after the string")
	}
}
