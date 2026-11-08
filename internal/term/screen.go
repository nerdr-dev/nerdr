package term

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nerdr-dev/nerdr/internal/detect"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/vt"
)

// Screen is what a program has drawn: the virtual screen and its
// scrollback, and what the program has asked of its terminal. It knows
// nothing of the program itself: what the program writes is given to Feed,
// so a screen can be kept for a program run elsewhere, or tested with no
// program at all.
type Screen struct {
	mu  sync.Mutex
	emu *vt.Emulator

	// osc holds each OSC back from the emulator until it has ended; see
	// oscFilter.
	osc      oscFilter
	oscOut   []byte
	title    string
	progress string
	// copied is what the program last put on the clipboard with OSC 52,
	// until the app takes it; see TakeCopied.
	copied   string
	cursorOn bool
	// Modes the program has asked for that change what keys and pastes must
	// look like on the way in; see Pane.Input.
	appCursor      bool
	bracketedPaste bool
	focusEvents    bool
	// The mouse reports the program has asked for; see mouse.go.
	mouseOn  map[ansi.DECMode]bool
	mouseSgr bool
	// exited is set when the program has gone: no cursor is drawn then.
	exited bool
	// incomplete is set when the screen may be missing some of what the
	// program drew; see Incomplete.
	incomplete bool
	// fed is how many bytes of output Feed has been given.
	fed int64

	// drawGen counts what changes what a Frame shows: output, a resize,
	// the exit. See DrawGen.
	drawGen uint64
	// gen counts the changes to what Snapshot reads: output, which also
	// sets the title and progress, and a resize. snap is the snapshot
	// last taken, at snapGen.
	gen     uint64
	snap    detect.Input
	snapGen uint64
	// lastOutput is when the program last wrote anything; see Quiet.
	lastOutput time.Time
	// drawnOn is the frame the screen was last drawn on; see NextFrame.
	drawnOn atomic.Uint64
}

// NewScreen is an empty screen of at least 10×3.
func NewScreen(w, h int) *Screen {
	w, h = max(w, 10), max(h, 3)
	s := &Screen{
		emu:        vt.NewEmulator(w, h),
		cursorOn:   true,
		lastOutput: time.Now(),
	}
	s.emu.SetScrollbackSize(5000)
	s.emu.SetCallbacks(vt.Callbacks{
		Title:            func(t string) { s.title = t },
		CursorVisibility: func(v bool) { s.cursorOn = v },
		EnableMode:       func(m ansi.Mode) { s.setMode(m, true) },
		DisableMode:      func(m ansi.Mode) { s.setMode(m, false) },
	})
	// OSC 9;4 is the progress report some agents use to say they are busy.
	s.emu.RegisterOscHandler(9, func(data []byte) bool {
		t := string(data)
		t = strings.TrimPrefix(t, "9;")
		if strings.HasPrefix(t, "4;") {
			s.progress = t
		}
		return true
	})
	// OSC 52 sets the clipboard: a program that takes the mouse, as Copilot
	// does, copies what is chosen in it this way, which the emulator would
	// drop. A query for what is on the clipboard is never answered.
	s.emu.RegisterOscHandler(52, func(data []byte) bool {
		if text, ok := clipboardSet(data); ok {
			s.copied = text
		}
		return true
	})
	return s
}

// Feed takes what the program wrote. What it asks of its terminal is
// answered through ReadReply, which something must be reading: Feed waits
// for an answer to be taken.
func (s *Screen) Feed(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feed(b)
}

// FeedAnswered is Feed for output whose queries were answered before, read
// again: they are not answered.
func (s *Screen) FeedAnswered(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emu.MuteReplies(true)
	defer s.emu.MuteReplies(false)
	s.feed(b)
}

// feed is Feed with s.mu held.
func (s *Screen) feed(b []byte) {
	s.oscOut = s.osc.write(s.oscOut[:0], b, func(t string) { s.title = t })
	s.write(s.oscOut)
	s.fed += int64(len(b))
	s.drawGen++
	s.gen++
	s.lastOutput = time.Now()
}

// write gives b to the emulator. Output it cannot handle can make it
// panic, which is not let bring nerdr down with it: the screen is marked
// incomplete instead, and the rest of b is dropped. The parser may be left
// inside the sequence, so a CAN, which ends any sequence, is given after.
// s.mu is held.
func (s *Screen) write(b []byte) {
	if !s.tryWrite(b) {
		s.incomplete = true
		s.tryWrite([]byte{0x18})
	}
}

// tryWrite reports whether the emulator took b without a panic.
func (s *Screen) tryWrite(b []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	_, _ = s.emu.Write(b)
	return true
}

// ReadReply reads what the screen answers to the program's queries (cursor
// position, device attributes, colours), to go back to the program. It
// returns an error once the replies are closed; see CloseReplies.
func (s *Screen) ReadReply(buf []byte) (int, error) { return s.emu.Read(buf) }

// CloseReplies ends ReadReply. It comes before Close, which is not safe
// while ReadReply is running.
func (s *Screen) CloseReplies() {
	if c, ok := s.emu.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
}

// Close releases the emulator, once ReadReply has returned.
func (s *Screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.emu.Close()
}

// Resize changes the screen to w×h, at least 10×3.
func (s *Screen) Resize(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resize(w, h)
}

// resize reports whether the size changed. A panic in the emulator, as
// in write, is not let bring nerdr down: the screen is marked incomplete.
// s.mu is held.
func (s *Screen) resize(w, h int) bool {
	w, h = max(w, 10), max(h, 3)
	if s.emu.Width() == w && s.emu.Height() == h {
		return false
	}
	if !s.tryResize(w, h) {
		s.incomplete = true
		s.tryWrite([]byte{0x18})
	}
	s.drawGen++
	s.gen++
	return true
}

// tryResize reports whether the emulator took the new size without a
// panic.
func (s *Screen) tryResize(w, h int) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	emuResize(s.emu, w, h)
	return true
}

// emuResize is how the emulator is resized; a test makes it panic.
var emuResize = (*vt.Emulator).Resize

func (s *Screen) setMode(m ansi.Mode, on bool) {
	if s.setMouseMode(m, on) {
		return
	}
	switch m {
	case ansi.ModeCursorKeys:
		s.appCursor = on
	case ansi.ModeBracketedPaste:
		s.bracketedPaste = on
	case ansi.ModeFocusEvent:
		s.focusEvents = on
	}
}

// TakeCopied is what the program has put on the clipboard since it was
// last asked, or "".
func (s *Screen) TakeCopied() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := s.copied
	s.copied = ""
	return text
}

// Title is the window title the program last set.
func (s *Screen) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// Snapshot is the input for status detection: the screen as plain text
// plus the title and progress reports. It is taken again only once the
// program has drawn since: an agent's state is looked at every few tenths
// of a second, and most of the time nothing has changed.
func (s *Screen) Snapshot() detect.Input {
	in, _ := s.SnapshotGen()
	return in
}

// SnapshotGen is Snapshot, and a number that changes whenever the
// snapshot can have: a caller that worked something out from the last
// one need not again while it is the same.
func (s *Screen) SnapshotGen() (detect.Input, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapGen != s.gen || s.snapGen == 0 {
		s.snap, s.snapGen = s.snapshot(), s.gen
	}
	return s.snap, s.gen
}

func (s *Screen) snapshot() detect.Input {
	w, h := s.emu.Width(), s.emu.Height()
	var b strings.Builder
	for y := 0; y < h; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			c := s.emu.CellAt(x, y)
			if c == nil {
				continue
			}
			if c.Width == 0 {
				if c.Content != "" {
					line.WriteString(c.Content)
				}
				continue
			}
			if c.Content == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(c.Content)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	return detect.Input{Screen: b.String(), Title: s.title, Progress: s.progress}
}

// Quiet reports how long it has been since the program last drew anything.
func (s *Screen) Quiet() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastOutput)
}

// LastOutput is when the program last drew anything.
func (s *Screen) LastOutput() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastOutput
}

// Size is the virtual screen's width and height.
func (s *Screen) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Width(), s.emu.Height()
}
