package review

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// TestWrapTextCountsColumns covers wrapping by what a line takes on the
// screen: a long word of wide or multibyte characters is cut between
// characters, and no line is wider than asked.
func TestWrapTextCountsColumns(t *testing.T) {
	body := "før " + strings.Repeat("🎉", 30) + " " + strings.Repeat("æøå", 20) + " https://example.com/" + strings.Repeat("x", 50)
	for _, l := range wrapText(body, 20, 0) {
		if !utf8.ValidString(l) || lipgloss.Width(l) > 20 {
			t.Fatalf("line %q: valid %v, %d wide", l, utf8.ValidString(l), lipgloss.Width(l))
		}
	}
	if got := wrapText("æøå æøå æøå", 7, 0); len(got) != 2 || got[0] != "æøå æøå" {
		t.Fatalf("æøå is one column each: %q", got)
	}
}
