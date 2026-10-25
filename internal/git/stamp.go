package git

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// stampRefsMost is the most refs Stamp looks at: a repository with more
// has no stamp, and is looked at with git every time.
const stampRefsMost = 5000

// Stamp is what the git side of the worktree at worktreePath looks like
// from outside, without running git: when, and to what size, its HEAD, its
// index and the repository's refs were last written. It changes with a
// commit, a checkout, git add, a fetch or a push, but not with a file
// edited and not added. "" when it cannot be told.
func Stamp(worktreePath string) string {
	gitDir, common, ok := gitsafe.Dirs(worktreePath)
	if !ok {
		return ""
	}
	var b strings.Builder
	add := func(path string) {
		if st, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", path, st.Size(), st.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s -\n", path)
		}
	}
	for _, f := range []string{"HEAD", "index", filepath.Join("logs", "HEAD")} {
		add(filepath.Join(gitDir, f))
	}
	for _, f := range []string{"packed-refs", "FETCH_HEAD"} {
		add(filepath.Join(common, f))
	}
	refs := 0
	err := filepath.WalkDir(filepath.Join(common, "refs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if refs++; refs > stampRefsMost {
			return fs.SkipAll
		}
		add(path) // a directory too: its time changes as refs come and go
		return nil
	})
	if err != nil || refs > stampRefsMost {
		return ""
	}
	return b.String()
}
