package review

import (
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/git"
)

// TestOpenWhyDetailFocusesThePanel covers the trap this fixes: while the
// diff cursor sat on a ✎ line, → always re-opened the same rationale and
// never advanced focus, so a rationale longer than the panel could not be
// scrolled at all.
func TestOpenWhyDetailFocusesThePanel(t *testing.T) {
	m := Model{
		focus:  focusDiff,
		height: 40,
		diffLines: []diffLine{
			{kind: '@', text: "@@ -1,2 +1,2 @@"},
			{kind: 'w', text: "  ✎ short reason", whyDetail: "the long version"},
			{kind: '+', text: "+something"},
		},
		diffCursor: 1,
	}
	if !m.openWhyDetail() {
		t.Fatal("expected the ✎ line under the cursor to be opened")
	}
	if m.focus != focusRelations {
		t.Fatalf("expected focus to move into the panel so ↑/↓ scroll the text, got %v", m.focus)
	}
	if m.relationsTab != relationsTabWhy {
		t.Fatalf("expected the why tab to be selected, got %d", m.relationsTab)
	}
	if m.whyDetail != "the long version" {
		t.Fatalf("expected the full rationale to be loaded, got %q", m.whyDetail)
	}
}

func TestOpenWhyDetailIgnoresOtherLines(t *testing.T) {
	m := Model{
		focus:      focusDiff,
		height:     40,
		diffLines:  []diffLine{{kind: '+', text: "+something"}},
		diffCursor: 0,
	}
	if m.openWhyDetail() {
		t.Fatal("expected a plain added line not to be treated as a rationale")
	}
	if m.focus != focusDiff {
		t.Fatalf("focus must stay put when there is nothing to open, got %v", m.focus)
	}
}

// TestPageFollowsFocus covers PgUp/PgDn paging whichever pane has focus,
// rather than always paging the diff even while reading the panel.
func TestPageFollowsFocus(t *testing.T) {
	rows := make([]relLine, 60)
	for i := range rows {
		rows[i] = relLine{text: "line"}
	}
	lines := make([]diffLine, 60)
	for i := range lines {
		lines[i] = diffLine{kind: ' ', text: "code"}
	}
	m := Model{height: 40, diffLines: lines, relRows: rows}

	m.focus = focusDiff
	m.page(1)
	if m.diffCursor == 0 {
		t.Fatal("expected pgdown to move the diff cursor while the diff has focus")
	}
	if m.relCursor != 0 {
		t.Fatalf("expected the panel cursor to stay put, got %d", m.relCursor)
	}

	m.focus = focusRelations
	diffBefore := m.diffCursor
	m.page(1)
	if m.relCursor == 0 {
		t.Fatal("expected pgdown to move the panel cursor while the panel has focus")
	}
	if m.diffCursor != diffBefore {
		t.Fatalf("expected the diff cursor to stay put, got %d (was %d)", m.diffCursor, diffBefore)
	}
}

// TestZoomGivesTheFocusedPaneTheWholeWindow also guards the viewport
// heights the cursor logic derives from these, since a mismatch there
// makes scrolling jump.
func TestZoomGivesTheFocusedPaneTheWholeWindow(t *testing.T) {
	m := Model{height: 40}

	normalDiff := m.diffInnerHeight()
	normalRel := m.relationsInnerHeight()

	m.zoomed = true
	m.focus = focusDiff
	if got := m.topPaneHeight(40); got != 38 {
		t.Fatalf("zoomed diff should fill everything but the menu and status bar, got %d", got)
	}
	if m.diffInnerHeight() <= normalDiff {
		t.Fatalf("zoomed diff viewport (%d) should exceed the normal one (%d)", m.diffInnerHeight(), normalDiff)
	}

	m.focus = focusRelations
	if got := m.relationsPaneHeight(40); got != 38 {
		t.Fatalf("zoomed panel should fill everything but the menu and status bar, got %d", got)
	}
	if m.relationsInnerHeight() <= normalRel {
		t.Fatalf("zoomed panel viewport (%d) should exceed the normal one (%d)", m.relationsInnerHeight(), normalRel)
	}

	m.zoomed = false
	if m.diffInnerHeight() != normalDiff || m.relationsInnerHeight() != normalRel {
		t.Fatal("unzooming must restore the original geometry")
	}
}

func TestZoomedViewRendersOnlyTheFocusedPane(t *testing.T) {
	m := Model{
		width: 100, height: 30, zoomed: true, focus: focusDiff,
		diffLines: []diffLine{{kind: '+', text: "+unique_diff_marker"}},
		repos:     []git.Repo{{Name: "unique_repo_marker"}},
	}
	got := m.View()
	if !strings.Contains(got, "unique_diff_marker") {
		t.Fatal("expected the focused diff pane to be rendered")
	}
	if strings.Contains(got, "unique_repo_marker") {
		t.Fatal("expected the other panes to be hidden while zoomed")
	}
	if lines := strings.Count(got, "\n") + 1; lines > 30 {
		t.Fatalf("zoomed view overflows the window: %d lines > 30", lines)
	}
}
