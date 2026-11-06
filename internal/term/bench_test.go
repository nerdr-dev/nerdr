package term

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkDrawBusy draws a full 200×50 screen of styled text as a client
// does for a program drawing all the time: a frame into the rows of the last,
// and its lines.
func BenchmarkDrawBusy(b *testing.B) {
	s := NewScreen(200, 50)
	var sb strings.Builder
	for i := range 80 {
		fmt.Fprintf(&sb, "\x1b[3%dmline %d \x1b[1msome bold\x1b[0m text that fills the row with words and more words %s\r\n", i%8, i, strings.Repeat("x", 90))
	}
	s.Feed([]byte(sb.String()))
	var f Frame
	for b.Loop() {
		s.Feed([]byte("y"))
		s.FrameInto(&f, 0)
		_ = Draw(f, true, nil)
	}
}
