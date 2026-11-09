package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
)

// replies runs cmd, and gives m core's answers among what it brings
// (core.Reply), returning what they told it.
func replies(m *Model, cmd tea.Cmd) []core.Event {
	var out []core.Event
	for _, msg := range runBatch(cmd) {
		if r, ok := msg.(core.Reply); ok {
			m.Update(r)
			out = append(out, r.Ev)
		}
	}
	return out
}

// answer is the one answer of type T that cmd brings m, and whether there
// was one.
func answer[T any](m *Model, cmd tea.Cmd) (T, bool) {
	var zero T
	for _, ev := range replies(m, cmd) {
		if v, ok := ev.(T); ok {
			return v, true
		}
	}
	return zero, false
}

// scanned has core look for the repositories, as it does in the
// background, and m take what it found.
func scanned(m *Model) {
	if cmd := fromCore(m.Core.ScanRepos()); cmd != nil {
		m.Update(cmd())
	}
}

// settle runs cmd and gives m what comes of it, then the same of what m
// does with that, as the program would: for what core does away from its
// loop, as writing its config file, to be done. A few rounds at most.
func settle(m *Model, cmd tea.Cmd) {
	for range 4 {
		var next []tea.Cmd
		for _, msg := range runBatch(cmd) {
			if msg == nil {
				continue
			}
			_, c := m.Update(msg)
			next = append(next, c)
		}
		if cmd = tea.Batch(next...); cmd == nil {
			return
		}
	}
}
