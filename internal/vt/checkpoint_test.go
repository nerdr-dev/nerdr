package vt

// nerdr's tests of checkpoint.go. See UPSTREAM.md.

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// newCkEmu is an emulator whose replies to queries are read and dropped,
// as a program reading them would.
func newCkEmu(t testing.TB, w, h int) *Emulator {
	e := NewEmulator(w, h)
	e.SetScrollbackSize(500)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, e)
	}()
	t.Cleanup(func() {
		// The reader is ended before Close, which is not safe while it
		// runs.
		_ = e.InputPipe().(io.Closer).Close()
		<-done
		_ = e.Close()
	})
	return e
}

func mustCheckpoint(t testing.TB, e *Emulator) []byte {
	t.Helper()
	b, err := e.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sameState fails if two emulators differ in what a checkpoint holds or in
// what is drawn, read through the public API as a check on the encoding.
func sameState(t testing.TB, what string, a, b *Emulator) {
	t.Helper()
	if a.Render() != b.Render() {
		t.Fatalf("%s: screens differ:\n%q\n%q", what, a.Render(), b.Render())
	}
	if a.CursorPosition() != b.CursorPosition() || a.IsAltScreen() != b.IsAltScreen() {
		t.Fatalf("%s: cursor or alt screen differ", what)
	}
	if a.ScrollbackLen() != b.ScrollbackLen() {
		t.Fatalf("%s: scrollback %d lines, %d", what, a.ScrollbackLen(), b.ScrollbackLen())
	}
	for i, l := range a.Scrollback().Lines() {
		if l.String() != b.Scrollback().Line(i).String() {
			t.Fatalf("%s: scrollback line %d differs:\n%q\n%q", what, i, l.String(), b.Scrollback().Line(i).String())
		}
	}
	if !bytes.Equal(mustCheckpoint(t, a), mustCheckpoint(t, b)) {
		t.Fatalf("%s: checkpoints differ", what)
	}
}

// ckStream is output with most of what a program does to its terminal:
// text, wide and combined characters, colours of every kind, hyperlinks,
// cursor moves, scroll regions, the alternate screen, modes, character
// sets, tab stops and titles.
func ckStream(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	pieces := []func() string{
		func() string { return strings.Repeat("word ", r.IntN(30)) },
		func() string { return "\r\n" },
		func() string { return "\r\n\r\n\r\n" },
		func() string { return "橋の上 " },
		func() string { return "é 👍🏽 🇳🇴 " },
		func() string { return fmt.Sprintf("\x1b[%dm", []int{0, 1, 3, 4, 7, 9, 31, 42, 97, 39, 49}[r.IntN(11)]) },
		func() string { return fmt.Sprintf("\x1b[38;5;%dm", r.IntN(256)) },
		func() string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r.IntN(256), r.IntN(256), r.IntN(256)) },
		func() string { return "\x1b[4:3m\x1b[58;2;255;0;0m" },
		func() string { return "\x1b]8;id=1;https://example.com/x\x1b\\link\x1b]8;;\x1b\\" },
		func() string { return fmt.Sprintf("\x1b[%d;%dH", 1+r.IntN(12), 1+r.IntN(40)) },
		func() string { return fmt.Sprintf("\x1b[%dA\x1b[%dC", r.IntN(4), r.IntN(10)) },
		func() string { return "\x1b[2;8r" },
		func() string { return "\x1b[r" },
		func() string { return "\x1b[3L\x1b[2M\x1b[4P\x1b[2@\x1b[K\x1b[1J" },
		func() string { return "\x1b[?1049h" },
		func() string { return "\x1b[?1049l" },
		func() string { return "\x1b[?25l\x1b[?2004h\x1b[?1000h\x1b[?1006h" },
		func() string { return "\x1b[?25h\x1b[?2004l\x1b[?1000l\x1b[?7l" },
		func() string { return "\x1b[?7h\x1b[?6h\x1b[?6l" },
		func() string { return "\x1b(0lqqk\x1b(B" },
		func() string { return "\x1b)0\x0e" + "aq" + "\x0f" },
		func() string { return "\x1bH\x1b[3g\t\x1b[g\t" },
		func() string { return "\x1b7\x1b[5;5Hsaved\x1b8" },
		func() string { return fmt.Sprintf("\x1b]0;title %d\x07", r.IntN(100)) },
		func() string { return "\x1b]7;file://host/tmp\x1b\\" },
		func() string { return "\x1b]4;1;rgb:ff/80/00\x07\x1b]10;#336699\x07" },
		func() string { return "\x1b[5 q" },
		func() string { return "x\x1b[3b" },
		func() string { return strings.Repeat("y", 40) },
		func() string { return "\x1b[6n\x1b[c" },
	}
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(pieces[r.IntN(len(pieces))]())
	}
	return []byte(b.String())
}

// feedChunks writes b to e in pieces of random size, as a pipe would give
// it.
func feedChunks(e *Emulator, b []byte, r *rand.Rand) {
	for len(b) > 0 {
		n := min(len(b), 1+r.IntN(64))
		_, _ = e.Write(b[:n])
		b = b[n:]
	}
}

// A checkpoint taken at any cut outside a sequence, restored into another
// emulator, gives the same state, and the same state again after the rest
// of the output given to both.
func TestCheckpointAtRandomCuts(t *testing.T) {
	const w, h = 40, 12
	cuts, skipped := 0, 0
	for seed := range uint64(8) {
		stream := ckStream(seed, 20000)
		r := rand.New(rand.NewPCG(seed, 3))
		for range 60 {
			cut := r.IntN(len(stream))
			a := newCkEmu(t, w, h)
			feedChunks(a, stream[:cut], r)
			if !a.AtGround() {
				if _, err := a.Checkpoint(); err != ErrNotAtGround {
					t.Fatalf("checkpoint inside a sequence: %v", err)
				}
				skipped++
				continue
			}
			cuts++
			b := newCkEmu(t, 10, 3)
			if err := b.Restore(mustCheckpoint(t, a)); err != nil {
				t.Fatal(err)
			}
			what := fmt.Sprintf("seed %d cut %d", seed, cut)
			sameState(t, what, a, b)
			// x/vt ends a grapheme cluster at the end of each write, so
			// both are given the rest in the same pieces.
			for rest := stream[cut:]; len(rest) > 0; {
				n := min(len(rest), 1+r.IntN(64))
				_, _ = a.Write(rest[:n])
				_, _ = b.Write(rest[:n])
				rest = rest[n:]
			}
			sameState(t, what+" and after", a, b)
		}
	}
	if cuts < 150 || skipped == 0 {
		t.Fatalf("%d cuts tried, %d inside a sequence: the stream tests too little", cuts, skipped)
	}
}

// A cut inside a UTF-8 character is inside a sequence too.
func TestCheckpointNotInsideACharacter(t *testing.T) {
	e := newCkEmu(t, 20, 5)
	_, _ = e.Write([]byte("橋")[:2])
	if e.AtGround() {
		t.Fatal("at ground inside a character")
	}
	_, _ = e.Write([]byte("橋")[2:])
	if !e.AtGround() {
		t.Fatal("not at ground after the character")
	}
}

// A pending wrap is kept: the next character goes on the next line.
func TestCheckpointKeepsPendingWrap(t *testing.T) {
	a := newCkEmu(t, 20, 5)
	_, _ = a.Write([]byte(strings.Repeat("x", 20)))
	if !a.atPhantom {
		t.Fatal("no wrap pending: the test shows nothing")
	}
	b := newCkEmu(t, 20, 5)
	if err := b.Restore(mustCheckpoint(t, a)); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Write([]byte("Y"))
	_, _ = b.Write([]byte("Y"))
	sameState(t, "after the wrap", a, b)
	if b.CursorPosition() != uv.Pos(1, 1) {
		t.Fatalf("cursor at %v after the wrap", b.CursorPosition())
	}
}

// What is under the alternate screen comes back when it is left, which the
// public API cannot give.
func TestCheckpointKeepsMainUnderAlt(t *testing.T) {
	a := newCkEmu(t, 20, 5)
	_, _ = a.Write([]byte("main text\x1b[?1049halt text"))
	b := newCkEmu(t, 20, 5)
	if err := b.Restore(mustCheckpoint(t, a)); err != nil {
		t.Fatal(err)
	}
	_, _ = b.Write([]byte("\x1b[?1049l"))
	if got := strings.TrimSpace(b.String()); got != "main text" {
		t.Fatalf("after leaving the alternate screen: %q", got)
	}
}

// A checkpoint that cannot be read leaves the emulator as it was.
func TestRestoreBadCheckpoint(t *testing.T) {
	a := newCkEmu(t, 20, 5)
	_, _ = a.Write([]byte("\x1b[31mhello\x1b]0;t\x07"))
	good := mustCheckpoint(t, a)
	b := newCkEmu(t, 20, 5)
	_, _ = b.Write([]byte("kept"))
	before := mustCheckpoint(t, b)

	newer := bytes.Clone(good)
	newer[len(checkpointMagic)] = CheckpointVersion + 1
	if v, err := CheckpointVersionOf(newer); err != nil || v != CheckpointVersion+1 {
		t.Fatalf("version of a newer one: %d, %v", v, err)
	}
	for name, bad := range map[string][]byte{
		"empty":     nil,
		"other":     []byte("not a checkpoint at all"),
		"newer":     newer,
		"truncated": good[:len(good)/2],
		"trailing":  append(bytes.Clone(good), 0),
	} {
		if err := b.Restore(bad); err == nil {
			t.Errorf("%s: restored", name)
		}
		if !bytes.Equal(mustCheckpoint(t, b), before) {
			t.Fatalf("%s: emulator changed", name)
		}
	}
	// Every cut of a good one fails rather than panics.
	for i := range len(good) {
		if err := b.Restore(good[:i]); err == nil {
			t.Fatalf("restored from %d of %d bytes", i, len(good))
		}
	}
}

// FuzzRestore: no input makes Restore panic or take a broken state.
func FuzzRestore(f *testing.F) {
	// 40×12: x/vt panics on a scroll region below the screen, which
	// ckStream's fits in.
	e := newCkEmu(f, 40, 12)
	_, _ = e.Write(ckStream(1, 2000))
	b, _ := e.Checkpoint()
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		e := newCkEmu(t, 40, 12)
		if e.Restore(b) == nil && e.Height() >= 12 {
			_, _ = e.Write(ckStream(2, 500))
			_ = e.Render()
		}
	})
}

// fullHistory is an emulator of an agent's size whose history is full of
// coloured lines, as a long session leaves it.
func fullHistory(b *testing.B) *Emulator {
	e := newCkEmu(b, 200, 50)
	e.SetScrollbackSize(5000)
	r := rand.New(rand.NewPCG(1, 1))
	var s strings.Builder
	for i := range 5100 {
		fmt.Fprintf(&s, "\x1b[38;5;%dm%4d \x1b[0m%s \x1b[1mbold\x1b[0m %s\r\n",
			r.IntN(256), i, strings.Repeat("text ", 10+r.IntN(25)), "橋")
	}
	_, _ = e.Write([]byte(s.String()))
	if e.ScrollbackLen() != 5000 {
		b.Fatalf("%d lines of history", e.ScrollbackLen())
	}
	return e
}

func BenchmarkCheckpointFullHistory(b *testing.B) {
	e := fullHistory(b)
	var n int
	for b.Loop() {
		ck, _ := e.Checkpoint()
		n = len(ck)
	}
	b.ReportMetric(float64(n)/1e6, "MB")
}

func BenchmarkRestoreFullHistory(b *testing.B) {
	ck, _ := fullHistory(b).Checkpoint()
	e := newCkEmu(b, 200, 50)
	for b.Loop() {
		if err := e.Restore(ck); err != nil {
			b.Fatal(err)
		}
	}
}
