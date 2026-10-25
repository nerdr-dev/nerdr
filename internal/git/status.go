package git

import (
	"strings"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// FileStatus describes a single changed file in a worktree.
type FileStatus struct {
	// Path is the file path relative to the worktree root.
	Path string
	// OldPath is the original name of a renamed or copied file.
	OldPath string
	// Code is the two-character porcelain status code (e.g. "M ", " M", "??", "A ").
	Code string
}

// ChangedFiles returns the changed files in the worktree at worktreePath.
//
// -uall matters: by default git collapses a wholly untracked directory into
// a single entry ending in "/", which is not a file and so cannot be shown
// as one. Listing untracked files individually keeps every entry openable.
// Ignored files are still excluded, so this doesn't descend into the likes
// of node_modules.
func ChangedFiles(worktreePath string) ([]FileStatus, error) {
	// -z gives paths as they are: without it git quotes any name with a
	// space or a non-ASCII letter, and the quoted name matches no file.
	cmd := gitsafe.Command(worktreePath, "status", "--porcelain", "-uall", "-z")
	// A status would otherwise refresh and rewrite the index, which the
	// watch on the git directory hears as a change: a refresh setting off
	// the next one, for as long as nerdr runs.
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	out, err := proc.Output(cmd)
	if err != nil {
		return nil, err
	}
	return parseStatus(string(out)), nil
}

// parseStatus reads `git status --porcelain -z`: "XY PATH" entries ended
// by NUL, where a rename or copy is followed by one more entry holding the
// path it came from.
func parseStatus(s string) []FileStatus {
	fields := strings.Split(s, "\x00")
	files := make([]FileStatus, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		if len(line) < 4 {
			continue
		}
		code := line[:2]
		files = append(files, FileStatus{Code: code, Path: line[3:]})
		if code[0] == 'R' || code[0] == 'C' || code[1] == 'R' || code[1] == 'C' {
			if i+1 < len(fields) {
				files[len(files)-1].OldPath = fields[i+1]
			}
			i++ // skip the original path
		}
	}
	return files
}

// Diff returns the diff of a single file against HEAD (combining staged and
// unstaged changes).
func Diff(worktreePath, file string, original ...string) (string, error) {
	args := append([]string{"diff", "--find-renames", "HEAD", "--"}, diffPaths(worktreePath, "", file, original)...)
	out, err := proc.Output(gitsafe.Command(worktreePath, args...))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Both sides of a rename must be in the pathspec, otherwise Git sees an
// addition and cannot match it with the old file. Callers with the status
// already loaded can pass its original path to avoid another status call.
func diffPaths(worktree, base, file string, original []string) []string {
	if len(original) == 0 {
		files, _ := (Comparison{Worktree: worktree, Base: base}).Files()
		for _, f := range files {
			if f.Path == file && f.OldPath != "" {
				original = []string{f.OldPath}
				break
			}
		}
	}
	// Each as the name it is: a file called *.txt, or starting with a
	// colon, is not a pattern for git to match others with.
	paths := []string{":(literal)" + file}
	for _, old := range original {
		if old != "" && old != file {
			paths = append(paths, ":(literal)"+old)
		}
	}
	return paths
}
