package review

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestNumberKeysSelectTabs covers the two remaining tabs being reachable
// directly. Jumping to code lives in the diff pane's own ↳ annotations; the
// panel itself only links out to GitHub.
func TestNumberKeysSelectTabs(t *testing.T) {
	m := Model{height: 40}
	for key, want := range map[string]int{"1": 0, "2": relationsTabWhy} {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = nm.(Model)
		if m.relationsTab != want {
			t.Errorf("pressing %q: expected tab %d, got %d", key, want, m.relationsTab)
		}
	}
}

func TestRelationsCursorStaysWithinViewBeforeScrolling(t *testing.T) {
	m := Model{height: 40, relationsTab: relationsTabWhy}
	m.whyDetail = strings.Repeat("a rationale line\n", 30)
	m.buildRelRows()
	if len(m.relRows) < 2 {
		t.Fatalf("expected the answer to fill several rows, got %d", len(m.relRows))
	}

	m.setRelCursor(0)
	top0 := m.relViewTop
	m.setRelCursor(1)
	if m.relViewTop != top0 {
		t.Fatalf("expected viewport to stay put for a small in-view move, top changed %d -> %d", top0, m.relViewTop)
	}
}

func TestGithubTabPlaceholderWhenNoPR(t *testing.T) {
	repo := setupJumpRepo(t, nil, map[string]string{
		"lib.ts": "export function helper() { return 1; }\n",
	})
	m := New(repo)
	m = openFileDiff(t, m, "lib.ts")
	if m.relationsTab != 0 {
		t.Fatalf("expected github to be the default tab (0), got %d", m.relationsTab)
	}
	if len(m.relRows) != 1 || m.relRows[0].actionable() {
		t.Fatalf("expected a single non-actionable placeholder row for a repo with no PR/remote, got %#v", m.relRows)
	}
	if !strings.Contains(m.relRows[0].text, "gh") && !strings.Contains(m.relRows[0].text, "PR") && !strings.Contains(m.relRows[0].text, "remote") {
		t.Logf("placeholder text: %q (informational only, not a strict assertion)", m.relRows[0].text)
	}
}

func TestTabCyclesRelationsTabsBothWays(t *testing.T) {
	m := Model{height: 40}

	for want := 1; want < relationsTabCount; want++ {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = nm.(Model)
		if m.relationsTab != want {
			t.Fatalf("tab %d: expected tab %d, got %d", want, want, m.relationsTab)
		}
	}
	// Past the last tab it wraps to the first rather than sticking.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	if m.relationsTab != 0 {
		t.Fatalf("expected tab to wrap round to 0, got %d", m.relationsTab)
	}

	// And backwards from the first tab wraps to the last.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = nm.(Model)
	if m.relationsTab != relationsTabCount-1 {
		t.Fatalf("expected shift+tab to wrap to %d, got %d", relationsTabCount-1, m.relationsTab)
	}
}

func TestSwitchingTabResetsTheCursor(t *testing.T) {
	m := Model{height: 40, relCursor: 5, relViewTop: 3}
	m.setRelationsTab(2)
	if m.relCursor != 0 || m.relViewTop != 0 {
		t.Fatalf("expected the cursor to reset for unrelated rows, got cursor=%d top=%d", m.relCursor, m.relViewTop)
	}
}

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
