package core

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

type FocusFilesMsg struct {
	T         *Tab
	Visit     *FocusVisit
	Returning bool
	Files     FileSnapshot
}

func TabPaths(t *Tab) []string {
	var paths []string
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	return paths
}

// Content ids include uncommitted and untracked files and ignore commit boundaries.
// Capture off the UI thread; delayed replies belong to a specific visit.
func CaptureFocus(t *Tab, v *FocusVisit, returning bool) Cmd {
	paths := TabPaths(t)
	return func() Event {
		files := FileSnapshot{}
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
		return FocusFilesMsg{t, v, returning, files}
	}
}

type FileSnapshot map[string]map[string]string

type FocusVisit struct{ Before, After FileSnapshot }
