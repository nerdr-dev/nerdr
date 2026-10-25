package git

import (
	"strings"

	"nerdr/internal/gitsafe"
	"nerdr/internal/proc"
)

// Worktree is a single git worktree belonging to a repository.
type Worktree struct {
	// Path is the absolute path of the worktree directory.
	Path string
	// Head is the commit SHA the worktree is checked out at.
	Head string
	// Branch is the branch name, empty when in detached HEAD state.
	Branch string
}

// ListWorktrees returns the worktrees of the repository at repoPath using
// `git worktree list --porcelain`.
func ListWorktrees(repoPath string) ([]Worktree, error) {
	out, err := proc.Output(gitsafe.Command(repoPath, "worktree", "list", "--porcelain"))
	if err != nil {
		return nil, err
	}
	return parseWorktrees(string(out)), nil
}

func parseWorktrees(s string) []Worktree {
	var wts []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			wts = append(wts, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD ") && cur != nil:
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch ") && cur != nil:
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return wts
}
