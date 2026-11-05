package core

import "path/filepath"

// TabFor is the tab one of whose worktrees is dir, or nil.
func (m *Core) TabFor(dir string) *Tab {
	dir = filepath.Clean(dir)
	for _, t := range m.Tabs {
		if filepath.Clean(t.Dir) == dir {
			return t
		}
		for _, w := range t.Worktrees {
			if filepath.Clean(w.Path) == dir {
				return t
			}
		}
	}
	return nil
}
