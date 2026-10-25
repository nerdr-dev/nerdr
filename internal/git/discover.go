package git

import (
	"os"
	"path/filepath"
	"sort"
)

// Repo is a git repository located under the startup directory.
type Repo struct {
	// Name is the directory name of the repository.
	Name string
	// Path is the absolute path to the repository root.
	Path string
}

// DiscoverRepos scans dir for immediate subdirectories that are git
// repositories and returns them sorted by name.
func DiscoverRepos(dir string) ([]Repo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var repos []Repo
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".git" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if isRepo(path) {
			repos = append(repos, Repo{Name: e.Name(), Path: path})
		}
	}

	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// isRepo reports whether path is a primary git repository. A primary
// repository has a .git directory, whereas a linked worktree has a .git file
// pointing back to its main repository and is surfaced via ListWorktrees
// instead of being listed as a repository of its own.
func isRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir()
}
