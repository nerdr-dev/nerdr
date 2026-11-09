package review

import (
	"strings"
	"testing"
)

func TestOpenWhyDetailIgnoresOtherLines(t *testing.T) {
	m := Model{src: Local{},
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

// TestZoomGivesTheFocusedPaneTheWholeWindow also guards the viewport
// heights the cursor logic derives from these, since a mismatch there
// makes scrolling jump.
func TestZoomGivesTheFocusedPaneTheWholeWindow(t *testing.T) {
	m := Model{src: Local{}, height: 40}

	normalDiff := m.diffInnerHeight()

	m.zoomed = true
	m.focus = focusDiff
	if got, want := m.topPaneHeight(40), 40-m.statusRows()-m.menuRows(); got != want {
		t.Fatalf("zoomed diff should fill everything but the menu and status bar: %d, want %d", got, want)
	}
	if m.diffInnerHeight() <= normalDiff {
		t.Fatalf("zoomed diff viewport (%d) should exceed the normal one (%d)", m.diffInnerHeight(), normalDiff)
	}

	m.zoomed = false
	if m.diffInnerHeight() != normalDiff {
		t.Fatal("unzooming must restore the original geometry")
	}
}

func TestZoomedViewRendersOnlyTheFocusedPane(t *testing.T) {
	m := Model{src: Local{},
		width: 100, height: 30, zoomed: true, focus: focusDiff,
		diffLines: []diffLine{{kind: '+', text: "+unique_diff_marker"}},
	}
	got := m.View()
	if !strings.Contains(got, "unique_diff_marker") {
		t.Fatal("expected the focused diff pane to be rendered")
	}
	if strings.Contains(got, "files (") {
		t.Fatal("expected the other panes to be hidden while zoomed")
	}
	if lines := strings.Count(got, "\n") + 1; lines > 30 {
		t.Fatalf("zoomed view overflows the window: %d lines > 30", lines)
	}
}
