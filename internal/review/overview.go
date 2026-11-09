package review

// overview.go holds the overview of every worktree in every repository.

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// ovItem is a single entry in the global worktree overview.
type ovItem struct {
	repoIdx         int
	label           string
	path            string
	counted         bool // files, add and del have arrived; see ovStatsMsg
	files, add, del int
}

// ovStat is what counting one worktree of the overview found.
type ovStat struct{ files, add, del int }

// ovStatsMsg carries the counts for the overview built as token.
type ovStatsMsg struct {
	token int
	stats []ovStat
}

func (m *Model) toggleOverview() tea.Cmd {
	if m.overview {
		m.overview = false
		return nil
	}
	m.overview, m.ovListed = true, false
	return m.buildOverview()
}

// buildOverview lists every worktree of the tab's repositories, and then
// counts their changes, in the background: a git status and a diff per
// worktree, too slow across many repositories to wait for.
func (m *Model) buildOverview() tea.Cmd {
	m.ovItems = nil
	m.ovToken++
	repos, token, src := m.repos, m.ovToken, m.src
	return func() tea.Msg {
		msg := ovListMsg{token: token}
		for ri, repo := range repos {
			wts, err := src.ListWorktrees(repo.Path)
			if err != nil {
				continue
			}
			for _, wt := range wts {
				branch := wt.Branch
				if branch == "" {
					branch = "(detached)"
				}
				msg.items = append(msg.items, ovItem{
					repoIdx: ri,
					label:   repo.Name + " / " + branch,
					path:    wt.Path,
				})
			}
		}
		return msg
	}
}

// ovListMsg carries the worktrees of the overview built as token.
type ovListMsg struct {
	token int
	items []ovItem
}

// onOverviewList shows the worktrees listed, and counts their changes.
func (m *Model) onOverviewList(msg ovListMsg) tea.Cmd {
	if msg.token != m.ovToken {
		return nil
	}
	m.ovItems = msg.items
	m.ovListed = true
	if m.ovCursor >= len(m.ovItems) {
		m.ovCursor = 0
	}
	paths := make([]string, len(m.ovItems))
	for i, it := range m.ovItems {
		paths[i] = it.path
	}
	token, src := m.ovToken, m.src
	return func() tea.Msg {
		stats := make([]ovStat, len(paths))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for i, p := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				stats[i].files, stats[i].add, stats[i].del = src.Summary(p)
			}()
		}
		wg.Wait()
		return ovStatsMsg{token: token, stats: stats}
	}
}

func (m Model) handleOverviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, m.quit()
	case "esc", "w":
		m.overview = false
		return m, nil
	case "up", "k":
		m.ovCursor = clamp(m.ovCursor-1, 0, len(m.ovItems)-1)
		return m, nil
	case "down", "j":
		m.ovCursor = clamp(m.ovCursor+1, 0, len(m.ovItems)-1)
		return m, nil
	case "enter":
		if m.ovCursor < 0 || m.ovCursor >= len(m.ovItems) {
			return m, nil
		}
		// The panel shows the tab's worktree of each repository, so this
		// is the repository's.
		it := m.ovItems[m.ovCursor]
		m.overview = false
		m.setToast(it.label)
		m.peek = nil
		m.jumpHistory = nil
		if it.repoIdx != m.repoIdx {
			m.repoIdx = it.repoIdx
			m.cursor = 0
		}
		return m, m.loadPinned()
	}
	return m, nil
}
