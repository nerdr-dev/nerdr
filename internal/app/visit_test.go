package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMenusRemainReachableInSmallTerminal(t *testing.T) {
	m := testModel(t)
	m.height, m.width = 18, 60
	m.openMenu(0, 0)
	mu := m.dialog.(*menu)
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
		lines := m.prefixHint(60)
		if len(lines) > m.height-m.barRows() {
			t.Fatal("prefix help overflows screen")
		}
		seen += ansi.Strip(strings.Join(lines, "\n"))
		m.handleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	for _, g := range core.CommandGroups() {
		if !strings.Contains(seen, g.Title) {
			t.Fatalf("group inaccessible: %s", g.Title)
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
