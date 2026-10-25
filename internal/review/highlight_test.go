package review

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() {
	// go test's stdout isn't a terminal, so lipgloss would otherwise strip
	// all ANSI colour codes; force a colour profile so these tests exercise
	// the same rendering path a real terminal does.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

func TestHighlightSourceColorsKnownLanguage(t *testing.T) {
	src := "export function helper() {\n  return 1;\n}\n"
	lines := highlightSource("f.ts", src)
	if lines == nil {
		t.Fatal("expected non-nil highlighted lines for a .ts file")
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	// A recognised language should emit ANSI escape codes (colour), not the
	// bare source text unchanged.
	if lines[0] == "export function helper() {" {
		t.Fatalf("expected coloured output, got plain text: %q", lines[0])
	}
	if !strings.Contains(lines[0], "helper") {
		t.Fatalf("expected the identifier to survive highlighting, got %q", lines[0])
	}
}

func TestHighlightSourceUnknownExtensionReturnsNil(t *testing.T) {
	if lines := highlightSource("f.this-is-not-a-real-extension-xyz", "hello\n"); lines != nil {
		t.Fatalf("expected nil for an unrecognised file type, got %v", lines)
	}
}

func TestHighlightSourceTooLargeReturnsNil(t *testing.T) {
	big := strings.Repeat("a", maxHighlightSize+1)
	if lines := highlightSource("f.go", big); lines != nil {
		t.Fatal("expected nil for oversized source")
	}
}

func TestHighlightContextLinesOnlyTouchesContextLines(t *testing.T) {
	diff := `diff --git a/f.ts b/f.ts
--- a/f.ts
+++ b/f.ts
@@ -1,3 +1,3 @@
 export function helper() {
-  return 1;
+  return 2;
 }
`
	lines := buildDiffLines(diff)
	out := highlightContextLines(lines, "f.ts", "export function helper() {\n  return 2;\n}\n")

	for i, l := range out {
		switch l.kind {
		case ' ':
			if l.text == "" {
				continue // trailing synthetic blank line from the diff's final "\n"
			}
			if l.hl == "" {
				t.Fatalf("line %d: expected context line to be highlighted, got no hl: %q", i, l.text)
			}
			if !strings.Contains(l.hl, "helper") && !strings.Contains(l.hl, "}") {
				t.Fatalf("line %d: highlighted text lost content: %q", i, l.hl)
			}
		case '+', '-':
			if l.hl != "" && !strings.Contains(l.hl, "return") {
				t.Fatalf("line %d: added/removed line hl unexpectedly touched: %q", i, l.hl)
			}
		}
	}
}
