package git

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// WorkTree is the id of the tree git would commit if everything in the
// worktree were added, new files too: the same work gives the same id,
// committed or not. The worktree's own index is left alone; a copy of it
// is used, so files git already knows are not read again.
func WorkTree(worktreePath string) (string, error) {
	out, err := proc.Output(gitsafe.Command(worktreePath, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "nerdr-index-")
	if denied.Err(err) != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	data, readErr := os.ReadFile(strings.TrimSpace(string(out)))
	_, writeErr := tmp.Write(data)
	if err := tmp.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return "", writeErr
	}
	if readErr != nil || len(data) == 0 {
		// No index yet: git makes one, but will not read an empty file.
		_ = os.Remove(name)
	}
	index := "GIT_INDEX_FILE=" + filepath.Clean(name)
	add := gitsafe.Command(worktreePath, "add", "-A")
	add.Env = append(add.Env, index)
	if err := proc.Run(add); err != nil {
		return "", err
	}
	write := gitsafe.Command(worktreePath, "write-tree")
	write.Env = append(write.Env, index)
	tree, err := proc.Output(write)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tree)), nil
}

// TreeOf is the id of the tree of commit ref, or "" when there is none.
func TreeOf(worktreePath, ref string) string {
	out, err := proc.Output(gitsafe.Command(worktreePath, "rev-parse", "--verify", "-q", ref+"^{tree}"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
