package review

import (
	"strings"
	"testing"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/nerdr-dev/nerdr/internal/analysis"
)

func TestBuildDiffLinesWordHighlight(t *testing.T) {
	diff := `diff --git a/f.txt b/f.txt
index 0000000..1111111 100644
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,3 @@
 context line
-old value here
+new value there
 unchanged
`
	lines := buildDiffLines(diff)
	if len(lines) == 0 {
		t.Fatal("no lines")
	}

	var removedHl, addedHl string
	for _, l := range lines {
		switch l.kind {
		case '-':
			removedHl = l.hl
		case '+':
			addedHl = l.hl
		}
	}
	if removedHl == "" || addedHl == "" {
		t.Fatalf("expected word-level highlight on +/- lines, removed=%q added=%q", removedHl, addedHl)
	}
	if !strings.Contains(removedHl, "old") || !strings.Contains(addedHl, "new") {
		t.Fatalf("highlight should keep original words: removed=%q added=%q", removedHl, addedHl)
	}
}

func TestAnnotateDiffLinesInsertsReferences(t *testing.T) {
	diff := `diff --git a/lib.ts b/lib.ts
--- a/lib.ts
+++ b/lib.ts
@@ -1,1 +1,1 @@
+export function helper() { return 1; }
`
	lines := buildDiffLines(diff)
	symbols := []analysis.Symbol{{Name: "helper", Kind: "function"}}
	refs := [][]analysis.Ref{{{File: "src/index.ts", Line: 4}}}

	out := annotateDiffLines(lines, symbols, refs)
	// A header line ("↳ helper used (1):") plus one jumpable reference row.
	if len(out) != len(lines)+2 {
		t.Fatalf("expected two annotation lines inserted, got %d lines (from %d)", len(out), len(lines))
	}

	var found bool
	for i, l := range out {
		if l.kind != '+' {
			continue
		}
		if i+2 >= len(out) || out[i+1].kind != 'a' || out[i+2].kind != 'a' {
			t.Fatalf("expected annotation lines right after the '+' declaration line")
		}
		if !strings.Contains(out[i+1].text, "helper") {
			t.Fatalf("header text = %q, want it to mention helper", out[i+1].text)
		}
		ref := out[i+2]
		if !strings.Contains(ref.text, "src/index.ts:4") {
			t.Fatalf("reference text = %q, want it to mention src/index.ts:4", ref.text)
		}
		if ref.refFile != "src/index.ts" || ref.refLine != 4 {
			t.Fatalf("reference jump target = %q:%d, want src/index.ts:4", ref.refFile, ref.refLine)
		}
		found = true
	}
	if !found {
		t.Fatal("no '+' line found to annotate")
	}
}

func TestAnnotateDiffLinesNoSymbolsNoOp(t *testing.T) {
	diff := "+export function helper() { return 1; }\n"
	lines := buildDiffLines(diff)
	out := annotateDiffLines(lines, nil, nil)
	if len(out) != len(lines) {
		t.Fatalf("expected no-op with no symbols, got %d lines from %d", len(out), len(lines))
	}
}

// TestWordHighlightPairsOnlyWithinARunOfChanges covers the confetti that
// pairing across a hunk produced: pure additions above a context line were
// character-diffed against removals below it, and unrelated lines came out
// speckled with matched letters.
func TestWordHighlightPairsOnlyWithinARunOfChanges(t *testing.T) {
	lines := buildDiffLines(strings.Join([]string{
		"@@ -1,4 +1,4 @@",
		"+// a brand new comment line",
		" unchanged",
		"-// whichever hunk currently contains that fragment",
		"+// whichever line currently contains that fragment",
		"-completely different text here",
		"+nothing in common at all, really",
	}, "\n"))
	if lines[1].hl != "" {
		t.Errorf("a pure addition must not be word-diffed against a later removal: %q", lines[1].hl)
	}
	if lines[3].hl == "" || lines[4].hl == "" {
		t.Error("an edited line pair in the same run should get word marks")
	}
	if lines[5].hl != "" || lines[6].hl != "" {
		t.Error("a rewritten pair with little in common should get the row tint only")
	}
}

func TestBuildDiffLinesShowsControlCharactersInsteadOfSendingThem(t *testing.T) {
	diff := "@@ -0,0 +1,2 @@\r\n+a\x1b[2Jb\tc\r\n+d\re\n"
	lines := buildDiffLines(diff)
	var got []string
	for _, l := range lines {
		got = append(got, l.text)
	}
	want := []string{"@@ -0,0 +1,2 @@", "+a␛[2Jb\tc", "+d␍e"} // the line end ends the last line, it starts none
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestIncompleteReferencesShownInDiff(t *testing.T) {
	m := Model{
		symbols:            []analysis.Symbol{{Name: "helper", Kind: "function"}},
		symbolRefs:         [][]analysis.Ref{nil},
		analysisIncomplete: "directory entry limit reached",
	}
	lines := m.displayLines("a.ts", "@@ -0,0 +1 @@\n+export function helper() {}\n", "", false)
	var text string
	for _, line := range lines {
		text += line.text + "\n"
	}
	if !strings.Contains(text, "Reference analysis incomplete: directory entry limit reached") ||
		!strings.Contains(text, "no references found (analysis incomplete)") ||
		strings.Contains(text, "no other references") {
		t.Fatalf("misleading incomplete diff:\n%s", text)
	}
	m.symbolRefs = [][]analysis.Ref{{{File: "caller.ts", Line: 1}}}
	lines = m.displayLines("a.ts", "@@ -0,0 +1 @@\n+export function helper() {}\n", "", false)
	text = ""
	for _, line := range lines {
		text += line.text + "\n"
	}
	if !strings.Contains(text, "at least 1; analysis incomplete") {
		t.Fatalf("unqualified count: %s", text)
	}
	m.symbols = nil
	lines = m.displayLines("a.ts", "", "", false)
	if len(lines) == 0 || !strings.Contains(lines[0].text, "Reference analysis incomplete") {
		t.Fatal("warning hidden without symbols")
	}
}

// In GitHub's colours, an added or removed line's code is coloured by its
// syntax on the tint, the removed one's from the file before, and the
// changed words keep their stronger tint.
func TestChangedLinesSyntax(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer useDiffColors(DiffColors[0])
	useDiffColors("github")
	old := "package p\n\nvar a = 1\n"
	src := "package p\n\nvar b = 1\n"
	lines := builtDiffLines("p.go", "@@ -3 +3 @@\n-var a = 1\n+var b = 1", src, true, old, curScheme)
	kw := syntaxLipglossStyle(chroma.Keyword)
	if want := kw.Background(delStyle.GetBackground()).Render("var"); !strings.Contains(lines[1].hl, want) {
		t.Errorf("the removed line's keyword should be in its syntax colour on the tint: %q", lines[1].hl)
	}
	if want := kw.Background(addStyle.GetBackground()).Render("var"); !strings.Contains(lines[2].hl, want) {
		t.Errorf("the added line's keyword should be in its syntax colour on the tint: %q", lines[2].hl)
	}
	name := syntaxLipglossStyle(highlightTokens("p.go", src)[2][2].Type)
	if want := name.Background(wordAddStyle.GetBackground()).Render("b"); !strings.Contains(lines[2].hl, want) {
		t.Errorf("the changed word should keep its stronger tint: %q", lines[2].hl)
	}
}

// A diff ends in a line end, which is no line of its own: a diff that
// adds one line has that line and its header, no empty line 2 after it.
// An empty line of the file is a context line, a space.
func TestDiffLineEndMakesNoLine(t *testing.T) {
	lines := buildDiffLines("@@ -0,0 +1 @@\n+only\n")
	if len(lines) != 2 || lines[1].newLine != 1 {
		t.Fatalf("lines = %+v, want the header and line 1", lines)
	}
	lines = buildDiffLines("@@ -1,2 +1,2 @@\n-a\n+b\n \n")
	if l := lines[len(lines)-1]; l.kind != ' ' || l.newLine != 2 || l.oldLine != 2 {
		t.Fatalf("an empty context line should stay, numbered: %+v", l)
	}
}
