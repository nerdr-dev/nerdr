package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolveCommit is the full id of the commit rev names in the repository
// at dir, or an error saying it names none.
func ResolveCommit(dir, rev string) (string, error) {
	out, err := git(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("%s is not a commit here", rev)
	}
	return strings.TrimSpace(out), nil
}

// Pick brings commits onto w's branch with git cherry-pick, oldest first,
// and returns how many it took. Commits w's repository does not have are
// left out: in a tab with several repositories, they are another one's.
// It is all or nothing: when one does not apply cleanly the pick is
// undone, the branch is left as it was, and the error says so.
func Pick(w Worktree, commits []string) (int, error) {
	var have []string
	wanted := map[string]bool{}
	for _, c := range commits {
		if id, err := ResolveCommit(w.Path, c); err == nil && !wanted[id] {
			have = append(have, id)
			wanted[id] = true
		}
	}
	if len(have) == 0 {
		return 0, nil
	}
	// Each after the ones it builds on, whatever order they were listed
	// in: a commit picked before its parent would not apply. By ancestry,
	// not by time: a rebase gives a branch's commits all the same second.
	// What the branch has already is neither walked nor picked again.
	out, err := git(w.Path, append(append([]string{"rev-list", "--topo-order", "--reverse"}, have...), "--not", "HEAD")...)
	if err != nil {
		return 0, err
	}
	var ordered []string
	for _, id := range strings.Fields(out) {
		if wanted[id] {
			ordered = append(ordered, id)
		}
	}
	if len(ordered) == 0 {
		return 0, nil
	}
	if _, err := gitHooks(w.Path, append([]string{"cherry-pick"}, ordered...)...); err != nil {
		_, _ = git(w.Path, "cherry-pick", "--abort")
		return 0, fmt.Errorf("%s: %w", filepath.Base(w.Repo), err)
	}
	return len(ordered), nil
}
