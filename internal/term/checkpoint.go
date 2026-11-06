package term

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/vt"
)

// A screen's checkpoint is its state as a block that something else, the
// holder, keeps without reading: put back with Restore, followed by what the
// program wrote after it, it gives the screen as it would have been. The
// block names its engine and both its versions, so a nerdr that cannot read
// it says so instead of showing something wrong.
const (
	checkpointMagic = "nerdr-screen"
	// checkpointEngine is the emulator the block is from.
	checkpointEngine = "x/vt"
	// CheckpointVersion is the version of the block around the engine's
	// state: what Screen keeps of its own. A change to it must change this.
	CheckpointVersion = 1
)

// ErrCheckpointBusy is returned by Checkpoint while the program is inside
// an escape sequence or a character: take it again once the program has
// gone quiet.
var ErrCheckpointBusy = errors.New("term: inside a sequence")

// Checkpoint is the screen's state as an opaque block for Restore: the
// emulator's, the OSC held back by the filter, and what the screen keeps
// of the program's modes and title. It fails with ErrCheckpointBusy
// while the program is in the middle of a sequence.
func (s *Screen) Checkpoint() ([]byte, error) {
	b, _, err := s.checkpoint()
	return b, err
}

// checkpoint is Checkpoint, and how many bytes of output Feed had been
// given when it was taken.
func (s *Screen) checkpoint() ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.checkpointLocked()
	return b, s.fed, err
}

func (s *Screen) checkpointLocked() ([]byte, error) {
	emu, err := s.emu.Checkpoint()
	if errors.Is(err, vt.ErrNotAtGround) {
		return nil, ErrCheckpointBusy
	}
	if err != nil {
		return nil, err
	}
	b := append([]byte(nil), checkpointMagic...)
	b = binary.AppendUvarint(b, CheckpointVersion)
	b = appendStr(b, checkpointEngine)
	b = binary.AppendUvarint(b, vt.CheckpointVersion)

	b = appendStr(b, s.title)
	b = appendStr(b, s.progress)
	var mouse []int
	for m, on := range s.mouseOn {
		if on {
			mouse = append(mouse, m.Mode())
		}
	}
	slices.Sort(mouse)
	b = binary.AppendUvarint(b, uint64(len(mouse)))
	for _, m := range mouse {
		b = binary.AppendUvarint(b, uint64(m))
	}
	for _, v := range []bool{s.cursorOn, s.appCursor, s.bracketedPaste, s.focusEvents, s.mouseSgr, s.incomplete} {
		b = appendBool(b, v)
	}

	b = binary.AppendUvarint(b, uint64(s.osc.state))
	b = appendStr(b, string(s.osc.buf))
	b = binary.AppendUvarint(b, uint64(s.osc.pending))
	b = appendBool(b, s.osc.split)

	return append(b, emu...), nil
}

// Restore puts a checkpoint into the screen, which takes the size it had;
// what the program wrote after it is then given to Feed. A block that
// cannot be read, of another engine or version, leaves the screen as it
// was but marked incomplete: whatever the caller shows of the program
// after it, it is not all.
func (s *Screen) Restore(block []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.restore(block); err != nil {
		s.incomplete = true
		s.drawGen++
		return err
	}
	return nil
}

func (s *Screen) restore(b []byte) error {
	r := ckReader{b: b}
	if len(b) < len(checkpointMagic) || string(b[:len(checkpointMagic)]) != checkpointMagic {
		return errors.New("term: not a screen checkpoint")
	}
	r.b = r.b[len(checkpointMagic):]
	if v := r.uvarint(); r.err == nil && v != CheckpointVersion {
		return fmt.Errorf("term: checkpoint version %d, want %d", v, CheckpointVersion)
	}
	if eng, v := r.str(), r.uvarint(); r.err == nil && (eng != checkpointEngine || v != vt.CheckpointVersion) {
		return fmt.Errorf("term: checkpoint of %s %d, want %s %d", eng, v, checkpointEngine, vt.CheckpointVersion)
	}
	title, progress := r.str(), r.str()
	mouseOn := map[ansi.DECMode]bool{}
	for range r.count(len(mouseModes)) {
		m := ansi.DECMode(r.uvarint())
		if !slices.Contains(mouseModes, m) {
			r.fail()
		}
		mouseOn[m] = true
	}
	var flags [6]bool
	for i := range flags {
		flags[i] = r.bool()
	}
	var osc oscFilter
	state, buf, pending := r.uvarint(), r.str(), r.uvarint()
	osc.split = r.bool()
	if state > oscRaw || pending > 3 || len(buf) > oscMax+1 {
		r.fail()
	}
	osc.state, osc.buf, osc.pending = int(state), []byte(buf), int(pending)
	// An OSC held back starts with ESC ], which end takes off. Outside
	// one, buf and split are what the last left, unread until the next
	// begins.
	if osc.state == oscString && !strings.HasPrefix(buf, "\x1b]") {
		r.fail()
	}
	if r.err != nil {
		return r.err
	}
	// The emulator is the last thing in the block, and leaves itself as
	// it was if it cannot be read.
	if err := s.emu.Restore(r.b); err != nil {
		return err
	}
	s.title, s.progress = title, progress
	s.mouseOn = mouseOn
	s.cursorOn, s.appCursor, s.bracketedPaste, s.focusEvents, s.mouseSgr = flags[0], flags[1], flags[2], flags[3], flags[4]
	s.incomplete = flags[5]
	s.osc = osc
	s.exited = false
	s.drawGen++
	s.gen++
	s.lastOutput = time.Now()
	return nil
}

// MarkIncomplete says the screen is not all the program drew: output it
// wrote was lost before it reached the screen. It stays marked; see
// Incomplete.
func (s *Screen) MarkIncomplete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.incomplete {
		s.incomplete = true
		s.drawGen++
	}
}

// Incomplete reports whether the screen may be missing some of what the
// program drew: a checkpoint could not be put back, output was lost, or
// the emulator failed on some of it.
// Nothing clears it but the program's screen going away with the program.
func (s *Screen) Incomplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.incomplete
}

func appendStr(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}

// ckReader reads the screen's part of a checkpoint; the first error is
// kept, and every read after it gives a zero value.
type ckReader struct {
	b   []byte
	err error
}

func (r *ckReader) fail() {
	if r.err == nil {
		r.err = errors.New("term: bad screen checkpoint")
	}
	r.b = nil
}

func (r *ckReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail()
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *ckReader) count(limit int) int {
	v := r.uvarint()
	if v > uint64(limit) {
		r.fail()
		return 0
	}
	return int(v)
}

func (r *ckReader) str() string {
	v := r.uvarint()
	if v > uint64(len(r.b)) {
		r.fail()
		return ""
	}
	n := int(v)
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

func (r *ckReader) bool() bool {
	if len(r.b) == 0 || r.b[0] > 1 {
		r.fail()
		return false
	}
	v := r.b[0] == 1
	r.b = r.b[1:]
	return v
}
