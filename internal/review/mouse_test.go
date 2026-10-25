package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/git"
)

// TestLayoutMatchesWhatIsDrawn is the test this whole design exists for: a
// click can only be trusted if the rectangles hit-tested against are the
// ones on screen. It renders the view and checks that each pane's heading
// really is at the top-left corner layout claims for it.
func TestLayoutMatchesWhatIsDrawn(t *testing.T) {
	m := Model{
		width: 120, height: 40,
		repos: []git.Repo{{Name: "myrepo"}},
	}
	lay := m.layout(m.width, m.height)
	screen := strings.Split(stripANSI(m.View()), "\n")

	// The heading sits on the pane's second row, one cell in from its left
	// border.
	headingAt := func(r rect) string {
		if r.y+1 >= len(screen) {
			t.Fatalf("pane at %+v is off the bottom of a %d-row screen", r, len(screen))
		}
		row := []rune(screen[r.y+1])
		if r.x+1 >= len(row) {
			t.Fatalf("pane at %+v starts past the end of the row", r)
		}
		end := min(r.x+r.w-1, len(row))
		return strings.TrimSpace(string(row[r.x+1 : end]))
	}

	for _, c := range []struct {
		name string
		r    rect
		want string
	}{
		{"repos", lay.repos, "repo"},
		{"worktrees", lay.worktrees, "worktree"},
		{"files", lay.files, "files"},
		{"diff", lay.diff, "diff"},
	} {
		if got := headingAt(c.r); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s pane: layout says %+v, but that row holds %q, not %q",
				c.name, c.r, got, c.want)
		}
	}

	// The panes tile the top row without overlapping or leaving a gap.
	if lay.repos.x+lay.repos.w != lay.files.x {
		t.Errorf("gap or overlap between the repo column and the files pane: %+v %+v", lay.repos, lay.files)
	}
	if lay.files.x+lay.files.w != lay.diff.x {
		t.Errorf("gap or overlap between the files and diff panes: %+v %+v", lay.files, lay.diff)
	}
	if lay.repos.y+lay.repos.h != lay.worktrees.y {
		t.Errorf("repo and worktree panes do not meet: %+v %+v", lay.repos, lay.worktrees)
	}
}

// TestClickSelectsTheRowUnderThePointer covers the arithmetic that turns a
// screen row into a list index — off by one here means clicking a file
// opens its neighbour.
func TestClickSelectsTheRowUnderThePointer(t *testing.T) {
	m := Model{width: 120, height: 40, repos: []git.Repo{
		{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}, {Name: "delta"},
	}}
	lay := m.layout(m.width, m.height)

	// Third item: border, heading, then the rows.
	y := lay.repos.y + 2 + 2
	nm, _ := m.Update(tea.MouseMsg{
		X: lay.repos.x + 3, Y: y,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
	})
	m = nm.(Model)

	if m.repoIdx != 2 {
		t.Fatalf("clicking the third row selected repo %d (%v), want 2",
			m.repoIdx, m.repos[m.repoIdx].Name)
	}
	if m.focus != focusRepos {
		t.Fatalf("a click should focus the pane it landed in, got %v", m.focus)
	}

	// A click on the pane's heading or border selects nothing.
	before := m.repoIdx
	nm, _ = m.Update(tea.MouseMsg{
		X: lay.repos.x + 3, Y: lay.repos.y,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
	})
	m = nm.(Model)
	if m.repoIdx != before {
		t.Fatalf("a click on the border moved the selection to %d", m.repoIdx)
	}
}

// TestWheelScrollsWithoutTakingFocus covers the wheel behaving the way a
// wheel does everywhere else: it scrolls what is under the pointer and
// leaves the focus alone.
func TestWheelScrollsWithoutTakingFocus(t *testing.T) {
	lines := make([]diffLine, 200)
	for i := range lines {
		lines[i] = diffLine{kind: ' ', text: "code"}
	}
	m := Model{width: 120, height: 40, focus: focusFiles, diffLines: lines}
	lay := m.layout(m.width, m.height)

	nm, _ := m.Update(tea.MouseMsg{
		X: lay.diff.x + 5, Y: lay.diff.y + 5, Button: tea.MouseButtonWheelDown,
	})
	m = nm.(Model)

	if m.diffViewTop != wheelRows {
		t.Fatalf("wheel over the diff scrolled it to %d, want %d", m.diffViewTop, wheelRows)
	}
	if m.diffCursor != wheelRows {
		t.Fatalf("the cursor scrolled out of view stays on its top row, got %d", m.diffCursor)
	}
	if m.focus != focusFiles {
		t.Fatalf("the wheel took focus from the files pane, now %v", m.focus)
	}

	nm, _ = m.Update(tea.MouseMsg{
		X: lay.diff.x + 5, Y: lay.diff.y + 5, Button: tea.MouseButtonWheelUp,
	})
	m = nm.(Model)
	if m.diffViewTop != 0 {
		t.Fatalf("wheel up should have scrolled back to 0, got %d", m.diffViewTop)
	}
	if m.diffCursor != wheelRows {
		t.Fatalf("a cursor still in view is left where it is, got %d", m.diffCursor)
	}
}

// TestScrollbar covers the thumb on the diff pane's border: drawn when the
// diff is longer than the pane, and a click on the border line jumping the
// view there.
func TestScrollbar(t *testing.T) {
	lines := make([]diffLine, 200)
	for i := range lines {
		lines[i] = diffLine{kind: ' ', text: "code"}
	}
	m := Model{width: 120, height: 40, focus: focusFiles, diffLines: lines}
	lay := m.layout(m.width, m.height)
	if !strings.Contains(m.View(), thumbGlyph) {
		t.Fatal("a long diff shows a scrollbar")
	}

	last := lay.diff.y + lay.diff.h - 2 - hintRows
	nm, _ := m.Update(tea.MouseMsg{X: lay.diff.x + lay.diff.w - 1, Y: last,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = nm.(Model)
	if want := len(lines) - m.diffInnerHeight(); m.diffViewTop != want {
		t.Fatalf("a click at the bottom of the track goes to the end: top %d, want %d", m.diffViewTop, want)
	}
	nm, _ = m.Update(tea.MouseMsg{X: lay.diff.x + lay.diff.w - 1, Y: lay.diff.y + 2,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = nm.(Model)
	if m.diffViewTop != 0 {
		t.Fatalf("dragging to the top goes to the start, got %d", m.diffViewTop)
	}
	nm, _ = m.Update(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	m = nm.(Model)
	if m.dragging != scrollNone {
		t.Fatal("letting go ends the drag")
	}

	short := Model{width: 120, height: 40, focus: focusFiles, diffLines: lines[:3]}
	if got := renderDiff(short.diffLines, 0, 0, 60, 20, false, "diff", "", lineSpan{}); strings.Contains(got, thumbGlyph) {
		t.Fatal("a diff that fits has no scrollbar")
	}
}

func TestThumb(t *testing.T) {
	for _, c := range []struct{ total, top, innerH, pos, size int }{
		{100, 0, 10, 0, 1},
		{100, 90, 10, 9, 1},
		{20, 0, 10, 0, 5},
		{20, 10, 10, 5, 5},
	} {
		if pos, size := thumb(c.total, c.top, c.innerH); pos != c.pos || size != c.size {
			t.Errorf("thumb(%d, %d, %d) = %d, %d; want %d, %d", c.total, c.top, c.innerH, pos, size, c.pos, c.size)
		}
	}
}

// TestMouseIsOffUntilAskedFor covers the default: text selection belongs to
// the terminal until the user says otherwise.
func TestMouseIsOffUntilAskedFor(t *testing.T) {
	m := New(t.TempDir())
	if m.mouseOn {
		t.Fatal("mouse reporting must be off by default")
	}
	if !m.EnableMouse().mouseOn {
		t.Fatal("EnableMouse (the --mouse flag) should turn it on")
	}

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M")})
	m = nm.(Model)
	if !m.mouseOn || cmd == nil {
		t.Fatalf("M should turn the mouse on and tell the terminal: on=%v cmd=%v", m.mouseOn, cmd != nil)
	}
	// The message types are unexported, so compare against the command
	// bubbletea itself would send.
	if got := cmd(); got != tea.EnableMouseCellMotion() {
		t.Fatalf("expected the enable command, got %#v", got)
	}

	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M")})
	m = nm.(Model)
	if m.mouseOn {
		t.Fatal("M again should turn it back off")
	}
	if got := cmd(); got != tea.DisableMouse() {
		t.Fatalf("expected the disable command, got %#v", got)
	}
}

// TestMenuIsReachableWithoutFunctionKeys covers the Mac case: F1-F2 are the
// brightness and volume keys on a laptop unless the system setting is
// changed, so the menu must have a plain-key route too.
func TestMenuIsReachableWithoutFunctionKeys(t *testing.T) {
	m := Model{width: 120, height: 40}
	press := func() {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
		m = nm.(Model)
	}
	modes := m.menuModes()
	for i := 1; i <= len(modes); i++ {
		press()
		if want := modes[i%len(modes)]; m.mode != want {
			t.Fatalf("press %d: mode %d, want %d", i, m.mode, want)
		}
	}
	if m.mode != modeReview {
		t.Fatalf("cycling all the way round should return to code review, got %d", m.mode)
	}
}

// TestClickLeavingThePeekSelectsTheClickedFile covers a click outside the
// section holding a file opened from a reference: leaving it drops its
// rows, which must not shift the selection onto another file.
func TestClickLeavingThePeekSelectsTheClickedFile(t *testing.T) {
	repo := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts":  "export function helper() { return 1; }\n",
		"more.ts": "export const a = 1;\n",
		"zed.ts":  "export const z = 1;\n",
	})
	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	m.diffCursor = annotationIndex(t, m)
	m.focus = focusDiff
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.peek == nil {
		t.Fatal("setup: expected a peek")
	}

	m.width, m.height = 120, 40
	idx := -1
	for i, r := range m.flatRows() {
		if r.node.path == "lib.ts" {
			idx = i
		}
	}
	lay := m.layout(m.width, m.height)
	nm, _ = m.Update(tea.MouseMsg{
		X: lay.files.x + 3, Y: lay.files.y + 2 + idx,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
	})
	m = nm.(Model)
	if path, ok := m.currentFile(); !ok || path != "lib.ts" {
		t.Fatalf("clicked lib.ts, selected %q", path)
	}
	if m.peek != nil {
		t.Fatal("a click outside the opened section should leave it")
	}
}

// TestDraggingTheFilesScrollbarLeavesThePeek covers the files pane's
// scrollbar while a file opened from a reference is shown: moving the
// cursor with it leaves the peek, as the keys do, so the diff shown is the
// selected file's.
func TestDraggingTheFilesScrollbarLeavesThePeek(t *testing.T) {
	repo := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts":  "export function helper() { return 1; }\n",
		"more.ts": "export const a = 1;\n",
		"zed.ts":  "export const z = 1;\n",
	})
	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	m.diffCursor = annotationIndex(t, m)
	m.focus = focusDiff
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.peek == nil {
		t.Fatal("setup: expected a peek")
	}
	m.width, m.height = 120, 40
	lay := m.layout(m.width, m.height)
	m.dragScrollbar(scrollFiles, lay.files, 0, lay.files.y+lay.files.h)
	if m.peek != nil {
		t.Fatal("dragging the files scrollbar should leave the peek")
	}
	if _, ok := m.currentFile(); !ok {
		t.Fatal("the cursor should be on a file of the change")
	}
}
