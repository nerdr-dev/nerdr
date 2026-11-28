package core

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/forges"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The new-tab dialog can make a repository to work on, or clone one, into
// the folder core looks for repositories in (RepoScan.NewRepoDir).

func init() { registerRequests(MakeRepo{}) }

// MakeRepo makes the repository called Name, on the config's repo_host
// too (GitHub unless it says) with OnGitHub;
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
	dir, host := m.Scan.NewRepoDir, m.Cfg.NewRepoHost()
	scan := m.ScanRepos()
	return func() Event {
		made := RepoMadeMsg{Job: q.Job}
		path, warning, err := makeRepo(q, dir, host)
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

// makeRepo makes or clones the repository q says, in dir: a new one on
// host too, and owner/name cloned from host, with host's CLI when it is
// there.
func makeRepo(q MakeRepo, dir, host string) (path, warning string, err error) {
	if dir == "" {
		return "", "", errors.New("there is no folder to make a repository in")
	}
	value := strings.TrimSpace(q.Name)
	if q.Clone {
		name, url, on, repo, err := workspace.CloneSource(value, host)
		if err == nil {
			err = workspace.CheckNewRepo(dir, name)
		}
		if err != nil {
			return "", "", err
		}
		if on != "" && forges.CanMake(on) {
			path, err = forges.CloneRepo(dir, on, repo, name)
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
	if q.OnGitHub {
		service := forge.KindOf(host).Name()
		if !forges.CanMake(host) {
			return path, "made " + filepath.Base(path) + " here, but not on " + service + ": " +
				forge.KindOf(host).CLI() + " is not installed", nil
		}
		if _, err := forges.CreateRepo(path, host); err != nil {
			return path, "made " + filepath.Base(path) + " here, but not on " + service + ": " + err.Error(), nil
		}
	}
	return path, "", nil
}
