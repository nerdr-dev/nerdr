package app

import (
	"nerdr/internal/review"

	tea "github.com/charmbracelet/bubbletea"
)

// explainDialog shows, whole, what the agent recorded to explain a file in
// the review panel: the file list has room only for the start of it.
// Any key closes it.
type explainDialog struct{ e review.ExplainMsg }

func (d *explainDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	m.dialog = nil
	return nil
}

func (d *explainDialog) view(m *Model, w, h int) string {
	width := max(min(w-10, 90), 20)
	var lines []string
	if d.e.Short != "" {
		if d.e.Role != "" {
			lines = append(lines, dim(d.e.Role))
		}
		lines = append(lines, wrapLines(d.e.Short, width, 8)...)
	}
	if len(d.e.Notes) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, dim("Notes on lines in it (✎ in the diff, enter there for more):"))
		for _, n := range d.e.Notes {
			lines = append(lines, wrapLines("✎ "+n, width, 4)...)
		}
	}
	lines = append(lines, "", dim("d in the file list shows the folders there instead, t the files in their folders."),
		m.keys("any key", "close"))
	return box(d.e.File, lines, w)
}
