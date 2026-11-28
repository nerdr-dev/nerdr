package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// wrapLines is a diff with lines of every length, some wrapping onto
// several rows.
func wrapLines(n int) []diffLine {
	lines := make([]diffLine, n)
	for i := range lines {
		kind := " "
		switch i % 3 {
		case 1:
			kind = "+"
		case 2:
			kind = "-"
		}
		lines[i] = diffLine{kind: kind[0], text: kind + strings.Repeat("word ", i%40), newLine: i + 1}
	}
	return lines
}

// The rows a diff wraps to, counted once, put every line on the row the
// line-by-line count does, and every row under the line it shows.
func TestDiffViewRowsMatchCountingThem(t *testing.T) {
	v := newDiffView(wrapLines(300), 70, 20)
	row := 0
	for i := range v.lines {
		n := len(wrapStarts(diffPlain(v.lines[i]), v.bodyW(i)))
		if got := v.rowOf(i); got != row {
			t.Fatalf("rowOf(%d) = %d, want %d", i, got, row)
		}
		if v.rows(i) != n {
			t.Fatalf("rows(%d) = %d, want %d", i, v.rows(i), n)
		}
		for r := row; r < row+n; r++ {
			if got := v.lineAt(0, r); got != i {
				t.Fatalf("lineAt(row %d) = %d, want %d", r, got, i)
			}
		}
		row += n
	}
	if v.rowOf(len(v.lines)) != row || v.rowOf(len(v.lines)+5) != row || v.rowOf(-1) != 0 {
		t.Fatalf("rowOf past the ends: %d, %d, %d; %d rows", v.rowOf(len(v.lines)), v.rowOf(len(v.lines)+5), v.rowOf(-1), row)
	}
	if v.lineAt(0, row) != -1 || v.lineAt(0, -1) != -1 {
		t.Fatal("no line is outside the diff")
	}
}

// The panel's frames wrap a diff once, not in every frame, until its
// lines or its width change.
func TestRowCacheKeepsTheRowsBetweenFrames(t *testing.T) {
	c := &rowCache{}
	lines := wrapLines(100)
	first := newCachedDiffView(c, lines, 70, 20).first
	if again := newCachedDiffView(c, lines, 70, 20).first; &again[0] != &first[0] {
		t.Fatal("the same lines at the same width are not wrapped again")
	}
	if wider := newCachedDiffView(c, lines, 90, 20).first; &wider[0] == &first[0] ||
		wider[len(lines)] == first[len(lines)] {
		t.Fatal("a new width wraps again")
	}
	changed := append([]diffLine(nil), lines...)
	changed[5].text = "+" + strings.Repeat("longer ", 60)
	v := newCachedDiffView(c, changed, 70, 20)
	if want := newDiffView(changed, 70, 20).first; v.first[len(changed)] != want[len(changed)] {
		t.Fatalf("a changed line is wrapped again: %d rows, want %d", v.first[len(changed)], want[len(changed)])
	}
	if n := len(newCachedDiffView(c, changed[:10], 70, 20).first); n != 11 {
		t.Fatalf("fewer lines are counted again: %d starts", n)
	}
}

// A line too long to be worth it, a minified file's, gets no word marks;
// a short pair still does.
func TestLongLinesAreNotWordDiffed(t *testing.T) {
	long := strings.Repeat("x", wordDiffMax)
	lines := buildDiffLines("@@ -1,2 +1,2 @@\n-" + long + "a\n-int a = 1;\n+" + long + "b\n+int a = 2;\n")
	var hl []string
	for _, dl := range lines {
		if dl.kind == '+' || dl.kind == '-' {
			hl = append(hl, dl.hl)
		}
	}
	if len(hl) != 4 {
		t.Fatalf("four changed lines, got %d", len(hl))
	}
	if hl[0] != "" || hl[2] != "" {
		t.Error("a line past wordDiffMax is not word-diffed")
	}
	if hl[1] == "" || hl[3] == "" {
		t.Error("a short pair still is")
	}
	if wordDiffTimeout > 100*time.Millisecond {
		t.Errorf("a pair may take %v", wordDiffTimeout)
	}
}

// The diff's lines are built where the diff is read, in the background,
// and put in place as they come.
func TestDiffLinesAreBuiltWhereTheDiffIsRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := diffReq{src: Local{}, wt: dir, path: "a.go", peek: true}.read()
	if msg.err != nil || !msg.built || len(msg.lines) != 3 || msg.lines[2].text != " func A() {}" {
		t.Fatalf("read: built %v, %d lines, err %v", msg.built, len(msg.lines), msg.err)
	}

	m := &Model{curWT: "/wt", peek: &peekTarget{file: "a.go"}}
	msg.wt = "/wt"
	msg.lines = []diffLine{{kind: ' ', text: " built", newLine: 1}}
	m.onDiff(msg)
	if len(m.diffLines) != 1 || m.diffLines[0].text != " built" {
		t.Fatalf("onDiff builds the lines again: %+v", m.diffLines)
	}
}

// A click in the zoomed diff puts its cursor on the row clicked, as it
// does in the diff beside the files.
func TestClickInTheZoomedDiff(t *testing.T) {
	m := Model{src: Local{}, width: 120, height: 40, focus: focusDiff, zoomed: true, diffLines: wrapLines(200)}
	m.diffViewTop = m.diffView().rowOf(50)
	lay := m.layout(m.width, m.height)
	if lay.zoom.w == 0 {
		t.Fatal("no zoomed pane")
	}
	m = m.send(tea.MouseMsg{X: lay.zoom.x + 5, Y: lay.zoom.y + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.diffCursor != 50 {
		t.Fatalf("the first row shown is line 50, the cursor went to %d", m.diffCursor)
	}
}

// A click in the overview picks the row clicked, even once the list has
// scrolled to keep its cursor in view.
func TestClickInTheScrolledOverview(t *testing.T) {
	m := Model{src: Local{}, width: 120, height: 20, overview: true, ovListed: true}
	for i := range 100 {
		m.ovItems = append(m.ovItems, ovItem{label: "wt" + strings.Repeat("x", i%3)})
	}
	m.ovCursor = 80
	lay := m.layout(m.width, m.height)
	innerH := lay.overview.h - 3 - hintRows
	m = m.send(tea.MouseMsg{X: 5, Y: lay.overview.y + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if want := 80 - innerH + 1; m.ovCursor != want {
		t.Fatalf("the first row shown is item %d, the cursor went to %d", want, m.ovCursor)
	}
}

// The PR screen scrolls by the rows it shows: with the filter's row
// below it too.
func TestPRConvInnerHeightIsWhatIsShown(t *testing.T) {
	m := prModel(samplePR())
	want := m.height - m.menuRows() - 3 - hintRows
	if got := m.prConvInnerHeight(); got != want {
		t.Fatalf("prConvInnerHeight = %d, want %d", got, want)
	}
	m.filtering = true
	if got := m.prConvInnerHeight(); got != want-1 {
		t.Fatalf("filtering: prConvInnerHeight = %d, want %d", got, want-1)
	}
}

// The fix prompt lists the checks that failed, not those still to run.
func TestFixPromptListsOnlyFailedChecks(t *testing.T) {
	pr := samplePR()
	pr.Checks = []forge.Check{
		{Name: "lint", Status: "FAILURE"},
		{Name: "queued", Status: "QUEUED"},
		{Name: "running", Status: "IN_PROGRESS"},
		{Name: "pending", Status: "PENDING"},
		{Name: "ok", Status: "SUCCESS"},
	}
	got := fixPrompt(pr, "log")
	if !strings.Contains(got, "lint (failure)") {
		t.Errorf("the failed check is listed:\n%s", got)
	}
	for _, name := range []string{"queued", "running", "pending", "ok"} {
		if strings.Contains(got, "- "+name+" ") {
			t.Errorf("%s is listed as failed:\n%s", name, got)
		}
	}
}

// What is copied keeps the file's empty lines, and a line with no diff
// sign in front whole.
func TestCopyKeepsEmptyLinesAndUnsignedOnes(t *testing.T) {
	m := Model{diffLines: []diffLine{
		{kind: ' ', text: " a"},
		{kind: ' ', text: ""},
		{kind: '+', text: "+b"},
		{kind: ' ', text: "(file is not in the worktree)"},
	}, diffSel: lineSpan{on: true, from: 0, to: 3}}
	if got, want := m.selText(), "a\n\nb\n(file is not in the worktree)"; got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
}

// A changed line in a pane with no room for its text draws only its
// sign column, not all of the line.
func TestDiffRowsWithNoWidth(t *testing.T) {
	for _, w := range []int{0, -3} {
		for _, r := range diffRows(diffLine{kind: '+', text: "+" + strings.Repeat("code ", 20)}, w) {
			if got := lipgloss.Width(r); got > 1 {
				t.Fatalf("width %d: a row %d wide: %q", w, got, r)
			}
		}
	}
}
