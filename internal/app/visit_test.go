package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestMenusRemainReachableInSmallTerminal(t *testing.T) {
	m := testModel(t)
	m.height, m.width = 18, 60
	// A menu longer than the screen: every command, as a long list.
	var items []menuItem
	for _, g := range core.AllCommands() {
		items = append(items, menuItem{label: g.Title})
		for _, e := range g.Entries {
			items = append(items, menuItem{label: e.Label, key: e.Key, run: func(*Model) tea.Cmd { return nil }})
		}
	}
	mu := &menu{items: items, hi: 1}
	m.dialog = mu
	for range len(mu.items) {
		mu.key(m, keyMsg("down"))
	}
	view := ansi.Strip(mu.view(m, 60, 14))
	if !strings.Contains(view, "Quit") || len(strings.Split(view, "\n")) > 14 {
		t.Fatal(view)
	}
	m.dialog = nil
	m.startPrefix()
	seen := ""
	for range 30 {
		box := m.prefixHint(60, 14)
		if lipgloss.Height(box) > 14 || lipgloss.Width(box) > 60 {
			t.Fatalf("prefix help overflows screen:\n%s", box)
		}
		seen += ansi.Strip(box)
		m.handleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	for _, g := range core.CommandGroups() {
		for _, e := range g.Entries {
			if !strings.Contains(seen, e.Label) {
				t.Fatalf("key inaccessible: %s", e.Label)
			}
		}
	}
	m.handleKey(keyMsg("?"))
	if m.prefix {
		t.Fatal("normal command failed after paging")
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
