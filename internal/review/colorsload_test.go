package review

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/nerdr-dev/nerdr/internal/git"
)

// cannedSource answers a diff, the file now and the file before from
// strings, counting the reads of the file before.
type cannedSource struct {
	Local
	diff, src, old string
	olds           *atomic.Int32
}

func (s cannedSource) Diff(git.Comparison, string, bool, string) (string, error) {
	return s.diff, nil
}

func (s cannedSource) ReadFile(string, string) (string, error) { return s.src, nil }

func (s cannedSource) OldSource(git.Comparison, string) (string, error) {
	s.olds.Add(1)
	return s.old, nil
}

// The file before the change is read only to colour removed lines: not
// for a diff that removes none, as a binary file's, nor for a scheme that
// leaves them in the tint's colour.
func TestOldSourceReadOnlyForRemovedLines(t *testing.T) {
	cases := []struct {
		name, path, diff, scheme string
		reads                    int32
	}{
		{"removed lines", "p.go", "@@ -1 +1 @@\n-var a = 1\n+var b = 1", "github", 1},
		{"binary file", "p.go", "Binary files a/p.go and /dev/null differ", "github", 0},
		{"added lines only", "p.go", "@@ -0,0 +1 @@\n+var b = 1", "github", 0},
		{"no lexer", "p.not-a-language", "@@ -1 +1 @@\n-a\n+b", "github", 0},
		{"nerdr's colours", "p.go", "@@ -1 +1 @@\n-var a = 1\n+var b = 1", "nerdr", 0},
	}
	for _, c := range cases {
		var n atomic.Int32
		r := diffReq{
			src:  cannedSource{diff: c.diff, src: "var b = 1\n", old: "var a = 1\n", olds: &n},
			path: c.path, original: c.path, scheme: diffSchemes[c.scheme],
		}
		r.read()
		if got := n.Load(); got != c.reads {
			t.Errorf("%s: read the file before %d times, want %d", c.name, got, c.reads)
		}
	}
}

// The whole new file has no removed lines to count the old side by, so
// its lines have the new number only.
func TestWholeFileHasNewNumbersOnly(t *testing.T) {
	var n atomic.Int32
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "p.txt"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := diffReq{
		src: cannedSource{diff: "@@ -1,2 +1,1 @@\n-gone\n kept", src: "kept\n", olds: &n},
		wt:  wt, path: "p.txt", original: "p.txt", mode: diffModeNewFull, scheme: diffSchemes["github"],
	}
	// FullNewFile is Local's: the file with the change marked, from the diff.
	lines := r.read().lines
	if len(lines) == 0 {
		t.Fatal("the whole new file should have lines")
	}
	for _, l := range lines {
		if l.oldLine != 0 {
			t.Errorf("%q has old number %d in the whole new file", l.text, l.oldLine)
		}
	}
}

// Diffs read in the background while c changes the colours are drawn in
// the colours each was asked in, and none races another (go test -race).
func TestLoadsWhileColoursChange(t *testing.T) {
	defer useDiffColors(DiffColors[0])
	var n atomic.Int32
	src := cannedSource{
		diff: "@@ -1,2 +1,2 @@\n package p\n-var a = 1\n+var b = 1",
		src:  "package p\nvar b = 1\n", old: "package p\nvar a = 1\n", olds: &n,
	}
	var wg sync.WaitGroup
	for i := range 8 {
		name := DiffColors[i%len(DiffColors)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := diffReq{src: src, path: "p.go", original: "p.go", scheme: diffSchemes[name]}
			r.read()
		}()
		useDiffColors(DiffColors[(i+1)%len(DiffColors)])
	}
	wg.Wait()
}

// GitHub's pane sets its text colour with its background, at the start of
// a row and after every reset, so code with no colour of its own is not
// the terminal's: dark on a light terminal.
func TestPaneSetsTextColour(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer useDiffColors(DiffColors[0])
	useDiffColors("github")
	set := "\x1b[48;2;13;17;23;38;2;240;246;252m"
	row := onPaneBG("plain\x1b[0m code\x1b[m end")
	if !strings.HasPrefix(row, set) || strings.Count(row, set) != 3 {
		t.Errorf("the pane's colours should start the row and follow each reset: %q", row)
	}
	useDiffColors("nerdr")
	if onPaneBG("plain") != "plain" {
		t.Error("nerdr's colours leave the terminal's background")
	}
}

// The old file is shown in one column, its own numbers: none of its lines
// is a line of the new file. Nor are a peeked file's two columns.
func TestOldFileAndPeekHaveOneColumn(t *testing.T) {
	var n atomic.Int32
	wt := t.TempDir()
	r := diffReq{
		src: cannedSource{src: "kept\n", old: "gone\nkept\n", olds: &n},
		wt:  wt, path: "p.txt", original: "p.txt", mode: diffModeOld, scheme: diffSchemes["github"],
	}
	r.src = oldFileSource{r.src.(cannedSource)}
	lines := r.read().lines
	if len(lines) < 2 {
		t.Fatalf("the old file should have its lines: %+v", lines)
	}
	for _, l := range lines {
		if l.oldLine != 0 {
			t.Errorf("%q has a second number, %d, in the old file", l.text, l.oldLine)
		}
	}
	for _, l := range plainSourceLines(githubDark, "p.txt", "a\nb\n") {
		if l.oldLine != 0 {
			t.Errorf("a peeked file's %q has a second number", l.text)
		}
	}
}

// oldFileSource answers the old file as Local formats it, from old.
type oldFileSource struct{ cannedSource }

func (s oldFileSource) OldFile(git.Comparison, string) (string, error) {
	return "@@ -1,2 +1,2 @@\n gone\n kept\n", nil
}
