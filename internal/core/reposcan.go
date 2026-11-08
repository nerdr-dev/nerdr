package core

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The repositories there are to work in, and the worktrees of theirs, as
// the new-tab dialog and the lists offer them. Core looks for them on the
// disk the repositories are on, which need not be the client's; the client
// shows the last look while a new one is under way.

func init() { registerRequests(ScanRepos{}) }

// RepoScanEvery is how often the repositories are looked for while no
// dialog asks.
const RepoScanEvery = time.Minute

// RepoScan is what core last found of the repositories: those to pick
// from (Repos), and why some could not be looked at (Err); where a new or
// cloned repository goes (NewRepoDir, "" when nowhere); and the folders of
// worktrees under WtRoot, grouped as a tab opens them, those open in a tab
// too (Groups). At is when; zero before the first look.
type RepoScan struct {
	Repos      []workspace.Repo
	Err        string
	NewRepoDir string
	Groups     []workspace.Group
	At         time.Time
}

// RepoScanMsg is a look at the repositories done.
type RepoScanMsg struct{ Scan RepoScan }

// ReposScannedMsg tells the client the repositories were looked at again
// (Core.Scan).
type ReposScannedMsg struct{}

// ScanRepos looks for the repositories again, for a dialog about to offer
// them.
type ScanRepos struct{}

func (ScanRepos) Do(m *Core) Cmd { return m.ScanRepos() }

// ScanRepos looks for the repositories. Looks may overlap: the one that
// looked last is kept.
func (m *Core) ScanRepos() Cmd {
	root, wtDir, dirs, wtRoot := m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs, m.WtRoot
	return func() Event {
		s := RepoScan{At: time.Now(), NewRepoDir: workspace.NewRepoDir(root, dirs)}
		repos, err := workspace.ScanAll(root, wtDir, dirs)
		if err != nil {
			s.Err = err.Error()
		}
		s.Repos = repos
		// Existing trusts the worktrees it finds: core's to do, as core
		// runs git in them.
		s.Groups = workspace.Existing(wtRoot, repos, nil)
		return RepoScanMsg{Scan: s}
	}
}

// OnRepoScan takes a look at the repositories.
func (m *Core) OnRepoScan(msg RepoScanMsg) Cmd {
	if msg.Scan.At.Before(m.Scan.At) {
		return nil // one that looked since is done already
	}
	m.Scan = msg.Scan
	return m.Tell(ReposScannedMsg{})
}

// tickScan looks for the repositories when the last look is old.
func (m *Core) tickScan(now time.Time) Cmd {
	if now.Sub(m.Scan.At) < RepoScanEvery {
		return nil
	}
	return m.ScanRepos()
}

// ClosedGroups are Scan's groups of worktrees that no tab has open.
func (m *Core) ClosedGroups() []workspace.Group {
	open := map[string]bool{}
	for _, t := range m.Tabs {
		open[t.Dir] = true
	}
	var out []workspace.Group
	for _, g := range m.Scan.Groups {
		if !open[g.Dir] {
			out = append(out, g)
		}
	}
	return out
}
