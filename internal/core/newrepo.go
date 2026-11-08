package core

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The new-tab dialog can make a repository to work on, or clone one, into
// the folder core looks for repositories in (RepoScan.NewRepoDir).

func init() { registerRequests(MakeRepo{}) }

// MakeRepo makes the repository called Name, on GitHub too with OnGitHub;
// or with Clone, clones the one Name says: owner/name, or a URL or path git
// can clone. It is told as a RepoMadeMsg of Job, once the repositories
// have been looked for again.
type MakeRepo struct {
	Job      string
	Name     string
	Clone    bool
	OnGitHub bool
}

// RepoMadeMsg is what became of MakeRepo of Job. Path is set when there is
// a repository to pick, Warning when something after that went wrong (its
// first commit, its copy on GitHub), Err when it failed.
type RepoMadeMsg struct {
	Job                string
	Path, Warning, Err string
}

// repoMadeMsg is a repository made, and the look at the repositories
// after.
type repoMadeMsg struct {
	Made RepoMadeMsg
	Scan RepoScanMsg
}

func (q MakeRepo) Do(m *Core) Cmd {
	dir, gh := m.Scan.NewRepoDir, !m.NoGh
	scan := m.ScanRepos()
	return func() Event {
		made := RepoMadeMsg{Job: q.Job}
		path, warning, err := makeRepo(q, dir, gh)
		made.Path, made.Warning = path, warning
		if err != nil {
			made.Err = err.Error()
		}
		msg := repoMadeMsg{Made: made}
		msg.Scan, _ = scan().(RepoScanMsg)
		return msg
	}
}

// onRepoMade takes a repository made: it is among the repositories, and
// the client is told.
func (m *Core) onRepoMade(msg repoMadeMsg) Cmd {
	return Batch(m.OnRepoScan(msg.Scan), m.Tell(msg.Made))
}

// makeRepo makes or clones the repository q says, in dir; on GitHub only
// with gh.
func makeRepo(q MakeRepo, dir string, gh bool) (path, warning string, err error) {
	if dir == "" {
		return "", "", errors.New("there is no folder to make a repository in")
	}
	value := strings.TrimSpace(q.Name)
	if q.Clone {
		name, url, onGitHub, err := workspace.CloneSource(value)
		if err == nil {
			err = workspace.CheckNewRepo(dir, name)
		}
		if err != nil {
			return "", "", err
		}
		if onGitHub && gh {
			path, err = github.CloneRepo(dir, value, name)
		} else {
			path, err = workspace.CloneRepo(dir, name, url)
		}
		return path, "", err
	}
	if err := workspace.CheckNewRepo(dir, value); err != nil {
		return "", "", err
	}
	path, err = workspace.InitRepo(dir, value)
	if err != nil {
		// A repository without its first commit is still one to pick; a
		// worktree of it says what is missing.
		return path, "", err
	}
	if q.OnGitHub && gh {
		if _, err := github.CreateRepo(path); err != nil {
			return path, "made " + filepath.Base(path) + " here, but not on GitHub: " + err.Error(), nil
		}
	}
	return path, "", nil
}
