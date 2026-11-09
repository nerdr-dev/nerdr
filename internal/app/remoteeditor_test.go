package app

import (
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/review"
)

// TestTerminalEditorRunsWhereCoreIs: with core on another machine, a file
// opened from the review panel in a terminal editor opens in a pane of
// the tab, run by core where the file is, not over the TUI.
func TestTerminalEditorRunsWhereCoreIs(t *testing.T) {
	t.Cleanup(func() { editor.File = "" })
	editor.File = "true" // a terminal editor that is there, and ends
	m := testModel(t)
	m.SetCoreHost("box")
	tb := m.Cur()
	tb.Dir = t.TempDir() // for the editor to start in
	tb.Worktrees = nil
	before := len(tb.Panes())
	m.openEditor(review.EditMsg{Dir: tb.Dir, File: tb.Dir + "/a.go", Line: 3})
	if m.editor != nil {
		t.Fatal("the editor took the TUI's screen")
	}
	ps := tb.Panes()
	if len(ps) != before+1 || ps[len(ps)-1].Kind != core.PaneShell {
		t.Fatalf("no pane for the editor: %d panes, were %d", len(ps), before)
	}
	if sh := ps[len(ps)-1].Shell; sh != nil {
		t.Cleanup(sh.Close)
	}
}
