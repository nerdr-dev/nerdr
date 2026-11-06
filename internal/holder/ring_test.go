package holder

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// The ring keeps the last max bytes in order, whatever the writes; a read
// from a position that is gone starts at the oldest kept, and says so.
func TestRingKeepsTheLastBytes(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, max := range []int{1, 7, 64, 1000} {
		r := newRing(max)
		var all []byte
		for range 500 {
			p := make([]byte, rng.IntN(3*max+2))
			for i := range p {
				p[i] = byte(rng.Uint32())
			}
			r.write(p)
			all = append(all, p...)
			want := all[max0(len(all)-max):]
			if r.end != int64(len(all)) || r.start() != int64(len(all)-len(want)) {
				t.Fatalf("max %d: start %d end %d, want %d %d", max, r.start(), r.end, len(all)-len(want), len(all))
			}
			if got := r.bytes(); !bytes.Equal(got, want) {
				t.Fatalf("max %d: kept %x, want %x", max, got, want)
			}
			pos := int64(rng.IntN(len(all) + 2))
			b := make([]byte, rng.IntN(max+3))
			n, from := r.read(pos, b)
			wantFrom := pos
			if wantFrom < r.start() {
				wantFrom = r.start()
			}
			if wantFrom > r.end {
				wantFrom = r.end
			}
			wantN := min(len(b), int(int64(len(all))-wantFrom))
			if from != wantFrom || n != wantN || !bytes.Equal(b[:n], all[from:from+int64(n)]) {
				t.Fatalf("max %d: read(%d) = %d from %d, want %d from %d", max, pos, n, from, wantN, wantFrom)
			}
		}
		// What restore is given reads back at the same positions.
		r2 := newRing(max)
		r2.restore(r.bytes(), r.end)
		if r2.start() != r.start() || r2.end != r.end || !bytes.Equal(r2.bytes(), r.bytes()) {
			t.Fatalf("max %d: restored %d..%d, want %d..%d", max, r2.start(), r2.end, r.start(), r.end)
		}
		r2.write([]byte("more"))
		r.write([]byte("more"))
		if !bytes.Equal(r2.bytes(), r.bytes()) {
			t.Fatalf("max %d: restored ring wrote differently", max)
		}
	}
}

// A quiet program's ring stays small.
func TestRingGrowsAsOutputComes(t *testing.T) {
	r := newRing(DefaultRing)
	r.write([]byte("$ "))
	if cap(r.buf) > 1<<10 {
		t.Errorf("two bytes took %d", cap(r.buf))
	}
}

func max0(n int) int { return max(n, 0) }
