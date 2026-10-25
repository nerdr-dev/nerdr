// Package input splits what is typed between the agent in the active tab
// and nerdr itself.
//
// While an agent has focus, keystrokes are passed to it byte for byte, the
// way tmux or herdr would: re-encoding them through a key parser would lose
// what the parser does not know (shift+enter, a paste's exact bytes, keys
// in modes the parser never enabled). Only the prefix key is taken out; it
// hands the next key to nerdr as a command. Mouse reports are taken out
// too: they are in nerdr's coordinates, so nerdr decides what was clicked
// and passes on, translated, what lands on an agent that asked for the
// mouse. Everything else — dialogs, the review panel — reads keys through
// bubbletea as usual.
package input

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// PrefixMsg is sent to the program when the prefix key is pressed while
// input is passing through to an agent. The key after it arrives as an
// ordinary tea.KeyMsg.
type PrefixMsg struct{}

// Router reads the terminal and routes it.
type Router struct {
	src    io.Reader
	prefix byte

	// pass is true while input goes straight to the sink.
	pass atomic.Bool
	// fkeys is true while F1-F5 go to the sink with the rest: a shell or
	// an editor has the keyboard, and the program in it may want them.
	fkeys atomic.Bool
	// last is when the terminal last sent anything, in Unix nanoseconds.
	last atomic.Int64

	mu   sync.Mutex
	sink func([]byte)
	send func(any)

	pr *io.PipeReader
	pw *io.PipeWriter

	// routeMu makes route and flushHeld take turns. held is the start of
	// an escape sequence the router may have to take out, cut off at the
	// end of a read; it waits a moment for the rest to arrive.
	routeMu sync.Mutex
	held    []byte
	timer   *time.Timer
	// mouseCut is when a mouse report cut off at the end of a read waited
	// in vain for its rest: should the rest come soon after all, at the
	// start of the next read, it is dropped with it.
	mouseCut time.Time
}

// holdFor is how long an escape sequence cut off at the end of a read
// waits for its rest. A terminal writes a sequence at once, so the rest is
// normally already in the next read.
const holdFor = 20 * time.Millisecond

// mouseCutFor is how long after a cut-off mouse report gave up waiting its
// rest is still taken for it, rather than for keys typed.
const mouseCutFor = 500 * time.Millisecond

// New makes a router over src (the terminal) with the given prefix byte.
func New(src io.Reader, prefix byte) *Router {
	r := &Router{src: src, prefix: prefix}
	r.pr, r.pw = io.Pipe()
	return r
}

// Reader is what bubbletea should read its input from.
func (r *Router) Reader() io.Reader { return r.pr }

// SetSend gives the router a way to post messages to the program.
func (r *Router) SetSend(send func(any)) {
	r.mu.Lock()
	r.send = send
	r.mu.Unlock()
}

// Passthrough sends input straight to sink until called again; a nil sink
// sends everything to bubbletea.
func (r *Router) Passthrough(sink func([]byte)) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
	r.pass.Store(sink != nil)
}

// PassFKeys sends F1-F5 on to the sink as well, rather than to the
// program, until called again with false.
func (r *Router) PassFKeys(pass bool) { r.fkeys.Store(pass) }

// PassesFKeys reports whether F1-F5 go on to the sink.
func (r *Router) PassesFKeys() bool { return r.fkeys.Load() }

// LastInput is when the terminal last sent anything: a key, a click, the
// mouse moved over it. Zero before it has.
func (r *Router) LastInput() time.Time {
	if n := r.last.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Time{}
}

// Run copies input until src fails. Call it in its own goroutine.
func (r *Router) Run() {
	buf := make([]byte, 16*1024)
	for {
		n, err := r.src.Read(buf)
		if n > 0 {
			r.last.Store(time.Now().UnixNano())
			r.route(buf[:n])
		}
		if err != nil {
			_ = r.pw.CloseWithError(err)
			return
		}
	}
}

func (r *Router) route(b []byte) {
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	if len(r.held) > 0 {
		b = append(r.held, b...)
		r.held = nil
		r.timer.Stop()
	}
	if !r.mouseCut.IsZero() {
		late := time.Since(r.mouseCut) > mouseCutFor
		r.mouseCut = time.Time{}
		if n := mouseParams(b); !late && n < len(b) && (b[n] == 'M' || b[n] == 'm') {
			b = b[n+1:]
		}
	}
	for len(b) > 0 {
		r.mu.Lock()
		sink, send := r.sink, r.send
		r.mu.Unlock()
		if !r.pass.Load() || sink == nil {
			r.toProgram(b)
			return
		}

		// Focus reports are nerdr's: they say whether herdr (or the
		// terminal) has given this window focus. Mouse reports are nerdr's
		// too; see the package comment.
		cut, kind, n := r.nextSpecial(b)
		if kind == 0 {
			if n := partialTail(b); n > 0 {
				cut -= n
				r.hold(b[cut:])
			}
		}
		if cut > 0 {
			sink(b[:cut])
		}
		b = b[cut:]
		switch kind {
		case 0:
			return
		case specialPrefix:
			b = b[1:]
			// Everything after the prefix goes to the program until it
			// decides where input goes next. The message is delivered
			// before the key that follows is written, so the program
			// always sees them in order.
			r.pass.Store(false)
			if send != nil {
				send(PrefixMsg{})
			}
		default:
			// A sequence that is nerdr's even while the agent has focus.
			_, _ = r.pw.Write(b[:n])
			b = b[n:]
		}
	}
}

// toProgram writes b to the program, each mouse report in a write of its
// own, and holds a mouse report cut off at the end of b for its rest.
// bubbletea reads 256 bytes at a time, and a report cut off at the end of
// one of them it takes for keys: for an escape, which closes a dialog, and
// the rest of the report typed. A quick turn of the wheel sends more than
// that at once.
func (r *Router) toProgram(b []byte) {
	if n := mouseTail(b); n > 0 {
		r.hold(b[len(b)-n:])
		b = b[:len(b)-n]
	}
	for len(b) > 0 {
		i := bytes.Index(b, []byte(mousePrefix))
		if i < 0 {
			_, _ = r.pw.Write(b)
			return
		}
		n := mouseLen(b[i:])
		if n == 0 {
			// Not a mouse report after all.
			n = len(mousePrefix)
		}
		if i > 0 {
			_, _ = r.pw.Write(b[:i])
		}
		_, _ = r.pw.Write(b[i : i+n])
		b = b[i+n:]
	}
}

// hold keeps b until the next read, or until holdFor has passed.
func (r *Router) hold(b []byte) {
	r.held = append([]byte(nil), b...)
	if r.timer == nil {
		r.timer = time.AfterFunc(holdFor, r.flushHeld)
	} else {
		r.timer.Reset(holdFor)
	}
}

// flushHeld sends on what was held when nothing followed it: it was a key
// of its own (escape or alt+[, say), not the start of a sequence. The
// start of a mouse report is no key: it is dropped, and its rest with it
// should it come later.
func (r *Router) flushHeld() {
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	b := r.held
	r.held = nil
	if len(b) == 0 {
		return
	}
	if bytes.HasPrefix(b, []byte(mousePrefix)) {
		r.mouseCut = time.Now()
		return
	}
	r.mu.Lock()
	sink := r.sink
	r.mu.Unlock()
	if r.pass.Load() && sink != nil {
		sink(b)
	} else {
		_, _ = r.pw.Write(b)
	}
}

// partialTail is how many bytes at the end of b are the start of a
// sequence the router takes out (a focus report, a mouse report or one of
// appKeys), cut off by the end of the read. A lone ESC at the end is held
// too: the escape key waits holdFor, but a mouse report cut off after its
// ESC would otherwise give the agent an escape — which interrupts it —
// and the rest of the report as typed text.
func partialTail(b []byte) int {
	if n := mouseTail(b); n > 0 {
		return n
	}
	if b[len(b)-1] == 0x1b {
		return 1
	}
	for n := min(len(b), 4); n >= 2; n-- {
		tail := b[len(b)-n:]
		if tail[0] != 0x1b {
			continue
		}
		for _, k := range specialSeqs {
			if len(k) > n && bytes.HasPrefix([]byte(k), tail) {
				return n
			}
		}
	}
	return 0
}

// mouseTail is how many bytes at the end of b are the start of a mouse
// report, cut off by the end of the read.
func mouseTail(b []byte) int {
	if i := bytes.LastIndex(b, []byte(mousePrefix)); i >= 0 && len(b)-i <= maxMouseLen && mouseParams(b[i+len(mousePrefix):]) == len(b)-i-len(mousePrefix) {
		return len(b) - i
	}
	return 0
}

// mousePrefix starts an SGR mouse report, ESC [ < b ; x ; y M (or m for a
// release): the only encoding nerdr turns on.
const mousePrefix = "\x1b[<"

// maxMouseLen is longer than any mouse report: ESC [ < 3 digits ; 4 ; 4 M.
const maxMouseLen = 20

// mouseParams is how many bytes at the start of b are a mouse report's
// parameters: digits and semicolons.
func mouseParams(b []byte) int {
	for i, c := range b {
		if (c < '0' || c > '9') && c != ';' {
			return i
		}
	}
	return len(b)
}

// mouseLen is the length of the mouse report b starts with, or 0.
func mouseLen(b []byte) int {
	if !bytes.HasPrefix(b, []byte(mousePrefix)) {
		return 0
	}
	n := len(mousePrefix) + mouseParams(b[len(mousePrefix):])
	if n < len(b) && (b[n] == 'M' || b[n] == 'm') {
		return n + 1
	}
	return 0
}

// specialSeqs are all the escape sequences nextSpecial looks for.
var specialSeqs = append([]string{"\x1b[I", "\x1b[O"}, appKeys...)

// appKeys are keys that go to nerdr even while an agent has focus: F1-F5
// switch the review panel between its screens, the menu it always shows.
// The agents nerdr runs do not use them; F6 and up go to them.
var appKeys = []string{
	"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS", // F1-F4 as most terminals send them
	"\x1b[11~", "\x1b[12~", "\x1b[13~", "\x1b[14~", // and as some others do
	"\x1b[15~", // F5, which all send so
}

// What nextSpecial found.
const (
	specialPrefix = 'P'
	specialFocus  = 'F'
	specialMouse  = 'M'
	specialKey    = 'K'
)

// nextSpecial finds the next byte sequence the router handles itself: the
// prefix, a focus report, a mouse report or one of appKeys. It returns the
// offset, what it is and its length; len(b) and 0 when there is none.
func (r *Router) nextSpecial(b []byte) (int, byte, int) {
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == r.prefix:
			return i, specialPrefix, 1
		case b[i] == 0x1b && i+2 < len(b) && b[i+1] == '[' && (b[i+2] == 'I' || b[i+2] == 'O'):
			// Not part of a longer sequence such as ESC [ 1 ; 2 I.
			return i, specialFocus, 3
		case b[i] == 0x1b:
			if n := mouseLen(b[i:]); n > 0 {
				return i, specialMouse, n
			}
			if r.fkeys.Load() {
				continue
			}
			for _, k := range appKeys {
				if bytes.HasPrefix(b[i:], []byte(k)) {
					return i, specialKey, len(k)
				}
			}
		}
	}
	return len(b), 0, 0
}
