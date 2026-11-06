package term

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// sameScreen fails if two screens differ in anything a client or the
// status detection reads, or in their checkpoints.
func sameScreen(t *testing.T, what string, a, b *Screen) {
	t.Helper()
	fa, fb := a.Frame(0), b.Frame(0)
	fa.Gen, fb.Gen = 0, 0
	if !reflect.DeepEqual(fa, fb) {
		t.Fatalf("%s: frames differ:\n%q\n%q", what, a.Snapshot().Screen, b.Snapshot().Screen)
	}
	if a.Snapshot() != b.Snapshot() {
		t.Fatalf("%s: snapshots differ: %+v, %+v", what, a.Snapshot(), b.Snapshot())
	}
	if a.ScrollbackLen() != b.ScrollbackLen() || a.Text(1000) != b.Text(1000) {
		t.Fatalf("%s: scrollback differs", what)
	}
	type modes struct{ mouse, motion, appCursor, paste, focus, sgr bool }
	m := func(s *Screen) modes {
		s.mu.Lock()
		defer s.mu.Unlock()
		return modes{s.mouseLevel() >= 0, s.mouseLevel() == 3, s.appCursor, s.bracketedPaste, s.focusEvents, s.mouseSgr}
	}
	if m(a) != m(b) {
		t.Fatalf("%s: modes differ: %+v, %+v", what, m(a), m(b))
	}
	ca, errA := a.Checkpoint()
	cb, errB := b.Checkpoint()
	if errA != nil || errB != nil || !bytes.Equal(ca, cb) {
		t.Fatalf("%s: checkpoints differ (%v, %v)", what, errA, errB)
	}
}

// agentStream is output as an agent writes it, with what Screen keeps of
// its own: titles, some with the ✳ the filter must hold back, progress
// reports, the clipboard, mouse modes, paste and focus modes.
func agentStream(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	pieces := []string{
		"\x1b]0;✳ Claude Code\x07", "\x1b]2;⠂ working\x1b\\", "\x1b]0;plain\x07",
		"\x1b]9;4;3;\x07", "\x1b]9;4;0;\x07",
		"\x1b]8;;http://x/✳\x07link\x1b]8;;\x07",
		"\x1b]52;c;aGVsbG8=\x07",
		"\x1b[?1000h\x1b[?1006h", "\x1b[?1003h", "\x1b[?1003l\x1b[?1000l\x1b[?1006l",
		"\x1b[?2004h", "\x1b[?2004l", "\x1b[?1h", "\x1b[?1l", "\x1b[?1004h", "\x1b[?1004l",
		"\x1b[?25l", "\x1b[?25h", "\x1b[?1049h", "\x1b[?1049l",
		"\x1b[31m> \x1b[0m", "\x1b[2K\r", "\x1b[3A", "\x1b[1;1H",
		"\r\n", "\r\n", "æøå ✳ 橋 ", "some words of output ",
		"\x1b\x1b[0m", "\x1b(B",
	}
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return []byte(b.String())
}

// A checkpoint taken at any cut, restored into another screen, gives the
// same screen, and the same again after the rest of the output, also with
// an OSC cut in two.
func TestScreenCheckpointAtRandomCuts(t *testing.T) {
	cuts, busy, heldOSC := 0, 0, 0
	for seed := range uint64(6) {
		stream := agentStream(seed, 8000)
		r := rand.New(rand.NewPCG(seed, 2))
		for range 50 {
			cut := r.IntN(len(stream))
			a, _ := screenOf(t, string(stream[:cut]))
			block, err := a.Checkpoint()
			if err == ErrCheckpointBusy {
				busy++
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			cuts++
			if a.osc.state != oscGround {
				heldOSC++
			}
			b, _ := screenOf(t)
			if err := b.Restore(block); err != nil {
				t.Fatal(err)
			}
			what := fmt.Sprintf("seed %d cut %d", seed, cut)
			sameScreen(t, what, a, b)
			for rest := stream[cut:]; len(rest) > 0; {
				n := min(len(rest), 1+r.IntN(64))
				a.Feed(rest[:n])
				b.Feed(rest[:n])
				rest = rest[n:]
			}
			sameScreen(t, what+" and after", a, b)
			if b.Incomplete() {
				t.Fatalf("%s: marked incomplete", what)
			}
		}
	}
	if cuts < 150 || busy == 0 || heldOSC < 10 {
		t.Fatalf("%d cuts, %d busy, %d with an OSC held back: the stream tests too little", cuts, busy, heldOSC)
	}
}

// A title cut inside its ✳ — 0x9C is in it — and taken as a checkpoint
// there is the whole title after, and none of it lands on the screen.
func TestScreenCheckpointKeepsHeldOSC(t *testing.T) {
	title := []byte("\x1b]0;✳ done\x07> ")
	cut := bytes.IndexByte(title, 0x9c)
	a, _ := screenOf(t, string(title[:cut]))
	block, err := a.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := screenOf(t)
	if err := b.Restore(block); err != nil {
		t.Fatal(err)
	}
	b.Feed(title[cut:])
	if b.Title() != "✳ done" || strings.TrimSpace(b.Snapshot().Screen) != ">" {
		t.Fatalf("title %q, screen %q", b.Title(), b.Snapshot().Screen)
	}
}

// A checkpoint of another engine or version, or damaged, is not put back:
// the screen stays as it was and is marked incomplete, and so is one that
// lost output.
func TestScreenRestoreRefused(t *testing.T) {
	a, _ := screenOf(t, "\x1b]0;t\x07hello")
	good, err := a.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	other := bytes.Replace(good, []byte(checkpointEngine), []byte("ghost"), 1)
	newer := bytes.Clone(good)
	newer[len(checkpointMagic)]++
	for name, bad := range map[string][]byte{
		"empty":     nil,
		"other":     other,
		"newer":     newer,
		"truncated": good[:len(good)-3],
	} {
		b, _ := screenOf(t, "kept")
		gen := b.DrawGen()
		if err := b.Restore(bad); err == nil {
			t.Fatalf("%s: restored", name)
		}
		if !b.Incomplete() || !b.Frame(0).Incomplete || b.DrawGen() == gen {
			t.Fatalf("%s: not marked incomplete, or not drawn again", name)
		}
		if strings.TrimSpace(b.Snapshot().Screen) != "kept" {
			t.Fatalf("%s: screen changed: %q", name, b.Snapshot().Screen)
		}
	}
	for i := range len(good) {
		b, _ := screenOf(t)
		if b.Restore(good[:i]) == nil {
			t.Fatalf("restored from %d of %d bytes", i, len(good))
		}
	}

	c, _ := screenOf(t)
	if err := c.Restore(good); err != nil || c.Incomplete() {
		t.Fatalf("good one: %v, incomplete %v", err, c.Incomplete())
	}
	c.MarkIncomplete()
	if !c.Frame(0).Incomplete {
		t.Fatal("lost output not marked")
	}
	// The mark is in the checkpoint: a screen put back from one that was
	// incomplete is too.
	again, _ := c.Checkpoint()
	d, _ := screenOf(t)
	if err := d.Restore(again); err != nil || !d.Incomplete() {
		t.Fatalf("restored from an incomplete one: %v, incomplete %v", err, d.Incomplete())
	}
}

// FuzzScreenRestore: no block makes Restore panic, and a screen it took
// goes on taking output.
func FuzzScreenRestore(f *testing.F) {
	s := NewScreen(40, 12)
	s.Feed([]byte("\x1b]0;✳ t\x07\x1b[?1000hhello\x1b]0;✳"))
	b, _ := s.Checkpoint()
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		s, _ := screenOf(t)
		if s.Restore(b) == nil {
			s.Feed(agentStream(1, 300))
			_ = s.Frame(0)
		}
	})
}

// A block whose filter is in a state the filter cannot be in is refused:
// the OSC it would hold back is not one, or a count is out of range.
func TestScreenRestoreRefusesABrokenFilter(t *testing.T) {
	a, _ := screenOf(t, "\x1b]0;✳")
	good, err := a.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if a.osc.state != oscString {
		t.Fatal("no OSC held back: the test shows nothing")
	}
	held := appendStr(nil, string(a.osc.buf))
	at := bytes.Index(good, held)
	if at < 0 {
		t.Fatal("filter not found in the block")
	}
	for name, buf := range map[string]string{"empty": "", "not an OSC": "ab"} {
		bad := append(append(bytes.Clone(good[:at]), appendStr(nil, buf)...), good[at+len(held):]...)
		b, _ := screenOf(t)
		if b.Restore(bad) == nil {
			t.Errorf("%s: restored", name)
		}
		b.Feed([]byte("\x07\x1b\\")) // would end the OSC
	}
	// pending, after the buffer, as a uvarint far past an int's range.
	p := at + len(held)
	bad := append(append(bytes.Clone(good[:p]), binary.AppendUvarint(nil, 1<<63)...), good[p+1:]...)
	b, _ := screenOf(t)
	if b.Restore(bad) == nil {
		t.Error("negative pending restored")
	}
}
