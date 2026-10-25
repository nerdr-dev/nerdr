package term

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// TestOSCWithC1ByteInCharacter covers a title whose ✳ has the byte 0x9C in
// it: the title is kept whole and none of it lands on the screen, however
// the reads split it, and other output goes through untouched.
func TestOSCWithC1ByteInCharacter(t *testing.T) {
	cases := []struct {
		in, title, screen string
	}{
		{"\x1b]0;✳ PR list in F3\x07> ab", "✳ PR list in F3", "> ab"},
		{"\x1b]2;✳ PR list\x1b\\> ab", "✳ PR list", "> ab"},
		{"\x1b]0;⠂ busy\x07\x1b[31mred\x1b[0m", "⠂ busy", "red"},
		{"\x1b]8;;http://x/✳\x07link\x1b]8;;\x07", "", "link"},
		{"æøå \x1b[2Cx", "", "æøå   x"},
	}
	for _, c := range cases {
		for _, step := range []int{1, 2, 3, 1000} {
			e := vt.NewEmulator(40, 2)
			var title string
			e.SetCallbacks(vt.Callbacks{Title: func(s string) { title = s }})
			var f oscFilter
			for i := 0; i < len(c.in); i += step {
				_, _ = e.Write(f.write(nil, []byte(c.in[i:min(i+step, len(c.in))]), func(s string) { title = s }))
			}
			var line strings.Builder
			for x := 0; x < 40; x++ {
				if cell := e.CellAt(x, 0); cell != nil && cell.Content != "" {
					line.WriteString(cell.Content)
				} else {
					line.WriteString(" ")
				}
			}
			if got := strings.TrimRight(line.String(), " "); got != c.screen {
				t.Errorf("%q in %d-byte reads: screen = %q, want %q", c.in, step, got, c.screen)
			}
			if title != c.title {
				t.Errorf("%q in %d-byte reads: title = %q, want %q", c.in, step, title, c.title)
			}
		}
	}
}
