package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/move"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/statedir"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Making a part's tab on another machine than that of the tab it came
// from. The cores do not know each other: as for a move (see move.go),
// the client asks each its part, and carries the commits over.
//
//  1. The target says where it has the tab's repositories, found by their
//     origin, or where it would clone one it lacks (SpinoffFind).
//  2. When the branch is to start with the part's commits, the source
//     bundles them (SpinoffPack), and the client carries the pack over a
//     chunk at a time (MoveChunk, MovePut).
//  3. The target clones what it lacks, makes the branch from origin's
//     main, brings the commits in and makes the tab (SpinoffLand), and
//     says what became of the part (SpinoffLandedMsg).
//  4. The client has the source record that (SpinoffNext), for the agent
//     that sent the part, and drop the pack (SpinoffPackDrop).
//
// Nothing is pushed: the commits go in the pack, and what they build on
// the target fetches from origin's main, as any new branch does.

func init() {
	registerRequests(SpinoffFind{}, SpinoffPack{}, SpinoffPackDrop{}, SpinoffLand{})
	registerEvents(SpinoffFoundMsg{}, SpinoffPackedMsg{}, SpinoffLandedMsg{})
}

// SpinoffFind asks the target where it has Repos: told as a
// SpinoffFoundMsg. Their branches are not looked at.
type SpinoffFind struct {
	Job   string
	Repos []MoveRepo
}

// SpinoffFoundMsg answers SpinoffFind of Job: a MoveTarget for each of
// its Repos, with Path, or CloneInto, or Err set.
type SpinoffFoundMsg struct {
	Job   string
	Repos []MoveTarget
}

func (q SpinoffFind) Do(m *Core) Cmd {
	paths, cloneDir := m.repoPaths(), m.Scan.NewRepoDir
	return Later(func() Event {
		msg := SpinoffFoundMsg{Job: q.Job}
		for _, r := range q.Repos {
			msg.Repos = append(msg.Repos, findRepo(r, paths, cloneDir, false))
		}
		return msg
	})
}

// SpinoffPack has the source bundle Commits, those of tab T's part, in
// the worktrees of T in Repos (their repositories' paths): told as a
// SpinoffPackedMsg. The pack is kept for MoveChunk until SpinoffPackDrop.
type SpinoffPack struct {
	Job     string
	T       TabRef
	Repos   []string
	Commits []string
}

// SpinoffPackedMsg is the pack of SpinoffPack Job made: Size bytes, whose
// SHA-256 is Sum. Err is why it was not.
type SpinoffPackedMsg struct {
	Job  string
	Size int64
	Sum  string
	Err  string
}

// spinoffManifest is what a pack of a part's commits holds, besides the
// bundles: a spinoffBundle for each repository with some of them.
type spinoffManifest struct {
	Repos []spinoffBundle
}

// spinoffBundle is the commits of one repository, by its Origin: their
// full ids, and the file in the pack that has them.
type spinoffBundle struct {
	Origin  string
	Bundle  string
	Commits []string
}

func (q SpinoffPack) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	switch {
	case t == nil:
		return m.Tell(SpinoffPackedMsg{Job: q.Job, Err: "the tab is gone"})
	case !validJob.MatchString(q.Job) || m.packsOut[q.Job] != "":
		return m.Tell(SpinoffPackedMsg{Job: q.Job, Err: "no spinoff " + q.Job})
	}
	var wts []workspace.Worktree
	for _, w := range t.Worktrees {
		if slices.Contains(q.Repos, filepath.Clean(w.Repo)) {
			wts = append(wts, w)
		}
	}
	dir, err := statedir.MkdirTemp("nerdr-move-out-")
	if err != nil {
		return m.Tell(SpinoffPackedMsg{Job: q.Job, Err: err.Error()})
	}
	if m.packsOut == nil {
		m.packsOut = map[string]string{}
	}
	m.packsOut[q.Job] = dir
	return Later(func() Event {
		size, sum, err := packCommits(dir, wts, q.Commits)
		msg := SpinoffPackedMsg{Job: q.Job, Size: size, Sum: sum}
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

// packCommits packs commits, as they are in wts, into dir/pack, and gives
// its size and SHA-256.
func packCommits(dir string, wts []workspace.Worktree, commits []string) (int64, string, error) {
	var man spinoffManifest
	files := [][2]string{{manifestFile, filepath.Join(dir, manifestFile)}}
	for i, w := range wts {
		full := move.FullCommits(w.Path, commits)
		if len(full) == 0 {
			continue
		}
		if move.OriginKey(w.Origin) == "" {
			return 0, "", fmt.Errorf("%s has no origin, so nerdr cannot tell which repository it is there", filepath.Base(w.Repo))
		}
		name := fmt.Sprintf("repo-%d.bundle", i)
		if err := move.BundleCommits(w.Path, full, filepath.Join(dir, name)); err != nil {
			return 0, "", fmt.Errorf("%s: %w", filepath.Base(w.Repo), err)
		}
		man.Repos = append(man.Repos, spinoffBundle{Origin: w.Origin, Bundle: name, Commits: full})
		files = append(files, [2]string{name, filepath.Join(dir, name)})
	}
	if len(man.Repos) == 0 {
		return 0, "", errors.New("none of the part's commits are in the repositories ticked")
	}
	b, err := json.Marshal(man)
	if err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), b, 0o600); err != nil {
		return 0, "", err
	}
	return move.WritePack(filepath.Join(dir, "pack"), files)
}

// SpinoffPackDrop has the source let go of the pack of SpinoffPack Job.
type SpinoffPackDrop struct{ Job string }

func (q SpinoffPackDrop) Do(m *Core) Cmd {
	if dir := m.packsOut[q.Job]; dir != "" {
		delete(m.packsOut, q.Job)
		go os.RemoveAll(dir)
	}
	return nil
}

// SpinoffWhere is a repository the target is to make a part's worktree
// in: Path, one of its own, or with Path "", the one it clones from
// Origin into a folder Name. Own is set when it is one of the tab's the
// part came from, by its origin: the commits go there.
type SpinoffWhere struct {
	Name, Origin, Path string
	Own                bool
}

// SpinoffLand has the target make the branch Name and the tab of Part,
// running Kind, in Repos (by SpinoffFoundMsg, and the target's own
// others), in the group named Group. With Reuse the branch starts with
// the part's commits, from move Job's pack (Size bytes, Sum its SHA-256);
// Elsewhere is set when none of Repos is Own. Parent is the tab the part
// came from, as its agent is told of it. Told as a SpinoffLandedMsg.
type SpinoffLand struct {
	Job         string
	Part        spinoff.Part
	Kind        agents.Kind
	Name, Group string
	Repos       []SpinoffWhere
	Parent      spinoff.Parent
	Reuse       bool
	Elsewhere   bool
	Size        int64
	Sum         string
}

// SpinoffLandedMsg is the tab of SpinoffLand Job made on the target, as
// T, and Outcome what became of the part; Err why nothing was made, for
// the user to put right in the dialog.
type SpinoffLandedMsg struct {
	Job     string
	T       TabRef
	Outcome spinoff.Outcome
	Err     string
}

// spinoffCloned is the repositories of a SpinoffLand cloned, or not, for
// the loop to go on with.
type spinoffCloned struct {
	q   SpinoffLand
	err error
}

// spinoffLanded is a part's worktrees made on the target, for the loop to
// make its tab.
type spinoffLanded struct {
	q   SpinoffLand
	res SpinoffMsg
}

func (q SpinoffLand) Do(m *Core) Cmd {
	if why := m.landPartRefusal(q); why != "" {
		m.dropMoveIn(q.Job) // what came of the commits, if any
		return m.Tell(SpinoffLandedMsg{Job: q.Job, Err: why})
	}
	if !slices.ContainsFunc(q.Repos, func(r SpinoffWhere) bool { return r.Path == "" }) {
		return m.spinoffLandSetup(q)
	}
	paths, cloneDir := m.repoPaths(), m.Scan.NewRepoDir
	return func() Event {
		for i, r := range q.Repos {
			if r.Path != "" {
				continue
			}
			// Looked for again: an earlier part may have cloned it since
			// the dialog asked.
			t := findRepo(MoveRepo{Name: r.Name, Origin: r.Origin}, paths, cloneDir, true)
			if t.Err != "" {
				return spinoffCloned{q: q, err: fmt.Errorf("%s: %s", r.Name, t.Err)}
			}
			q.Repos[i].Path = t.Path
		}
		return spinoffCloned{q: q}
	}
}

// landPartRefusal is why SpinoffLand q cannot be done here, "" when it
// can.
func (m *Core) landPartRefusal(q SpinoffLand) string {
	paths := m.repoPaths()
	switch {
	case !validJob.MatchString(q.Job):
		return "no spinoff " + q.Job
	case !slices.Contains(m.Available(), q.Kind):
		return string(q.Kind) + " is not on PATH there"
	case q.Part.Sandboxed && !m.SandboxedKind(q.Kind):
		return "an agent in a sandbox sent this part, and " + string(q.Kind) + " is not set sandboxed there"
	case m.TabLimitReached():
		return "the tab limit (max_tabs) is reached there: close a tab there first, or skip this part"
	case len(q.Repos) == 0:
		return "pick at least one repository"
	case q.Reuse && m.movesIn[q.Job] == "":
		return "the part's commits did not come"
	case m.WtRoot == "":
		return "there is no folder to make worktrees in there"
	}
	for _, r := range q.Repos {
		switch {
		case r.Path == "" && move.OriginKey(r.Origin) == "":
			return r.Name + " has no origin to clone it from"
		case r.Path != "" && !slices.Contains(paths, r.Path):
			return "no repository " + r.Path + " there"
		}
	}
	return ""
}

// onSpinoffCloned goes on with SpinoffLand once what it lacked is cloned.
func (m *Core) onSpinoffCloned(ev spinoffCloned) Cmd {
	scan := m.ScanRepos()
	if ev.err != nil {
		m.dropMoveIn(ev.q.Job)
		return Batch(scan, m.Tell(SpinoffLandedMsg{Job: ev.q.Job, Err: ev.err.Error()}))
	}
	return Batch(scan, m.spinoffLandSetup(ev.q))
}

// spinoffLandSetup makes the worktrees of SpinoffLand q, once the user
// has allowed the repositories' setup.
func (m *Core) spinoffLandSetup(q SpinoffLand) Cmd {
	var paths []string
	for _, r := range q.Repos {
		paths = append(paths, r.Path)
	}
	if q.Part.Sandboxed && q.Reuse {
		var imported []workspace.Worktree
		for _, p := range paths {
			imported = append(imported, workspace.Worktree{Repo: p})
		}
		m.ImportSandboxed(imported)
	}
	return m.AskSetup(paths, func() Cmd { return m.spinoffLandMake(q) })
}

// spinoffLandMake makes the worktrees of SpinoffLand q in the background,
// bringing the commits in from its pack.
func (m *Core) spinoffLandMake(q SpinoffLand) Cmd {
	wtRoot, allowed, dir := m.WtRoot, SetupAllowedFile(), m.movesIn[q.Job]
	return func() Event {
		var repos []workspace.Repo
		for _, r := range q.Repos {
			repos = append(repos, workspace.Repo{Name: r.Name, Path: r.Path})
		}
		res := SpinoffMsg{Kind: q.Kind, Group: q.Group, Outcome: spinoff.Outcome{Part: q.Part, Branch: q.Name,
			Agent: string(q.Kind), Reused: q.Reuse, Elsewhere: q.Elsewhere}}
		var pick func(workspace.Worktree) error
		if q.Reuse {
			man, in, err := readPartPack(dir, q.Size, q.Sum)
			if err != nil {
				res.Err = fmt.Errorf("the part's commits did not come whole: %w", err)
				return spinoffLanded{q: q, res: res}
			}
			own := map[string]bool{}
			for _, r := range q.Repos {
				own[filepath.Clean(r.Path)] = r.Own
			}
			pick = func(w workspace.Worktree) error {
				if !own[filepath.Clean(w.Repo)] {
					return nil
				}
				for _, b := range man.Repos {
					if !move.SameOrigin(w.Repo, b.Origin) {
						continue
					}
					if err := move.UnbundleCommits(w.Repo, filepath.Join(in, b.Bundle), b.Commits); err != nil {
						return fmt.Errorf("%s: %w", filepath.Base(w.Repo), err)
					}
					_, err := workspace.Pick(w, b.Commits)
					return err
				}
				return nil
			}
		}
		makePart(&res, wtRoot, allowed, repos, pick)
		return spinoffLanded{q: q, res: res}
	}
}

// readPartPack unpacks the pack of a part's commits in dir, Size bytes
// whose SHA-256 is sum, and gives its manifest and the folder it is
// unpacked in. A pack from another machine is data: its names are
// checked.
func readPartPack(dir string, size int64, sum string) (spinoffManifest, string, error) {
	var man spinoffManifest
	in := filepath.Join(dir, "in")
	if err := os.Mkdir(in, 0o700); err != nil {
		return man, "", err
	}
	if st, err := os.Stat(filepath.Join(dir, "pack")); err != nil || st.Size() != size {
		return man, "", errors.New("the pack did not arrive whole")
	}
	names, err := move.ReadPack(filepath.Join(dir, "pack"), sum, in)
	if err != nil {
		return man, "", err
	}
	if !slices.Contains(names, manifestFile) {
		return man, "", errors.New("the pack has no manifest")
	}
	b, err := os.ReadFile(filepath.Join(in, manifestFile))
	if err == nil {
		err = json.Unmarshal(b, &man)
	}
	if err != nil {
		return man, "", err
	}
	for _, r := range man.Repos {
		if !slices.Contains(names, r.Bundle) || r.Bundle == manifestFile || len(r.Commits) == 0 {
			return man, "", fmt.Errorf("no bundle %q", r.Bundle)
		}
		for _, c := range r.Commits {
			if !spinoff.FullCommit(c) {
				return man, "", fmt.Errorf("no commit %q", c)
			}
		}
	}
	return man, in, nil
}

// dropMoveIn forgets what came of move or spinoff job.
func (m *Core) dropMoveIn(job string) {
	if dir := m.movesIn[job]; dir != "" {
		delete(m.movesIn, job)
		go os.RemoveAll(dir)
	}
}

// undoPart takes the worktrees wts, just made for a part, and their new
// branches away again, a branch only while it is where it was made; group
// is their folder, "" for one.
func undoPart(group string, wts []workspace.Worktree) {
	for _, w := range wts {
		head, err := workspace.ResolveCommit(w.Path, "HEAD")
		if rerr := workspace.Remove("", []workspace.Worktree{w}, true); rerr == nil && err == nil {
			_ = workspace.DeleteBranch(w, head)
		}
	}
	if group != "" {
		_ = os.Remove(group)
	}
}

// onSpinoffLanded makes the tab of a part whose worktrees are made here,
// and tells the client what became of the part.
func (m *Core) onSpinoffLanded(ev spinoffLanded) Cmd {
	q, res := ev.q, ev.res
	m.dropMoveIn(q.Job)
	if q.Part.Sandboxed && res.Outcome.Reused && len(res.Wts) > 0 {
		m.ImportSandboxed(res.Wts)
	}
	if res.Err != nil {
		return m.Tell(SpinoffLandedMsg{Job: q.Job, Err: res.Err.Error()})
	}
	o := res.Outcome
	if q.Part.Sandboxed && !m.SandboxedKind(q.Kind) {
		// The config changed while the worktree was made: what was made
		// goes again, and the part waits, for the user to set the agent
		// sandboxed again, or pick another.
		wts, group := res.Wts, ""
		if len(wts) > 1 {
			group = res.Plan.Dir
		}
		why := string(q.Kind) + " is no longer set sandboxed there; nothing is left of the tab there"
		return Later(func() Event {
			undoPart(group, wts)
			return SpinoffLandedMsg{Job: q.Job, Err: why}
		})
	}
	nt, cmd := m.addTab(AddTab{ID: NewTabID(), Name: res.Plan.Branch, Dir: res.Plan.Dir, Group: q.Group, Wts: res.Wts,
		Kind: q.Kind, Task: spinoff.Prompt(q.Part, q.Parent, o), Goal: q.Part.Summary,
		Sandboxed: q.Part.Sandboxed, ImportedSandboxed: q.Part.Sandboxed && o.Reused})
	o.Tab = nt.ID
	note := "made " + res.Plan.Branch
	if q.Parent.Machine != "" {
		note += ", split off from " + q.Parent.Name + " on " + q.Parent.Machine
	}
	if o.PickFailed != "" {
		note += ": its commits did not come over cleanly, so its agent brings them in"
	}
	if len(res.Warnings) > 0 {
		note += "; " + strings.Join(res.Warnings, "; ")
	}
	m.Toast(note)
	return Batch(cmd, m.Tell(PanesMsg{}), m.Tell(SpinoffLandedMsg{Job: q.Job, T: m.Ref(nt), Outcome: o}))
}
