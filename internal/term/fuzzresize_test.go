package term

import (
	"io"
	"testing"
)

// fuzzScreen is a screen whose replies are read and dropped, as a pane
// reads them for its program.
func fuzzScreen(t *testing.T, w, h int) *Screen {
	s := NewScreen(w, h)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 256)
		for {
			if _, err := s.ReadReply(buf); err != nil && err != io.ErrShortBuffer {
				return
			}
		}
	}()
	t.Cleanup(func() {
		s.CloseReplies()
		<-done
		s.Close()
	})
	return s
}

// resizeOps runs b on s as output and resizes: a byte below 0x20 that
// two follow is a resize to the size they give, any other a run of the
// output after it, as long as it says.
func resizeOps(s *Screen, b []byte) {
	for len(b) > 0 {
		op := b[0]
		b = b[1:]
		if op < 0x20 && len(b) >= 2 {
			s.Resize(int(b[0])%300+1, int(b[1])%150+1)
			b = b[2:]
			continue
		}
		n := min(int(op)%96+1, len(b))
		s.Feed(b[:n])
		b = b[n:]
	}
}

// FuzzScreenResize: no output, with resizes between, makes the screen
// panic, nor leaves it unable to take output and draw. The emulator is
// not memory safe in every engine; this is the test the contract asks of
// one before it is used (decision C).
func FuzzScreenResize(f *testing.F) {
	seed := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	resize := func(w, h byte) []byte { return []byte{0x01, w, h} }
	feed := func(s string) []byte {
		var out []byte
		for len(s) > 0 {
			n := min(len(s), 96)
			out = append(append(out, byte(96+n-1)), s[:n]...) // 96+n-1 says n
			s = s[n:]
		}
		return out
	}
	f.Add(seed(feed("\x1b[5;20r\x1b[20;1Hscrolled\n\n"), resize(40, 6), feed("\x1b[Mmore\r\n")))
	f.Add(seed(feed("\x1b[?1049h\x1b[2J\x1b[30;100Hfar"), resize(10, 3), feed("\x1b[?1049lback"), resize(200, 50)))
	f.Add(seed(feed("橋橋橋橋\x1b[1;79H橋"), resize(79, 24), resize(1, 1), feed("\x1b[@\x1b[P\x1b[4h橋")))
	f.Add(seed(feed(string(agentStream(3, 200))), resize(30, 8), feed(string(agentStream(4, 200)))))
	f.Fuzz(func(t *testing.T, b []byte) {
		s := fuzzScreen(t, 80, 24)
		resizeOps(s, b)
		s.Resize(50, 10)
		s.Feed([]byte("\x1b[0m\r\nafter\r\n"))
		_ = s.Frame(0)
		_ = s.Snapshot()
	})
}
