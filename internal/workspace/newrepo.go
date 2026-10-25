package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// cloneTimeout bounds a clone: a big repository over a slow line takes a
// while, a remote that never answers must not hold the dialog for ever.
const cloneTimeout = 15 * time.Minute

// NewRepoDir is the folder a new or cloned repository goes in, so that the
// next scan finds it: root, or, when nerdr was started inside a
// repository, the first of the folders the config lists to look in. ""
// when there is no such folder.
func NewRepoDir(root string, dirs []string) string {
	if !isPrimary(root) {
		return root
	}
	if len(dirs) > 0 {
		if d := absUnder(root, dirs[0]); !isPrimary(d) {
			return d
		}
	}
	return ""
}

// validRepoName is a name a new repository's folder can have, here and on
// GitHub.
var validRepoName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// newRepoPath is where a repository named name goes in parent, or an error
// saying why it cannot.
func newRepoPath(parent, name string) (string, error) {
	if !validRepoName.MatchString(name) {
		return "", fmt.Errorf("%q is not a name for a repository: use letters, digits, -, _ and .", name)
	}
	p := filepath.Join(parent, name)
	if _, err := os.Stat(p); err == nil {
		return "", fmt.Errorf("%s already exists", p)
	}
	return p, nil
}

// InitRepo makes a repository named name in parent, on a main branch with
// an empty first commit, so a worktree has a base to branch from, and
// returns its path.
func InitRepo(parent, name string) (string, error) {
	p, err := newRepoPath(parent, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	if _, err := git(parent, "init", "--quiet", "--initial-branch=main", name); err != nil {
		return "", fmt.Errorf("git init: %w", err)
	}
	if _, err := git(p, "commit", "--quiet", "--allow-empty", "-m", "Initial commit"); err != nil {
		return p, fmt.Errorf("made %s, but not its first commit: %w", p, err)
	}
	return p, nil
}

// ghRepoName matches owner/name, the short form of a GitHub repository.
var ghRepoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// CloneSource reads what is typed to clone: a URL or path git clones as
// it is, or owner/name on GitHub. It returns the folder name the clone
// gets, and, for owner/name, the URL to clone from when gh is not there to
// do it.
func CloneSource(src string) (name, url string, github bool, err error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", "", false, errors.New("type owner/name or a URL to clone")
	}
	github = ghRepoName.MatchString(src) && !strings.HasPrefix(src, ".")
	url = src
	if github {
		url = "https://github.com/" + src + ".git"
	}
	name = strings.TrimSuffix(strings.TrimRight(src, "/"), ".git")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	if !validRepoName.MatchString(name) {
		return "", "", false, fmt.Errorf("found no repository name in %q", src)
	}
	return name, url, github, nil
}

// CloneRepo clones url into a folder named name in parent with git, and
// returns its path.
func CloneRepo(parent, name, url string) (string, error) {
	p, err := newRepoPath(parent, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	if _, err := gitTimeout(cloneTimeout, parent, "clone", "--quiet", "--", url, name); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return p, nil
}

// CheckNewRepo says whether a repository named name can be made in
// parent, before anything is done about it.
func CheckNewRepo(parent, name string) error {
	_, err := newRepoPath(parent, name)
	return err
}
