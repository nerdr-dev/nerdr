package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/handoff"
	"github.com/nerdr-dev/nerdr/internal/move"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Moving a tab to another machine (see internal/move for the git of it).
// The cores do not know each other: the client asks each its part, and
// carries the work from one to the other in chunks.
//
//  1. The target says whether it has the repositories (same origin) and
//     what of their branches it has (MoveCheck); the source what the move
//     would carry and leave, and how large it is (MoveLook). The client
//     shows it, and the user says yes; a repository the target lacks is
//     cloned there first (MoveCheck with Clone).
//  2. The source asks the agent for a handoff, waits for it (or for
//     MoveNow), and stops the agent (MoveStart).
//  3. The source packs the work, the conversation and the tab's data
//     (MovePackedMsg), and the client carries the pack over a chunk at a
//     time (MoveChunk, MovePut).
//  4. The target makes the tab and says so (MoveLand, MoveLandedMsg).
//  5. Only then does the source close its tab (MoveDone), keeping its
//     worktree unless the user says not to. Anything failing before that
//     has the source carry on with its agent (MoveAbort), and the target
//     drop what it got (MoveDrop).
//
// A pack from another machine is data: its names and paths are checked,
// the target makes its own, and the handoff in it is an agent's text.

func init() {
	registerRequests(MoveCheck{}, MoveLook{}, MoveStart{}, MoveNow{}, MoveChunk{}, MoveAbort{}, MoveDone{},
		MovePut{}, MoveLand{}, MoveDrop{})
	registerEvents(MoveCheckMsg{}, MoveLookMsg{}, MovePackedMsg{}, MoveChunkMsg{}, MovePutMsg{}, MoveLandedMsg{})
}

// MoveWait is a tab being moved: Job is the move's, and Packing is set
// once its agent is stopped and the work is being packed.
type MoveWait struct {
	Job     string
	Packing bool
}

// moveOut is a move of a tab away, on the source.
type moveOut struct {
	t    *Tab
	have [][]string
	dir  string
	// stopped is set once the agent was stopped for the move, to start
	// again if it does not go.
	stopped bool
}

// validJob is a move's job id: it names temporary folders.
var validJob = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// MoveRepo is a repository of the tab to move: its folder name, origin
// and the branch the tab is on.
type MoveRepo struct {
	Name, Origin, Branch string
}

// MoveCheck asks the target what it has of Repos: told as a MoveCheckMsg.
// With Clone, a repository it lacks is cloned from its origin first.
type MoveCheck struct {
	Job   string
	Repos []MoveRepo
	Clone bool
}

// MoveTarget is what the target has of one repository: Path, the one
// with the same origin, Have the commits of it a bundle may leave out,
// and Tip where its branch of the name is ("" when it has none); or,
// with none found, CloneInto the folder it would be cloned into. Err is
// why the work cannot land there.
type MoveTarget struct {
	Path, CloneInto string
	Have            []string
	Tip             string
	Err             string
}

// MoveCheckMsg answers MoveCheck of Job: a MoveTarget for each of its
// Repos, and Dir, the folder the tab's worktrees will be in. Err is why
// the tab cannot move there at all.
type MoveCheckMsg struct {
	Job   string
	Repos []MoveTarget
	Dir   string
	Err   string
}

func (q MoveCheck) Do(m *Core) Cmd {
	var paths []string
	for _, r := range m.Scan.Repos {
		paths = append(paths, r.Path)
	}
	if m.Root != "" && !slices.Contains(paths, m.Root) {
		paths = append(paths, m.Root)
	}
	wtRoot, cloneDir := m.WtRoot, m.Scan.NewRepoDir
	cmd := Later(func() Event { return checkMove(q, paths, wtRoot, cloneDir) })
	if q.Clone {
		return Batch(cmd, m.ScanRepos())
	}
	return cmd
}

func checkMove(q MoveCheck, paths []string, wtRoot, cloneDir string) MoveCheckMsg {
	msg := MoveCheckMsg{Job: q.Job}
	if len(q.Repos) == 0 {
		msg.Err = "the tab has no worktree to move"
		return msg
	}
	var repos []workspace.Repo
	for _, r := range q.Repos {
		var t MoveTarget
		switch {
		case workspace.ValidBranch(r.Branch) != nil:
			t.Err = "no branch to move"
		case move.OriginKey(r.Origin) == "":
			t.Err = r.Name + " has no origin, so nerdr cannot tell which repository it is there"
		}
		if t.Err == "" {
			for _, p := range paths {
				if move.SameOrigin(p, r.Origin) {
					t.Path = p
					break
				}
			}
		}
		if t.Err == "" && t.Path == "" {
			switch {
			case cloneDir == "":
				t.Err = "there is no repository with origin " + r.Origin + ", and no folder to clone it into"
			case workspace.CheckNewRepo(cloneDir, r.Name) != nil:
				t.Err = "there is no repository with origin " + r.Origin + ", and " + workspace.CheckNewRepo(cloneDir, r.Name).Error()
			case q.Clone:
				p, err := workspace.CloneRepo(cloneDir, r.Name, r.Origin)
				if err != nil {
					t.Err = err.Error()
				}
				t.Path = p
			default:
				t.CloneInto = filepath.Join(cloneDir, r.Name)
			}
		}
		if t.Path != "" && t.Err == "" {
			t.Err = move.CanLand(t.Path, move.Part{Branch: r.Branch})
			t.Have, t.Tip = move.Have(t.Path, r.Branch), move.Tip(t.Path, r.Branch)
		}
		msg.Repos = append(msg.Repos, t)
		repos = append(repos, workspace.Repo{Name: r.Name, Path: t.Path})
	}
	if wtRoot == "" {
		msg.Err = "there is no folder to make worktrees in"
		return msg
	}
	msg.Dir = workspace.DirFor(wtRoot, q.Repos[0].Branch, repos)
	if _, err := os.Lstat(msg.Dir); err == nil {
		msg.Err = msg.Dir + " already exists there"
	}
	return msg
}

// MoveLook asks the source what moving tab T would carry and leave, with
// Have and Tips the target's, by worktree: told as a MoveLookMsg.
type MoveLook struct {
	Job  string
	T    TabRef
	Have [][]string
	Tips []string
}

// MoveLookMsg answers MoveLook of Job: each worktree's Look, and Size,
// how large the move would be now. Err is why the tab cannot be moved.
type MoveLookMsg struct {
	Job   string
	Repos []move.Look
	Size  int64
	Err   string
}

func (q MoveLook) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	if err := m.CanMove(t); err != "" {
		return m.Tell(MoveLookMsg{Job: q.Job, Err: err})
	}
	wts := slices.Clone(t.Worktrees)
	return Later(func() Event {
		msg := MoveLookMsg{Job: q.Job}
		for i, w := range wts {
			l := move.LookAt(w.Path)
			if l.Refuse == "" && i < len(q.Tips) && q.Tips[i] != "" && !move.Ancestor(w.Path, q.Tips[i], "HEAD") {
				l.Refuse = "branch " + l.Branch + " there has commits this one has not"
			}
			if l.Refuse != "" {
				msg.Err = filepath.Base(w.Repo) + ": " + l.Refuse
			}
			msg.Repos = append(msg.Repos, l)
		}
		if msg.Err != "" {
			return msg
		}
		dir, err := os.MkdirTemp("", "nerdr-move-look-")
		if err != nil {
			msg.Err = err.Error()
			return msg
		}
		defer os.RemoveAll(dir)
		size, _, err := packWork(dir, wts, q.Have, nil, "")
		msg.Size = size
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

// CanMove is why t cannot be moved now, "" when it can.
func (m *Core) CanMove(t *Tab) string {
	switch {
	case t.PartOf != nil || t.ReviewOf != nil:
		return "only a tab's own agent moves with it"
	case t.Moving != nil:
		return "the tab is being moved already"
	case len(t.Worktrees) == 0:
		return "the tab has no worktree to move"
	case m.ReposHold(t):
		return "the tab's repositories are being changed"
	case t.Handoff != nil:
		return "the tab waits for a handoff to switch its agent"
	}
	return ""
}

// MoveStart starts moving tab T: with Handoff its agent is asked for one
// first, then it is stopped and the work packed, leaving out Have, the
// target's commits by worktree. Told as a MovePackedMsg.
type MoveStart struct {
	Job     string
	T       TabRef
	Have    [][]string
	Handoff bool
}

// MovePackedMsg is the work of tab T packed for move Job: Size bytes,
// whose SHA-256 is Sum, with Heads the commit of each worktree's branch
// it carries, by path. Err is why it was not.
type MovePackedMsg struct {
	Job   string
	T     TabRef
	Size  int64
	Sum   string
	Heads map[string]string
	Err   string
}

func (q MoveStart) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return m.Tell(MovePackedMsg{Job: q.Job, T: q.T, Err: "the tab is gone"})
	}
	if err := m.CanMove(t); err != "" || !validJob.MatchString(q.Job) {
		if err == "" {
			err = "no move " + q.Job
		}
		return m.Tell(MovePackedMsg{Job: q.Job, T: q.T, Err: err})
	}
	dir, err := os.MkdirTemp("", "nerdr-move-out-")
	if err != nil {
		return m.Tell(MovePackedMsg{Job: q.Job, T: q.T, Err: err.Error()})
	}
	if m.movesOut == nil {
		m.movesOut = map[string]*moveOut{}
	}
	m.movesOut[q.Job] = &moveOut{t: t, have: q.Have, dir: dir}
	t.Moving = &MoveWait{Job: q.Job}
	if q.Handoff && t.AgentRunning() && !t.QueuePaused {
		w := &HandoffWait{Move: true, Asked: state.Message{Text: handoff.MoveRequest(t.Agent), Auto: true}, AskedAt: time.Now()}
		t.Messages = append([]state.Message{w.Asked}, t.Messages...)
		t.Handoff = w
		m.SaveSession()
		m.Tell(PanesMsg{})
		m.Toast(m.AgentName(t) + ": asked " + string(t.Agent) + " for a handoff before the move")
		return nil
	}
	return m.movePack(t, "")
}

// MoveNow packs tab T for move Job without waiting for its agent's
// handoff any longer.
type MoveNow struct {
	Job string
	T   TabRef
}

func (q MoveNow) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil || t.Moving == nil || t.Moving.Job != q.Job || t.Moving.Packing {
		return nil
	}
	return m.movePack(t, "")
}

// moveManifest is what a pack says of the tab moved, besides its work.
type moveManifest struct {
	Name, Group, Note, Goal string
	Messages                []state.Message
	Agent                   agents.Kind
	// Sandboxed is whether the agent ran in a sandbox.
	Sandboxed bool
	// Handoff is what the agent wrote for the next, "" when it wrote
	// none; Transcript the file in the pack with its conversation, ""
	// when there is none, and TranscriptErr why.
	Handoff       string
	Transcript    string
	TranscriptErr string
	// Sub is the folder the agent works in, within the tab's.
	Sub   string
	Parts []move.Part
}

const (
	manifestFile   = "manifest.json"
	transcriptFile = "transcript.md"
)

// movePack stops t's agent and packs its work, with summary the handoff
// it wrote; see MoveStart.
func (m *Core) movePack(t *Tab, summary string) Cmd {
	o := m.movesOut[t.Moving.Job]
	if o == nil {
		return nil
	}
	if w := t.Handoff; w != nil {
		if i := t.FindMessage(w.Asked); i >= 0 {
			t.DropMessage(i)
		}
		t.Handoff = nil
	}
	t.HandoffReady = nil
	if t.Pane != nil {
		old := t.Pane
		go old.Close()
		t.Pane = nil
		o.stopped = true
	}
	t.Light = StExited
	t.AwaitWork, t.QueuePaused = false, false
	t.Moving.Packing = true
	m.SaveSession()
	m.Tell(PanesMsg{})
	man := moveManifest{
		Name: t.Name, Group: t.Group, Note: t.Note, Goal: t.Goal,
		Messages: slices.Clone(t.Messages), Agent: t.Agent,
		Sandboxed: t.InSandbox || m.SandboxedKind(t.Agent) || t.HasSandboxed(),
		Handoff:   summary,
	}
	wts := slices.Clone(t.Worktrees)
	if root := tabFolder(wts); root != "" {
		if sub, err := filepath.Rel(root, t.Dir); err == nil && sub != "." && filepath.IsLocal(sub) {
			man.Sub = sub
		}
	}
	specs, kind, id, dir := m.SpecsFor(t), t.Agent, t.SessionID, t.Dir
	job, ref, pdir, have := t.Moving.Job, m.Ref(t), o.dir, o.have
	return Later(func() Event {
		text, err := agents.Transcript(specs, kind, id, dir)
		if err != nil {
			man.TranscriptErr = err.Error()
			text = ""
		}
		size, sum, err := packWork(pdir, wts, have, &man, text)
		msg := MovePackedMsg{Job: job, T: ref, Size: size, Sum: sum, Heads: map[string]string{}}
		if err != nil {
			msg.Err = err.Error()
		}
		for i, p := range man.Parts {
			msg.Heads[wts[i].Path] = p.Head
		}
		return msg
	})
}

// tabFolder is the folder of a tab on wts: its worktree's, or with
// several, the one they are in.
func tabFolder(wts []workspace.Worktree) string {
	switch len(wts) {
	case 0:
		return ""
	case 1:
		return wts[0].Path
	}
	return filepath.Dir(wts[0].Path)
}

// packWork packs the work in wts into dir/pack, leaving out have, with
// man and transcript when man is set (else only to see how large it is),
// and gives the pack's size and SHA-256.
func packWork(dir string, wts []workspace.Worktree, have [][]string, man *moveManifest, transcript string) (int64, string, error) {
	var files [][2]string
	var parts []move.Part
	for i, w := range wts {
		p, err := move.Snapshot(w.Path)
		if err != nil {
			return 0, "", fmt.Errorf("%s: %w", filepath.Base(w.Repo), err)
		}
		p.Name = filepath.Base(w.Repo)
		if len(wts) > 1 {
			p.Sub = filepath.Base(w.Path)
		}
		p.Origin = w.Origin
		var h []string
		if i < len(have) {
			h = have[i]
		}
		name := fmt.Sprintf("repo-%d.bundle", i)
		if err := move.Bundle(w.Path, &p, h, filepath.Join(dir, name)); err != nil {
			return 0, "", fmt.Errorf("%s: %w", p.Name, err)
		}
		if p.Bundle != "" {
			files = append(files, [2]string{name, filepath.Join(dir, name)})
		}
		parts = append(parts, p)
	}
	if man != nil {
		man.Parts = parts
		if transcript != "" {
			if err := os.WriteFile(filepath.Join(dir, transcriptFile), []byte(transcript), 0o600); err != nil {
				return 0, "", err
			}
			man.Transcript = transcriptFile
			files = append(files, [2]string{transcriptFile, filepath.Join(dir, transcriptFile)})
		}
		b, err := json.Marshal(man)
		if err != nil {
			return 0, "", err
		}
		if err := os.WriteFile(filepath.Join(dir, manifestFile), b, 0o600); err != nil {
			return 0, "", err
		}
		files = append([][2]string{{manifestFile, filepath.Join(dir, manifestFile)}}, files...)
	}
	return move.WritePack(filepath.Join(dir, "pack"), files)
}

// MoveChunk asks the source for the chunk of move Job's pack at Off: told
// as a MoveChunkMsg.
type MoveChunk struct {
	Job string
	Off int64
}

// MoveChunkMsg is the chunk of move Job's pack at Off: at most
// move.Chunk bytes, fewer at its end.
type MoveChunkMsg struct {
	Job  string
	Off  int64
	Data []byte
	Err  string
}

func (q MoveChunk) Do(m *Core) Cmd {
	o := m.movesOut[q.Job]
	if o == nil || o.t.Moving == nil || !o.t.Moving.Packing {
		return m.Tell(MoveChunkMsg{Job: q.Job, Off: q.Off, Err: "no pack for the move"})
	}
	file := filepath.Join(o.dir, "pack")
	return Later(func() Event {
		msg := MoveChunkMsg{Job: q.Job, Off: q.Off}
		f, err := os.Open(file)
		if err != nil {
			msg.Err = err.Error()
			return msg
		}
		defer f.Close()
		buf := make([]byte, move.Chunk)
		n, err := f.ReadAt(buf, q.Off)
		if err != nil && !errors.Is(err, io.EOF) {
			msg.Err = err.Error()
		}
		msg.Data = buf[:n]
		return msg
	})
}

// MoveAbort gives up move Job of tab T on the source: the tab carries on
// here, its agent started again if the move stopped it. Why is told.
type MoveAbort struct {
	Job string
	T   TabRef
	Why string
}

func (q MoveAbort) Do(m *Core) Cmd {
	return m.moveAbort(q.Job, q.Why)
}

// moveAbort is MoveAbort.
func (m *Core) moveAbort(job, why string) Cmd {
	o := m.movesOut[job]
	if o == nil {
		return nil
	}
	delete(m.movesOut, job)
	go os.RemoveAll(o.dir)
	t := o.t
	if t.Moving == nil || t.Moving.Job != job {
		return nil
	}
	t.Moving = nil
	if w := t.Handoff; w != nil && w.Move {
		m.CancelHandoff(t)
	}
	var cmd Cmd
	if m.TabIndex(t) >= 0 && o.stopped && t.Pane == nil {
		cmd = m.Start(t, true)
	} else if t.Pane != nil {
		t.Light = StIdle
	}
	m.SaveSession()
	m.Tell(PanesMsg{})
	msg := m.AgentName(t) + ": not moved"
	if why != "" {
		msg += " (" + why + ")"
	}
	m.Toast(msg + "; it carries on here")
	return cmd
}

// MoveDone ends move Job of tab T on the source, once the target has the
// tab: the tab is closed, and with Remove its worktrees deleted, and with
// BranchHeads their branches as well (see CloseTab).
type MoveDone struct {
	Job         string
	T           TabRef
	Remove      bool
	BranchHeads map[string]string
}

func (q MoveDone) Do(m *Core) Cmd {
	o := m.movesOut[q.Job]
	if o == nil {
		return nil
	}
	delete(m.movesOut, q.Job)
	go os.RemoveAll(o.dir)
	t := o.t
	t.Moving = nil
	return m.CloseTab(t, q.Remove, true, q.BranchHeads)
}

// MovePut is a chunk of move Job's pack at Off, for the target.
type MovePut struct {
	Job  string
	Off  int64
	Data []byte
}

// MovePutMsg says the chunk of move Job at Off is written; Err that it
// was not.
type MovePutMsg struct {
	Job string
	Off int64
	Err string
}

func (q MovePut) Do(m *Core) Cmd {
	if !validJob.MatchString(q.Job) {
		return m.Tell(MovePutMsg{Job: q.Job, Off: q.Off, Err: "no move " + q.Job})
	}
	if q.Off < 0 || q.Off+int64(len(q.Data)) > move.Limit {
		return m.Tell(MovePutMsg{Job: q.Job, Off: q.Off, Err: move.TooBig(q.Off + int64(len(q.Data))).Error()})
	}
	if m.movesIn == nil {
		m.movesIn = map[string]string{}
	}
	dir := m.movesIn[q.Job]
	if dir == "" {
		if q.Off != 0 {
			return m.Tell(MovePutMsg{Job: q.Job, Off: q.Off, Err: "the move's first chunk did not come"})
		}
		d, err := os.MkdirTemp("", "nerdr-move-in-")
		if err != nil {
			return m.Tell(MovePutMsg{Job: q.Job, Off: q.Off, Err: err.Error()})
		}
		dir, m.movesIn[q.Job] = d, d
	}
	return Later(func() Event {
		msg := MovePutMsg{Job: q.Job, Off: q.Off}
		f, err := os.OpenFile(filepath.Join(dir, "pack"), os.O_WRONLY|os.O_CREATE, 0o600)
		if err == nil {
			_, err = f.WriteAt(q.Data, q.Off)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

// MoveDrop has the target forget move Job, and what it got of it.
type MoveDrop struct{ Job string }

func (q MoveDrop) Do(m *Core) Cmd {
	if dir := m.movesIn[q.Job]; dir != "" {
		delete(m.movesIn, q.Job)
		go os.RemoveAll(dir)
	}
	return nil
}

// MoveLand makes the tab of move Job on the target, from its pack (Size
// bytes, Sum its SHA-256), as tab ID, with agent Kind, the worktrees in
// Repos (by MoveCheckMsg) and in Dir. From is the machine it came from,
// as the user's nerdr names it. A tab whose agent was in a sandbox lands
// with Kind not in one only with Weaker: the user said so. Told as a
// MoveLandedMsg.
type MoveLand struct {
	Job, ID   string
	Size      int64
	Sum       string
	Kind      agents.Kind
	Repos     []string
	Dir       string
	From      string
	Sandboxed bool
	Weaker    bool
}

// MoveLandedMsg is move Job's tab made on the target, as T; Err why it
// was not.
type MoveLandedMsg struct {
	Job string
	T   TabRef
	Err string
}

// moveLanded is the work of a move put in its worktrees, for the loop to
// make its tab.
type moveLanded struct {
	q      MoveLand
	wts    []workspace.Worktree
	man    moveManifest
	dir    string
	prompt string
	err    error
}

func (q MoveLand) Do(m *Core) Cmd {
	dir := m.movesIn[q.Job]
	if why := m.landRefusal(q); why != "" {
		return m.Tell(MoveLandedMsg{Job: q.Job, Err: why})
	}
	wtRoot := m.WtRoot
	return func() Event {
		ev := landMove(q, dir, wtRoot)
		return ev
	}
}

// landRefusal is why move q cannot land here, "" when it can: the
// agent is not here, or would be in less of a sandbox than the tab's was
// without the user's yes to it.
func (m *Core) landRefusal(q MoveLand) string {
	switch {
	case m.movesIn[q.Job] == "":
		return "nothing of the move came"
	case !slices.Contains(m.Available(), q.Kind):
		return string(q.Kind) + " is not on PATH there"
	case q.Sandboxed && !m.SandboxedKind(q.Kind) && !q.Weaker:
		return string(q.Kind) + " is not set to run in a sandbox there, and the tab's agent was in one"
	case !validID.MatchString(q.ID) || m.tabByID(q.ID) != nil:
		return "no tab id " + q.ID
	}
	return ""
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// landMove unpacks move q's pack in dir and puts its work in worktrees
// under wtRoot.
func landMove(q MoveLand, dir, wtRoot string) moveLanded {
	ev := moveLanded{q: q}
	in := filepath.Join(dir, "in")
	if err := os.Mkdir(in, 0o700); err != nil {
		ev.err = err
		return ev
	}
	if st, err := os.Stat(filepath.Join(dir, "pack")); err != nil || st.Size() != q.Size {
		ev.err = errors.New("the move did not arrive whole")
		return ev
	}
	names, err := move.ReadPack(filepath.Join(dir, "pack"), q.Sum, in)
	if err != nil {
		ev.err = err
		return ev
	}
	if !slices.Contains(names, manifestFile) {
		ev.err = errors.New("the pack has no manifest")
		return ev
	}
	b, err := os.ReadFile(filepath.Join(in, manifestFile))
	if err == nil {
		err = json.Unmarshal(b, &ev.man)
	}
	if err == nil {
		err = checkManifest(&ev.man, names, len(q.Repos))
	}
	if err != nil {
		ev.err = fmt.Errorf("the pack's manifest: %w", err)
		return ev
	}
	man := ev.man
	var repos []workspace.Repo
	for i, p := range man.Parts {
		if !move.SameOrigin(q.Repos[i], p.Origin) {
			ev.err = fmt.Errorf("%s is no longer a repository with origin %s", q.Repos[i], p.Origin)
			return ev
		}
		repos = append(repos, workspace.Repo{Name: p.Name, Path: q.Repos[i]})
	}
	folder := workspace.DirFor(wtRoot, man.Parts[0].Branch, repos)
	if folder != q.Dir {
		ev.err = errors.New("the folder for the worktrees is not the one checked")
		return ev
	}
	if _, err := os.Lstat(folder); err == nil {
		ev.err = errors.New(folder + " already exists there")
		return ev
	}
	var made []workspace.Worktree
	undo := func() {
		for _, w := range made {
			_ = workspace.Remove("", []workspace.Worktree{w}, true)
		}
		if len(man.Parts) > 1 {
			_ = os.Remove(folder)
		}
	}
	for i, p := range man.Parts {
		path := folder
		if len(man.Parts) > 1 {
			path = filepath.Join(folder, p.Sub)
		}
		bundle := ""
		if p.Bundle != "" {
			bundle = filepath.Join(in, p.Bundle)
		}
		if err := move.Land(q.Repos[i], path, p, bundle); err != nil {
			undo()
			ev.err = fmt.Errorf("%s: %w", p.Name, err)
			return ev
		}
		w := workspace.Worktree{Repo: q.Repos[i], Path: path, Branch: p.Branch, Pushes: workspace.OriginURLs(path)}
		if len(w.Pushes) > 0 {
			w.Origin = w.Pushes[0]
		}
		made = append(made, w)
	}
	ev.wts = made
	ev.dir = filepath.Join(tabFolder(made), man.Sub)
	info := handoff.Info{Machine: q.From, From: man.Agent, To: q.Kind, Summary: man.Handoff, TranscriptErr: man.TranscriptErr}
	if man.Transcript != "" {
		text, err := os.ReadFile(filepath.Join(in, man.Transcript))
		if err == nil {
			path := handoff.Path(made[0].Path, q.ID, man.Agent)
			err = store.WriteFile(path, text, 0o600)
			info.Transcript, info.TranscriptLen = path, len(text)
		}
		if err != nil {
			info.Transcript, info.TranscriptErr = "", err.Error()
		}
	}
	ev.prompt = handoff.Prompt(info)
	return ev
}

// checkManifest checks what a pack from another machine says of its tab:
// names and folders it may use, a part for each of the n repositories,
// and the files it names in the pack.
func checkManifest(man *moveManifest, files []string, n int) error {
	if len(man.Parts) != n || n == 0 {
		return fmt.Errorf("%d repositories, not %d", len(man.Parts), n)
	}
	if !agents.Known(man.Agent) {
		return fmt.Errorf("no agent %q", man.Agent)
	}
	for _, p := range man.Parts {
		if workspace.ValidBranch(p.Branch) != nil {
			return fmt.Errorf("no branch %q", p.Branch)
		}
		if n > 1 && (p.Sub == "" || !filepath.IsLocal(p.Sub) || strings.ContainsRune(p.Sub, filepath.Separator)) {
			return fmt.Errorf("no folder %q", p.Sub)
		}
		if p.Bundle != "" && !slices.Contains(files, p.Bundle) {
			return fmt.Errorf("no bundle %q", p.Bundle)
		}
	}
	if man.Sub != "" && !filepath.IsLocal(man.Sub) {
		return fmt.Errorf("no folder %q", man.Sub)
	}
	if man.Transcript != "" && !slices.Contains(files, man.Transcript) {
		return fmt.Errorf("no transcript %q", man.Transcript)
	}
	if len(man.Handoff) > handoff.MaxSummary {
		man.Handoff = man.Handoff[:handoff.MaxSummary]
	}
	man.Name = clip(man.Name, 200)
	man.Group = clip(man.Group, 200)
	man.Goal = clip(man.Goal, 4096)
	man.Note = clip(man.Note, 64<<10)
	return nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// tabByID is the tab of id, nil when there is none.
func (m *Core) tabByID(id string) *Tab {
	for _, t := range m.Tabs {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// onMoveLanded makes the tab of a move whose work has landed.
func (m *Core) onMoveLanded(ev moveLanded) Cmd {
	q := ev.q
	if dir := m.movesIn[q.Job]; dir != "" {
		delete(m.movesIn, q.Job)
		go os.RemoveAll(dir)
	}
	if ev.err != nil {
		return m.Tell(MoveLandedMsg{Job: q.Job, Err: ev.err.Error()})
	}
	man := ev.man
	t, cmd := m.addTab(AddTab{ID: q.ID, Name: man.Name, Dir: ev.dir, Group: man.Group, Wts: ev.wts, Kind: q.Kind,
		Task: ev.prompt, Goal: man.Goal, Sandboxed: man.Sandboxed})
	t.Note = man.Note
	for _, msg := range man.Messages {
		msg.Text = clip(msg.Text, 64<<10)
		t.Messages = append(t.Messages, msg)
	}
	m.SaveSession()
	m.Toast(t.Name + ": moved here from " + q.From)
	return Batch(cmd, m.ScanRepos(), m.Tell(MoveLandedMsg{Job: q.Job, T: m.Ref(t)}))
}

// movesGone gives up the moves going on when the client that carried
// them goes: the tabs on the source carry on there.
func (m *Core) movesGone() Cmd {
	var cmds []Cmd
	for job := range m.movesOut {
		cmds = append(cmds, m.moveAbort(job, "the client went"))
	}
	for job := range m.movesIn {
		MoveDrop{Job: job}.Do(m)
	}
	return Batch(cmds...)
}
