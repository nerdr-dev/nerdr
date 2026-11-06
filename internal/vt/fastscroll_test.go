package vt

// nerdr's tests of its faster scroll in screen.go. See UPSTREAM.md.

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// scrollStream is output that scrolls in every way: line feeds at the
// bottom, in a scroll region, with left and right margins, SU, SD, IL and DL,
// with coloured backgrounds that blank lines take.
func scrollStream(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 7))
	var b strings.Builder
	for b.Len() < n {
		switch r.IntN(8) {
		case 0:
			fmt.Fprintf(&b, "\x1b[%d;%dr", 1+r.IntN(4), 5+r.IntN(8))
		case 1:
			b.WriteString("\x1b[r")
		case 2:
			fmt.Fprintf(&b, "\x1b[%dS\x1b[%dT", r.IntN(4), r.IntN(3))
		case 3:
			fmt.Fprintf(&b, "\x1b[%d;1H\x1b[%dL\x1b[%dM", 1+r.IntN(12), r.IntN(3), r.IntN(3))
		case 4:
			fmt.Fprintf(&b, "\x1b[4%dm", r.IntN(8))
		case 5:
			b.WriteString("\x1b[?69h\x1b[3;30s\x1b[12;1H\r\n\r\n\x1b[?69l")
		default:
			fmt.Fprintf(&b, "line %d %s\r\n", r.IntN(1000), strings.Repeat("w ", r.IntN(30)))
		}
	}
	return []byte(b.String())
}

// The faster scroll leaves the same state as upstream's copying one.
func TestFastScrollSameAsUpstream(t *testing.T) {
	defer func() { copyOnScroll = false }()
	streams := map[string][]byte{}
	for seed := range uint64(4) {
		streams[fmt.Sprintf("scroll %d", seed)] = scrollStream(seed, 30000)
		streams[fmt.Sprintf("mixed %d", seed)] = ckStream(seed, 30000)
	}
	for name, s := range streams {
		for _, chunk := range []int{4096, 7} {
			run := func(copying bool) []byte {
				copyOnScroll = copying
				e := newCkEmu(t, 40, 12)
				for i := 0; i < len(s); i += chunk {
					_, _ = e.Write(s[i:min(i+chunk, len(s))])
				}
				return mustCheckpoint(t, e)
			}
			if !bytes.Equal(run(false), run(true)) {
				t.Errorf("%s at chunk %d: states differ", name, chunk)
			}
		}
	}
}

func BenchmarkScroll(b *testing.B) {
	out := []byte(strings.Repeat("\x1b[44msome output on a line of its own\x1b[0m\r\n", 2000))
	for _, copying := range []bool{false, true} {
		b.Run(fmt.Sprintf("copying=%v", copying), func(b *testing.B) {
			copyOnScroll = copying
			defer func() { copyOnScroll = false }()
			e := newCkEmu(b, 200, 50)
			b.SetBytes(int64(len(out)))
			for b.Loop() {
				_, _ = e.Write(out)
			}
		})
	}
}
