package git

import (
	"strings"

	"nerdr/internal/gitsafe"
	"nerdr/internal/proc"
)

// DetectBase returns the branch the worktree's work is compared with: the
// branch it was stacked on (see StackedBase), else the primary base branch
// — origin's default (origin/HEAD), main or master, local or origin's —
// if it exists in the repository.
func DetectBase(worktreePath string) string {
	if b := StackedBase(worktreePath); b != "" {
		return b
	}
	refs := []string{"main", "master", "origin/main", "origin/master"}
	if d := DefaultBranch(worktreePath); d != "" {
		refs = append([]string{d, "origin/" + d}, refs...)
	}
	for _, ref := range refs {
		if err := proc.Run(gitsafe.Command(worktreePath, "rev-parse", "--verify", "-q", ref)); err == nil {
			return ref
		}
	}
	return ""
}

// DefaultBranch is the name of origin's default branch, as origin/HEAD
// says (set by clone, or `git remote set-head origin -a`): develop or
// trunk in repositories that do not use main. "" when it is not set.
func DefaultBranch(dir string) string {
	out, err := proc.Output(gitsafe.Command(dir, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"))
	if err != nil {
		return ""
	}
	b, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "origin/")
	if !ok || b == "HEAD" {
		return ""
	}
	return b
}

// stackedKey is the git config, per branch, that names the branch it was
// started on top of.
func stackedKey(branch string) string { return "branch." + branch + ".nerdrBase" }

// StackedBase is the local branch the worktree's branch was started on
// top of, when it was made from another tab's branch rather than from
// main, as long as that branch is still there: once it is merged and
// deleted, the work is compared with main again.
func StackedBase(worktreePath string) string {
	b := CurrentBranch(worktreePath)
	if b == "" {
		return ""
	}
	out, err := proc.Output(gitsafe.Command(worktreePath, "config", "--get", stackedKey(b)))
	base := strings.TrimSpace(string(out))
	if err != nil || base == "" || base == b {
		return ""
	}
	if proc.Run(gitsafe.Command(worktreePath, "rev-parse", "--verify", "-q", "refs/heads/"+base)) != nil {
		return ""
	}
	return base
}

// SetStackedBase records that branch was started on top of base.
func SetStackedBase(repo, branch, base string) error {
	return proc.Run(gitsafe.Command(repo, "config", stackedKey(branch), base))
}

// CurrentBranch is the branch checked out in the worktree, or "" when HEAD
// is detached or git cannot tell.
func CurrentBranch(worktreePath string) string {
	out, err := proc.Output(gitsafe.Command(worktreePath, "symbolic-ref", "--short", "-q", "HEAD"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// A branch is compared with where it left base (their merge base), not with
// base as it is now: once base has moved on, its newer commits would
// otherwise show up here as this branch's change, reversed.

// MergeBase is the commit the worktree's branch forked from base at, or
// base itself when git cannot tell.
func MergeBase(worktreePath, base string) string {
	out, err := proc.Output(gitsafe.Command(worktreePath, "merge-base", base, "HEAD"))
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return base
	}
	return strings.TrimSpace(string(out))
}

// ChangedFilesBase returns the files that differ between the merge base
// with base and the current worktree: committed work, uncommitted changes
// and new files not yet added.
func ChangedFilesBase(worktreePath, base string) ([]FileStatus, error) {
	out, err := proc.Output(gitsafe.Command(worktreePath, "diff", "--find-renames", "--merge-base", base, "--name-status", "-z"))
	if err != nil {
		return nil, err
	}
	files := parseNameStatus(string(out))
	untracked, err := proc.Output(gitsafe.Command(worktreePath, "ls-files", "--others", "--exclude-standard", "-z"))
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p != "" {
			files = append(files, FileStatus{Code: "??", Path: p})
		}
	}
	return files, nil
}

// DiffBase returns the diff of a single file against the merge base with
// base.
func DiffBase(worktreePath, base, file string, original ...string) (string, error) {
	args := append([]string{"diff", "--find-renames", "--merge-base", base, "--"}, diffPaths(worktreePath, base, file, original)...)
	out, err := proc.Output(gitsafe.Command(worktreePath, args...))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseNameStatus reads `git diff --name-status -z`: a status field, then
// the path, each ended by NUL; a rename or copy has two paths, of which the
// new one is kept.
func parseNameStatus(s string) []FileStatus {
	var out []FileStatus
	fields := strings.Split(s, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		oldPath := ""
		if status == "" {
			continue
		}
		if status[0] == 'R' || status[0] == 'C' {
			if i+2 >= len(fields) {
				break
			}
			oldPath, path = path, fields[i+2]
			i++
		}
		out = append(out, FileStatus{Code: nameStatusCode(status), Path: path, OldPath: oldPath})
	}
	return out
}

func nameStatusCode(status string) string {
	switch status[0] {
	case 'A':
		return "A "
	case 'D':
		return "D "
	case 'R':
		return "R "
	default:
		return " M"
	}
}
