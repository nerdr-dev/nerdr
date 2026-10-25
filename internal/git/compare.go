package git

// Comparison is what a worktree's change is measured against, and the one
// place that decides it. The file list, each file's diff, the old version
// of a file and the counts all have to agree on it: when one of them
// compared against HEAD and another against the base branch, the screen
// showed a list of changes next to a diff of nothing.
type Comparison struct {
	Worktree string
	// Base is the branch the change is measured from — from where the
	// worktree's branch left it, so its whole work counts — or "" for the
	// uncommitted changes alone, measured from HEAD.
	Base string
}

// Files lists the changed files, new files not yet added included.
func (c Comparison) Files() ([]FileStatus, error) {
	if c.Base != "" {
		return ChangedFilesBase(c.Worktree, c.Base)
	}
	return ChangedFiles(c.Worktree)
}

// Diff is the unified diff of one file. A file not yet added has none from
// git, and is shown with every line added.
func (c Comparison) Diff(file string, untracked bool, original ...string) (string, error) {
	switch {
	case untracked:
		return NewFileContent(c.Worktree, file)
	case c.Base != "":
		return DiffBase(c.Worktree, c.Base, file, original...)
	default:
		return Diff(c.Worktree, file, original...)
	}
}

// OldRef is the commit holding the version the change started from.
func (c Comparison) OldRef() string {
	if c.Base != "" {
		return MergeBase(c.Worktree, c.Base)
	}
	return "HEAD"
}

// OldSource is file's content as the change found it.
func (c Comparison) OldSource(file string) (string, error) {
	return ReadRefFile(c.Worktree, c.OldRef(), file)
}

// OldFile is file as the change found it, formatted for the diff pane.
func (c Comparison) OldFile(file string) (string, error) {
	return OldFileContent(c.Worktree, c.OldRef(), file)
}

// NumStat counts the lines added and removed in each changed file, new
// files not yet added included; see NumStat.
func (c Comparison) NumStat() map[string]LineCount {
	against := []string{"HEAD"}
	if c.Base != "" {
		against = []string{"--merge-base", c.Base}
	}
	return NumStat(c.Worktree, against...)
}

// ShortStat counts the files changed and the lines added and removed.
func (c Comparison) ShortStat() (files, add, del int) {
	if c.Base != "" {
		return ShortStatBase(c.Worktree, c.Base)
	}
	return ShortStat(c.Worktree)
}
