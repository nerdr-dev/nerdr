package app

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"

	tea "github.com/charmbracelet/bubbletea"
)

type fileSnapshot map[string]map[string]string
type focusVisit struct{ before, after fileSnapshot }
type focusFilesMsg struct {
	t         *tab
	visit     *focusVisit
	returning bool
	files     fileSnapshot
}

func tabPaths(t *tab) []string {
	var paths []string
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	return paths
}

// Content ids include uncommitted and untracked files and ignore commit boundaries.
// Capture off the UI thread; delayed replies belong to a specific visit.
func captureFocus(t *tab, v *focusVisit, returning bool) tea.Cmd {
	paths := tabPaths(t)
	return func() tea.Msg {
		files := fileSnapshot{}
		for _, p := range paths {
			tree, err := git.WorkTree(p)
			if err != nil {
				continue
			}
			b, err := proc.Output(gitsafe.Command(p, "ls-tree", "-rz", tree))
			if err != nil {
				continue
			}
			files[p] = map[string]string{}
			for _, entry := range strings.Split(string(b), "\x00") {
				meta, name, ok := strings.Cut(entry, "\t")
				if ok {
					files[p][name] = meta
				}
			}
		}
		return focusFilesMsg{t, v, returning, files}
	}
}

func (m *Model) focusFiles(msg focusFilesMsg) {
	t := msg.t
	if m.tabIndex(t) < 0 || t.focusVisit != msg.visit {
		return
	}
	v := msg.visit
	if msg.returning {
		v.after = msg.files
	} else {
		v.before = msg.files
	}
	if v.before == nil || v.after == nil {
		return
	}
	t.unseen = map[string]map[string]bool{}
	for p, before := range v.before {
		after, ok := v.after[p]
		if !ok {
			continue
		}
		t.unseen[p] = map[string]bool{}
		for f, stamp := range after {
			if before[f] != stamp {
				t.unseen[p][f] = true
			}
		}
		for f := range before {
			if _, ok := after[f]; !ok {
				t.unseen[p][f] = true
			}
		}
	}
	if t == m.cur() {
		m.review = m.review.SetUnseen(t.unseen)
	}
}
