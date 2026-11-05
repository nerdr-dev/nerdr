package core

import (
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// searchLimit is the most conversations listed.
const SearchLimit = 200

// searchRoots are the folders whose conversations are searched.
func (m *Core) SearchRoots() []string {
	roots := []string{m.Root, m.WtRoot}
	for _, t := range m.Tabs {
		roots = append(roots, t.Dir)
		for _, w := range t.Worktrees {
			roots = append(roots, w.Path)
		}
	}
	return roots
}

// resumeHint is the command that carries on c in a terminal.
func ResumeHint(c *agents.Conversation) string {
	if c.Kind == agents.Codex {
		return "codex resume " + c.ID
	}
	return "claude --resume " + c.ID
}

// shortDir is dir from the folder nerdr started in, when under it.
func (m *Core) ShortDir(dir string) string {
	if rel, err := filepath.Rel(m.Root, dir); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return filepath.Base(dir)
		}
		return rel
	}
	return dir
}
