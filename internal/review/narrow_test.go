package review

import (
	"fmt"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestViewInATinyTerminal: the panel draws in a terminal too narrow or
// low for its frames, down to none at all, without a panic (a negative
// strings.Repeat, a slice past its end).
func TestViewInATinyTerminal(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, map[string]string{
		"a.go": "package a\n\nfunc A() {}\n",
		"b.go": "package b\n",
	})
	repo := filepath.Join(parent, "repo")
	m := NewEmbedded(Local{})
	m = pinNow(t, m, []Pin{{Repo: repo, Path: repo}})
	m = m.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	m.refreshFiles()
	for w := range 8 {
		for _, h := range []int{0, 1, 2, 3, 5, 8, 40} {
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				m := m.send(tea.WindowSizeMsg{Width: w, Height: h})
				_ = m.View()
			})
		}
	}
}
