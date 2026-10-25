package review

// overview.go holds the overview of every worktree in every repository.

import (
	"sync"

	"nerdr/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// ovItem is a single entry in the global worktree overview.
type ovItem struct {
	repoIdx, wtIdx  int
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
	m.overview = true
	return m.buildOverview()
}

// buildOverview lists every worktree at once and returns the command that
// counts their changes: that is a git status and a diff per worktree, too
// slow across many repositories to do before the list can be shown.
func (m *Model) buildOverview() tea.Cmd {
	m.ovItems = nil
	m.ovToken++
	for ri, repo := range m.repos {
		wts, err := git.ListWorktrees(repo.Path)
		if err != nil {
			continue
		}
		for wi, wt := range wts {
			branch := wt.Branch
			if branch == "" {
				branch = "(detached)"
			}
			m.ovItems = append(m.ovItems, ovItem{
				repoIdx: ri, wtIdx: wi,
				label: repo.Name + " / " + branch,
				path:  wt.Path,
			})
		}
	}
	if m.ovCursor >= len(m.ovItems) {
		m.ovCursor = 0
	}
	paths := make([]string, len(m.ovItems))
	for i, it := range m.ovItems {
		paths[i] = it.path
	}
	token := m.ovToken
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
				if fs, err := git.ChangedFiles(p); err == nil {
					stats[i].files = len(fs)
				}
				_, stats[i].add, stats[i].del = git.ShortStat(p)
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
		if m.ovCursor >= 0 && m.ovCursor < len(m.ovItems) {
			it := m.ovItems[m.ovCursor]
			m.peek = nil
			m.jumpHistory = nil
			m.repoIdx = it.repoIdx
			m.loadWorktrees()
			m.wtIdx = clamp(it.wtIdx, 0, len(m.worktrees)-1)
			m.loadFiles()
			m.loadDiff()
			m.overview = false
			m.setToast(it.label)
		}
		return m, nil
	}
	return m, nil
}
