package app

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A repository's .nerdr.toml says what to copy into a new worktree and
// what to run there (internal/workspace). An agent can write it, so it is
// shown to the user before a worktree is made with it, the first time and
// each time it has changed, and done only once they allow it.

// setupAllowedFile is where the .nerdr.toml files the user allowed are
// kept, beside the sessions.
func setupAllowedFile() string {
	return filepath.Join(filepath.Dir(state.Dir()), "setup-allowed.json")
}

// askRepoSetup goes on with then once the user has said whether the
// .nerdr.toml of each of repos may be done, asking first about those not
// yet allowed as they are now. The dialog open, a wizard or the spinoff
// one, is put back for then.
func (m *Model) askRepoSetup(repos []string, then func(m *Model) tea.Cmd) tea.Cmd {
	file := setupAllowedFile()
	ask := workspace.ToAllow(file, repos)
	if len(ask) == 0 {
		return then(m)
	}
	var names []string
	for r := range ask {
		names = append(names, r)
	}
	sort.Strings(names)
	lines := []string{
		"The repository asks, in " + workspace.RepoConfigFile + ", for this in the new worktree.",
		"It runs as you, outside any agent's sandbox, and an agent may",
		"have written it: allow it only if you know what it does.", "",
	}
	for _, r := range names {
		c := ask[r]
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render(filepath.Join(r, workspace.RepoConfigFile)))
		if len(c.Copy) > 0 {
			lines = append(lines, "  copy:  "+strings.Join(c.Copy, ", "))
		}
		if s := strings.TrimSpace(c.Setup); s != "" {
			for i, l := range strings.Split(s, "\n") {
				head := "  setup: "
				if i > 0 {
					head = "         "
				}
				lines = append(lines, head+l)
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, dim("y allows it until it changes · n makes the worktree without it"))
	back := m.dialog
	m.dialog = &confirm{
		title: "Allow " + workspace.RepoConfigFile + "?",
		lines: lines,
		yes: func(m *Model) tea.Cmd {
			for _, r := range names {
				if err := workspace.Allow(file, r, ask[r]); err != nil {
					m.toast("could not keep that it is allowed: " + err.Error())
				}
			}
			m.dialog = back
			return then(m)
		},
		no: func(m *Model) tea.Cmd {
			m.dialog = back
			return then(m)
		},
	}
	return nil
}
