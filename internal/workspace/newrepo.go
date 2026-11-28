package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
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
	if _, err := gitRunEnv(gitDefaultTimeout, gitsafe.EnvNoRepo(), parent, "init", "--quiet", "--initial-branch=main", name); err != nil {
		return "", fmt.Errorf("git init: %w", err)
	}
	// The commit goes to the repository git finds from the folder: this
	// one, made just now, never one around it, even one nerdr trusts.
	if st, err := os.Stat(filepath.Join(p, ".git")); err != nil || !st.IsDir() {
		return "", fmt.Errorf("git init made no repository in %s", p)
	}
	if _, err := gitRunEnv(gitDefaultTimeout, gitsafe.EnvNoRepo(), p, "commit", "--quiet", "--allow-empty", "-m", "Initial commit"); err != nil {
		return p, fmt.Errorf("made %s, but not its first commit: %w", p, err)
	}
	return p, nil
}

// shortName matches owner/name, the short form of a repository on the
// host repositories are cloned from (config's repo_host).
var shortName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// hostPath matches host/path…, a repository on another host named
// without a URL: gitlab.com/group/name, git.example.com/a/b/c.
var hostPath = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+(:[0-9]+)?(/[A-Za-z0-9_.-]+){2,}$`)

// CloneSource reads what is typed to clone: a URL or path git clones as
// it is, owner/name on host (the config's repo_host), or host/path on
// another. It returns the folder name the clone gets, the URL to clone
// from with git, and, for owner/name and host/path, the host and path,
// for the host's CLI to clone it with its login.
func CloneSource(src, host string) (name, url, onHost, path string, err error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", "", "", "", errors.New("type owner/name or a URL to clone")
	}
	url = src
	switch {
	case strings.HasPrefix(src, "."):
	case shortName.MatchString(src):
		onHost, path = host, src
	case hostPath.MatchString(src):
		onHost, path, _ = strings.Cut(src, "/")
		onHost = strings.ToLower(onHost)
	}
	path = strings.TrimSuffix(path, ".git")
	if onHost != "" {
		url = "https://" + onHost + "/" + path + ".git"
	}
	name = strings.TrimSuffix(strings.TrimRight(src, "/"), ".git")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	if !validRepoName.MatchString(name) {
		return "", "", "", "", fmt.Errorf("found no repository name in %q", src)
	}
	return name, url, onHost, path, nil
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
	if _, err := gitRunEnv(cloneTimeout, gitsafe.EnvNoRepo(), parent, "clone", "--quiet", "--", url, name); err != nil {
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
