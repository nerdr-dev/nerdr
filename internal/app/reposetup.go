package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A repository's .nerdr.toml says what to copy into a new worktree and
// what to run there (internal/workspace). An agent can write it, so it is
// shown to the user before a worktree is made with it, the first time and
// each time it has changed, and done only once they allow it.

// askRepoSetup asks core's question q, whether the .nerdr.toml of each of
// its repositories may be done. The dialog open, a wizard or the spinoff
// one, is put back for what core goes on with.
//
// What is asked about is shown exactly, scrolled if long, and yes is taken
// only once its end has been shown. An agent may have written it, and a
// TOML string can hold any character: control and invisible format
// characters are drawn as their escapes, marked, so that none reaches the
// terminal to hide or rewrite what is shown, and so that the user sees
// they are in what runs.
func (m *Model) askRepoSetup(q core.Question) {
	names, ask := q.Repos, q.Setup
	hidden := 0
	shown := func(s string) string {
		t, n := clean.Marked(s, warn)
		hidden += n
		return t
	}
	bold := lipgloss.NewStyle().Bold(true)
	var scroll []string
	for _, r := range names {
		c := ask[r]
		scroll = append(scroll, bold.Render(shown(filepath.Join(r, workspace.RepoConfigFile))))
		if len(c.Copy) > 0 {
			paths := make([]string, len(c.Copy))
			for i, p := range c.Copy {
				paths[i] = shown(p)
			}
			scroll = append(scroll, "  copy:  "+strings.Join(paths, ", "))
		}
		// Trimmed as workspace trims it before it runs it.
		if s := strings.TrimSpace(c.Setup); s != "" {
			for i, l := range strings.Split(s, "\n") {
				head := "  setup: "
				if i > 0 {
					head = "         "
				}
				scroll = append(scroll, head+shown(l))
			}
		}
		scroll = append(scroll, "")
	}
	lines := []string{
		"The repository asks, in " + workspace.RepoConfigFile + ", for this in the new worktree.",
		"It runs as you, outside any agent's sandbox, and an agent may",
		"have written it: allow it only if you know what it does.",
	}
	if hidden > 0 {
		lines = append(lines, warn(fmt.Sprintf("It holds %d character%s that would not show, drawn here as escapes:", hidden, pluralS(hidden))),
			warn("what runs has them as they are."))
	}
	lines = append(lines, "")
	back, mc := m.dialog, m.tellingOn()
	m.dialog = &confirm{
		title:  "Allow " + workspace.RepoConfigFile + "?",
		lines:  lines,
		scroll: scroll,
		after:  []string{keyHint("y") + dim(" allows it until it changes · ") + keyHint("n") + dim(" makes the worktree without it")},
		toEnd:  true,
		yes: func(m *Model) tea.Cmd {
			m.dialog = back
			return m.doOn(mc, core.Answer{ID: q.ID, Answer: core.AnswerAllowed})
		},
		no: func(m *Model) tea.Cmd {
			m.dialog = back
			return m.doOn(mc, core.Answer{ID: q.ID, Answer: core.RunDeclined})
		},
	}
}

// askSetup goes on with then once the user has said whether the .nerdr.toml
// of each of repos may be done, asking first, through core, about those not
// yet allowed as they are now.
func (m *Model) askSetup(repos []string, then func(m *Model) tea.Cmd) tea.Cmd {
	m.lastSetup++
	if m.afterSetup == nil {
		m.afterSetup = map[int]func(m *Model) tea.Cmd{}
	}
	m.afterSetup[m.lastSetup] = then
	return m.do(core.AskSetup{Token: m.lastSetup, Repos: repos})
}

// setupAnswered goes on with what waited for the question of askSetup
// token.
func (m *Model) setupAnswered(token int) tea.Cmd {
	then := m.afterSetup[token]
	delete(m.afterSetup, token)
	if then == nil {
		return nil
	}
	return then(m)
}
