package review

import "testing"

// TestFramedIsLipglossBorder: framed draws a pane's border as the border
// styles render it, byte for byte: lines of one width, shorter ones, wide
// and styled cells, a tab.
func TestFramedIsLipglossBorder(t *testing.T) {
	blocks := []string{
		"one\ntwo\nthree",
		"a wide 世界 row\nshort\n\n" + dimStyle.Render("dim") + " and plain",
		selectedStyle.Render("selected  ") + "\n\ttabbed\n✓ ok",
		"",
	}
	for _, b := range blocks {
		for _, active := range []bool{true, false} {
			style := borderInactive
			if active {
				style = borderActive
			}
			if got, want := framed(b, active), style.Render(b); got != want {
				t.Errorf("framed(%q, %v):\n%q\nlipgloss:\n%q", b, active, got, want)
			}
		}
	}
}
