package review

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// annotationIndex finds the row of the first *jumpable* reference line
// (an 'a' line with a refFile set — the header line above it isn't
// jumpable) in m.diffLines, failing the test if there isn't one.
func annotationIndex(t *testing.T, m Model) int {
	t.Helper()
	for i, dl := range m.diffLines {
		if dl.kind == 'a' && dl.refFile != "" {
			return i
		}
	}
	t.Fatalf("no jumpable annotation line found in diff pane; lines: %#v", m.diffLines)
	return -1
}

// openFileDiff drives the model from the repo root down to path's diff pane
// inside the given worktree (assumed to already be selected as m.wtIdx 0,
// the single worktree of a repo with no linked worktrees).
func openFileDiff(t *testing.T, m Model, path string) Model {
	t.Helper()
	send := func(msg tea.Msg) Model {
		nm, _ := m.Update(msg)
		return nm.(Model)
	}
	m = send(tea.KeyMsg{Type: tea.KeyRight}) // repos -> worktrees
	m = send(tea.KeyMsg{Type: tea.KeyRight}) // worktrees -> files
	// The project the annotations come from loads in the background; hand
	// it over as the program would once it has.
	m = send(loadContextCmd(m.curWT)())
	found := false
	for i, r := range m.flatRows() {
		if !r.node.isDir && r.node.path == path {
			m.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("file %q not found in tree", path)
	}
	m = send(tea.KeyMsg{Type: tea.KeyEnter}) // files -> diff
	if m.diff == "" {
		t.Fatalf("expected non-empty diff for %q", path)
	}
	return m
}

func TestJumpToReferenceOutsideTree(t *testing.T) {
	repo := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts": "export function helper() { return 1; }\n",
	})

	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	idx := annotationIndex(t, m)
	if !strings.Contains(m.diffLines[idx].text, "caller.ts") {
		t.Fatalf("expected annotation to mention caller.ts, got %q", m.diffLines[idx].text)
	}
	m.diffCursor = idx
	m.focus = focusDiff

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)

	if m.peek == nil || m.peek.file != "caller.ts" {
		t.Fatalf("expected peek at caller.ts, got %#v", m.peek)
	}
	if !strings.Contains(m.diff, "export function use()") {
		t.Fatalf("expected peek diff to show caller.ts content, got %q", m.diff)
	}
	if len(m.jumpHistory) != 1 {
		t.Fatalf("expected one saved jump state, got %d", len(m.jumpHistory))
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = nm.(Model)
	if m.peek != nil {
		t.Fatalf("expected peek cleared after jumping back, got %#v", m.peek)
	}
	if !strings.Contains(m.diff, "export function helper()") {
		t.Fatalf("expected to be back on lib.ts's diff, got %q", m.diff)
	}
}

// TestJumpToReferenceInsideTree covers the exact bug reported: jumping to a
// reference deep in a file (not line 1) must land on that line, not near
// the top — the diff pane's synthetic full-file diff has header/hunk rows
// ahead of the actual content that a naive line-1 index would land inside.
func TestJumpToReferenceInsideTree(t *testing.T) {
	// Pad caller.ts so the reference isn't near line 1 — a header-offset
	// bug would otherwise be invisible. Reference "helper" only via a
	// qualified access (lib.helper()), not also via a destructured import,
	// so there's exactly one word-index match to jump to.
	callerLines := []string{"import * as lib from './lib';", "", "// padding", "// padding", "// padding",
		"// padding", "// padding", "// padding"}
	refLine := len(callerLines) + 1 // the "export function use()" line below
	callerLines = append(callerLines, "export function use() { return lib.helper(); }")
	caller := strings.Join(callerLines, "\n") + "\n"

	repo := setupJumpRepo(t, nil, map[string]string{
		"lib.ts":    "export function helper() { return 1; }\n",
		"caller.ts": caller,
	})

	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	idx := annotationIndex(t, m)
	m.diffCursor = idx
	m.focus = focusDiff

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)

	if m.peek != nil {
		t.Fatalf("expected no peek (caller.ts is in the tree), got %#v", m.peek)
	}
	if m.diffMode != diffModeNewFull {
		t.Fatalf("expected diffMode switched to full-file view, got %v", m.diffMode)
	}
	path, ok := m.currentFile()
	if !ok || path != "caller.ts" {
		t.Fatalf("expected tree cursor on caller.ts, got %q ok=%v", path, ok)
	}

	if m.diffCursor < 0 || m.diffCursor >= len(m.diffLines) {
		t.Fatalf("diffCursor %d out of range (%d lines)", m.diffCursor, len(m.diffLines))
	}
	landed := m.diffLines[m.diffCursor]
	if landed.newLine != refLine {
		t.Fatalf("expected to land on line %d, landed on line %d (row text %q)", refLine, landed.newLine, landed.text)
	}
	if !strings.Contains(landed.text, "export function use()") {
		t.Fatalf("expected to land on the reference line itself, got %q", landed.text)
	}
}

// TestJumpToEachOfMultipleReferences covers a symbol used from more than
// one place: each reference must be its own selectable row with its own
// jump target, not all collapsed onto one line that only jumps to the first.
func TestJumpToEachOfMultipleReferences(t *testing.T) {
	repo := setupJumpRepo(t, nil, map[string]string{
		"lib.ts": "export function helper() { return 1; }\n",
		"a.ts":   "import * as lib from './lib';\nexport function useA() { return lib.helper(); }\n",
		"b.ts":   "import * as lib from './lib';\n\n\nexport function useB() { return lib.helper(); }\n",
	})

	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")

	var refRows []int
	for i, dl := range m.diffLines {
		if dl.kind == 'a' && dl.refFile != "" {
			refRows = append(refRows, i)
		}
	}
	if len(refRows) != 2 {
		t.Fatalf("expected 2 individually jumpable reference rows (a.ts and b.ts), got %d: %#v", len(refRows), m.diffLines)
	}

	targets := map[string]bool{}
	for _, row := range refRows {
		mm := m
		mm.diffCursor = row
		mm.focus = focusDiff
		nm, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
		mm = nm.(Model)
		path, ok := mm.currentFile()
		if !ok {
			t.Fatalf("row %d: expected a file to be selected after jumping", row)
		}
		targets[path] = true
	}
	if !targets["a.ts"] || !targets["b.ts"] {
		t.Fatalf("expected jumping from each row to reach a distinct file, got %v", targets)
	}
}

// setupJumpRepo creates a repo with committed files, then adds extra
// uncommitted files (making them appear as changed/new in the tree).
func setupJumpRepo(t *testing.T, committed, uncommitted map[string]string) string {
	t.Helper()
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	runGit(t, "", "init", "-q", repo)
	writeFile(t, filepath.Join(repo, "README.md"), "x\n")
	for name, content := range committed {
		writeFile(t, filepath.Join(repo, name), content)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	for name, content := range uncommitted {
		writeFile(t, filepath.Join(repo, name), content)
	}
	return parent
}

// TestPeekIsMarkedInTheFileTree covers the tree marking what the diff pane
// actually shows: jumping to a file outside the change set used to leave
// the cursor on the file jumped from.
func TestPeekIsMarkedInTheFileTree(t *testing.T) {
	repo := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts": "export function helper() { return 1; }\n",
	})

	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	m.diffCursor = annotationIndex(t, m)
	m.focus = focusDiff

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)

	if m.peek == nil {
		t.Fatal("expected a peek at caller.ts")
	}
	path, ok := m.currentFile()
	if !ok || path != "caller.ts" {
		t.Fatalf("expected the tree cursor to mark the peeked file, got %q ok=%v", path, ok)
	}

	// Going back drops the row again and restores the original selection.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = nm.(Model)
	for _, r := range m.flatRows() {
		if r.node.path == "caller.ts" {
			t.Fatal("the peek row should be gone once the peek is left")
		}
	}
	path, ok = m.currentFile()
	if !ok || path != "lib.ts" {
		t.Fatalf("expected to be back on lib.ts, got %q ok=%v", path, ok)
	}
}

func TestMovingTheCursorLeavesThePeek(t *testing.T) {
	repo := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts": "export function helper() { return 1; }\n",
	})

	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	m.diffCursor = annotationIndex(t, m)
	m.focus = focusDiff
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)

	m.focus = focusFiles
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)

	if m.peek != nil {
		t.Fatal("browsing the tree should leave the peek")
	}
	for _, r := range m.flatRows() {
		if r.node.path == "caller.ts" {
			t.Fatal("a stale peek row would leave the cursor indexing rows that no longer exist")
		}
	}
}

// TestLeavingTheReviewLayoutEndsThePeek covers a file opened from a
// reference still being listed after going to another screen and back,
// where nothing said how it got there and it read as part of the change.
func TestLeavingTheReviewLayoutEndsThePeek(t *testing.T) {
	repo := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts": "export function helper() { return 1; }\n",
	})

	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	m.diffCursor = annotationIndex(t, m)
	m.focus = focusDiff
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.peek == nil {
		t.Fatal("expected a peek at caller.ts")
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	nm, _ = nm.(Model).Update(tea.KeyMsg{Type: tea.KeyF1})
	m = nm.(Model)
	if m.peek != nil || len(m.jumpHistory) != 0 {
		t.Fatalf("expected the peek and its history gone, got %#v and %d", m.peek, len(m.jumpHistory))
	}
	for _, r := range m.flatRows() {
		if r.node.path == "caller.ts" {
			t.Fatal("the peek row should be gone after leaving the review layout")
		}
	}
	if path, ok := m.currentFile(); !ok || path != "lib.ts" {
		t.Fatalf("expected to be back on lib.ts, got %q ok=%v", path, ok)
	}
}
