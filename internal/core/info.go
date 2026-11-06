package core

import (
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

const (
	// InfoEvery is how often a tab's branches are looked at again, and
	// prEvery how often its pull request, which asks GitHub: the tab on
	// screen, and one whose agent is working. The intervals of asking
	// GitHub are at the default github_refresh; see githubEvery.
	InfoEvery = 20 * time.Second
	PrEvery   = 90 * time.Second
	// The others change little but by their agent, whose turn ending has
	// them looked at anyway: a look is eight git commands a worktree, and
	// many tabs looked at as often kept git running all the time.
	IdleInfoEvery = 5 * time.Minute
	IdlePREvery   = 10 * time.Minute
	InfoTickN     = 2 * time.Second
	// A look finds out first, without git, whether the branches can have
	// changed (see git.Stamp and maySkip), and when not, keeps what it
	// found last: a tab on screen with nothing going on runs no git. It
	// runs git anyway after fullInfoEvery, for what that does not see.
	FullInfoEvery = 2 * time.Minute
	// PrPanelEvery is how often F2 reads the pull request it shows again.
	PrPanelEvery = 2 * time.Minute
)

// PrState is a pull request's state in words, e.g. "open", and for a
// branch that went on after it was merged or closed, how far, e.g.
// "merged · 1 commit after".
func PrState(pr github.Status) string {
	s := strings.ToLower(pr.State)
	if pr.After > 0 {
		s += " · " + Plural(pr.After, "commit") + " after"
	}
	return s
}

// InfoTickMsg tells the client that core has looked at the background
// work, as it does every InfoTickN: its own turn for the same.
type InfoTickMsg struct{}

type InfoMsg struct {
	Id   string
	Info TabInfo
	// Asks and prAsks are the tab's counts of looks asked for when this
	// one started; see lookAgain.
	Asks, PrAsks int
}

// InfoTick is the background work due now: the branches, the pull requests,
// the issues, and agents to put to sleep or wake. The loop runs it every
// InfoTickN, with a client or without.
func (m *Core) InfoTick(now time.Time) Cmd {
	return Batch(m.RefreshInfo(), m.TickPRs(now), m.TickIssues(now), m.SleepIdle(now))
}

// GithubEvery is d, an interval of asking GitHub written for the default
// github_refresh, made as much longer or shorter as the config's is.
func (m *Core) GithubEvery(d time.Duration) time.Duration {
	n := m.Cfg.GitHubRefresh
	if n <= 0 {
		return d
	}
	return d * time.Duration(n) / config.DefaultGitHubRefresh
}

// LookAt finds out about wts; the pull requests only when withPR is set,
// else they are carried over from prev. With skip, the branches are only
// looked at with git when their git files changed since prev.
func LookAt(wts []workspace.Worktree, prev TabInfo, withPR, skip bool) TabInfo {
	in := TabInfo{At: time.Now(), PrAt: prev.PrAt}
	if withPR {
		in.PrAt = in.At
	}
	var stamps []string
	for _, w := range wts {
		stamps = append(stamps, git.Stamp(w.Path))
	}
	in.Stamp = strings.Join(stamps, "\x00")
	if skip && !slices.Contains(stamps, "") && in.Stamp == prev.Stamp && len(prev.Wts) == len(wts) {
		in.FullAt = prev.FullAt
		in.Wts = slices.Clone(prev.Wts)
		if withPR && github.Available() {
			for i, w := range wts {
				in.Wts[i].Pr, in.Wts[i].HasPR = github.BranchStatus(w.Path)
			}
		}
		return in
	}
	in.FullAt = in.At
	for i, w := range wts {
		wi := WtInfo{Repo: w.Repo, Branch: git.CurrentBranch(w.Path)}
		if wi.Branch != "" {
			w.Branch = wi.Branch
		}
		st, err := workspace.BranchStats(w)
		wi.Stats = st
		if err != nil {
			wi.Err = err.Error()
		} else {
			_, wi.Add, wi.Del = git.ShortStatBase(w.Path, st.Base)
			wi.Unpushed = workspace.UnpushedCount(w)
		}
		wi.Uncommitted = workspace.Uncommitted(w)
		// Another branch has another pull request: the old one's is not
		// carried over to it.
		moved := wi.Branch != "" && i < len(prev.Wts) && prev.Wts[i].Branch != "" && prev.Wts[i].Branch != wi.Branch
		switch {
		case (withPR || moved) && github.Available():
			wi.Pr, wi.HasPR = github.BranchStatus(w.Path)
		case i < len(prev.Wts):
			wi.Pr, wi.HasPR = prev.Wts[i].Pr, prev.Wts[i].HasPR
			if wi.HasPR && wi.Pr.State != "OPEN" {
				// The branch may have moved since GitHub was asked: a
				// commit on top of a merged PR is in no PR, and the tab
				// is no longer done.
				wi.Pr, wi.HasPR = github.Recheck(w.Path, wi.Pr)
			}
		}
		in.Wts = append(in.Wts, wi)
	}
	return in
}

// Overlap is another tab that changes some of the same files.
type Overlap struct {
	With  *Tab
	Files []string
}

// Branches are the other tab's branches in the repositories the files are
// in.
func (o Overlap) Branches() []string {
	repos := map[string]bool{}
	for _, f := range o.Files {
		repo, _, _ := strings.Cut(f, "/")
		repos[repo] = true
	}
	var out []string
	for i, w := range o.With.Worktrees {
		b := w.Branch
		if i < len(o.With.Info.Wts) && o.With.Info.Wts[i].Branch != "" {
			b = o.With.Info.Wts[i].Branch // checked out now
		}
		if repos[BaseName(w.Repo)] && b != "" && !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		out = append(out, o.With.Name)
	}
	return out
}

func BaseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// followBranches takes in the branches a tab's worktrees are on now. A tab
// still named after the branch it was made on is renamed with it; one
// renamed by the user keeps its name.
func (m *Core) FollowBranches(t *Tab) {
	changed := false
	for i, wi := range t.Info.Wts {
		if i >= len(t.Worktrees) || wi.Branch == "" || wi.Branch == t.Worktrees[i].Branch {
			continue
		}
		if i == 0 && t.Name == t.Worktrees[0].Branch {
			t.Name = wi.Branch
		}
		t.Worktrees[i].Branch = wi.Branch
		changed = true
	}
	if changed {
		m.SaveSession()
	}
}

// WtInfo is what was found about one of a tab's worktrees.
type WtInfo struct {
	Repo string
	// Branch is the one checked out now, which need not be the one the tab
	// was made on: switching branch in the tab's shell, or by the agent,
	// leaves the same worktree on another branch. "" when detached.
	Branch   string
	Stats    workspace.Stats
	Add, Del int
	Unpushed int // commits on no remote, which p would push
	// Uncommitted is how many files have changes not committed, which c
	// would commit.
	Uncommitted int
	Pr          github.Status
	HasPR       bool
	Err         string
}

// TabInfo is what was found about a tab.
type TabInfo struct {
	Wts  []WtInfo
	At   time.Time // when the branches were last looked at
	PrAt time.Time // when the pull requests were
	// Stale asks for a look at the next tick, e.g. when the agent has just
	// finished something.
	Stale   bool
	StalePR bool
	// FullAt is when git was last run for the branches, stamp what their
	// git files looked like then, and watchN how many changes the review
	// panel's watch had reported then; see maySkip.
	FullAt time.Time
	Stamp  string
	WatchN int
}

func (in TabInfo) Known() bool { return !in.At.IsZero() }

func (in TabInfo) Sums() (add, del, ahead, behind int) {
	for _, w := range in.Wts {
		add += w.Add
		del += w.Del
		ahead = max(ahead, w.Stats.Ahead)
		behind = max(behind, w.Stats.Behind)
	}
	return
}

// CanPush is whether p has commits to push: some branch has commits on no
// remote. Before the branches are looked at, it is not known not to.
func (in TabInfo) CanPush() bool {
	if !in.Known() {
		return true
	}
	for _, w := range in.Wts {
		if w.Err == "" && w.Unpushed > 0 {
			return true
		}
	}
	return false
}

// CanCommit is whether c has changes to commit. Before the branches are
// looked at, it is not known not to.
func (in TabInfo) CanCommit() bool {
	if !in.Known() {
		return true
	}
	for _, w := range in.Wts {
		if w.Uncommitted > 0 {
			return true
		}
	}
	return false
}

// CanOpenPR is whether c has a pull request to open: some branch has
// commits ahead of its base and no open PR yet.
func (in TabInfo) CanOpenPR() bool {
	if !in.Known() {
		return true
	}
	for _, w := range in.Wts {
		if w.Err == "" && w.Stats.Ahead > 0 && !(w.HasPR && w.Pr.State == "OPEN") {
			return true
		}
	}
	return false
}

// Ci is the worst CI state of the tab's pull requests.
func (in TabInfo) Ci() string {
	rank := map[string]int{"": 0, "pass": 1, "pending": 2, "fail": 3}
	best := ""
	for _, w := range in.Wts {
		if w.HasPR && rank[w.Pr.CI] > rank[best] {
			best = w.Pr.CI
		}
	}
	return best
}

// Prs are the tab's pull requests.
func (in TabInfo) Prs() []github.Status {
	var out []github.Status
	for _, w := range in.Wts {
		if w.HasPR {
			out = append(out, w.Pr)
		}
	}
	return out
}

// Merged reports whether every worktree of the tab has a merged pull
// request: its work is safely in main. A branch that went on after its
// pull request was merged is not: the commits after it are in no pull
// request, and closing the tab for a merged one could delete them.
func (in TabInfo) Merged() bool {
	if len(in.Wts) == 0 {
		return false
	}
	for _, w := range in.Wts {
		if !w.HasPR || w.Pr.State != "MERGED" || w.Pr.After > 0 {
			return false
		}
	}
	return true
}

// LookAgain asks for a fresh look at t's branches, and with pr at its
// pull requests too, at the next tick. A look already under way does not
// answer it: it started before whatever made this one wanted — a commit,
// a push — and what it found is from before that too.
func (t *Tab) LookAgain(pr bool) {
	t.Info.Stale = true
	t.InfoAsks++
	if pr {
		t.Info.StalePR = true
		t.PrAsks++
	}
}

// RefreshInfo starts a look at the tab that most wants one, if any. One at
// a time: the look runs git in every worktree of the tab, and a slow
// repository must not pile them up.
func (m *Core) RefreshInfo() Cmd {
	if m.InfoBusy {
		return nil
	}
	now := time.Now()
	var pick *Tab
	for _, t := range m.Tabs {
		every, _ := m.InfoEvery(t)
		due := t.Info.Stale || now.Sub(t.Info.At) > every
		if !due {
			continue
		}
		// The tab in front first, then the one waited on longest.
		if pick == nil || t == m.Front() || (pick != m.Front() && t.Info.At.Before(pick.Info.At)) {
			pick = t
		}
	}
	if pick == nil {
		return nil
	}
	m.InfoBusy = true
	prev := pick.Info
	_, prEvery := m.InfoEvery(pick)
	withPR := prev.StalePR || now.Sub(prev.PrAt) > prEvery
	skip, watchN := m.MaySkip(pick, now)
	id, wts := pick.ID, append([]workspace.Worktree(nil), pick.Worktrees...)
	asks, prAsks := pick.InfoAsks, pick.PrAsks
	return func() Event {
		in := LookAt(wts, prev, withPR, skip)
		in.WatchN = watchN
		return InfoMsg{Id: id, Info: in, Asks: asks, PrAsks: prAsks}
	}
}

// MaySkip reports whether a look at t can keep what the last one found
// when its git files are as they were then: nothing asked for a fresh
// look, the last full one is recent, and no file was edited since — as
// far as can be told: the review panel's watch sees edits in the worktree
// it shows; in another, only an agent that is not working is taken not to
// edit. watchN is the watch's count of changes to keep with the look.
func (m *Core) MaySkip(t *Tab, now time.Time) (skip bool, watchN int) {
	prev := t.Info
	watchN = prev.WatchN
	if !prev.Known() || prev.Stale || now.Sub(prev.FullAt) >= FullInfoEvery {
		return false, watchN
	}
	var wt string
	n := 0
	if m.Client != nil {
		s := m.Client.Sight()
		wt, n = s.WatchWt, s.WatchN
	}
	for _, w := range t.Worktrees {
		if wt != "" && filepath.Clean(w.Path) == filepath.Clean(wt) {
			return n == prev.WatchN && (len(t.Worktrees) == 1 || t.Light != StWorking), n
		}
	}
	return t.Light != StWorking, watchN
}

// InfoEvery is how often t's branches, and its pull requests, are looked
// at: often while it is on screen or its agent works, seldom otherwise.
func (m *Core) InfoEvery(t *Tab) (branches, prs time.Duration) {
	if m.Client != nil && m.Client.TabShown(m.Ref(t)) || t.Light == StWorking {
		return InfoEvery, m.GithubEvery(PrEvery)
	}
	return IdleInfoEvery, m.GithubEvery(IdlePREvery)
}

// FileKey is a file of repository Repo, its main checkout.
type FileKey struct{ Repo, File string }

// SetInfo takes a finished look and works out the overlaps again.
func (m *Core) SetInfo(msg InfoMsg) {
	m.InfoBusy = false
	for _, t := range m.Tabs {
		if t.ID == msg.Id {
			if len(msg.Info.Wts) != len(t.Worktrees) {
				t.Info.Stale = true // the tab changed while it was looked at
				return
			}
			merged := t.Info.Merged()
			t.Info = msg.Info
			t.Info.Stale = t.InfoAsks != msg.Asks
			t.Info.StalePR = t.PrAsks != msg.PrAsks
			m.FollowBranches(t)
			if t.Info.Merged() != merged {
				m.Tell(PanesMsg{}) // the panel says so, or no longer does
			}
		}
	}
	m.FindOverlaps()
}

// FindOverlaps records, for every tab, the other tabs that change some of
// the same files in the same repository.
func (m *Core) FindOverlaps() {
	type key struct{ repo, file string }
	who := map[key][]*Tab{}
	for _, t := range m.Tabs {
		for _, w := range t.Info.Wts {
			for _, f := range w.Stats.Changed {
				k := key{w.Repo, f}
				who[k] = append(who[k], t)
			}
		}
	}
	m.Overlaps = map[string][]Overlap{}
	m.Shared = map[string]map[FileKey][]string{}
	byPair := map[[2]string]*Overlap{}
	for k, ts := range who {
		if len(ts) < 2 {
			continue
		}
		for _, a := range ts {
			if m.Shared[a.ID] == nil {
				m.Shared[a.ID] = map[FileKey][]string{}
			}
			for _, b := range ts {
				if a != b {
					key := FileKey{Repo: k.repo, File: k.file}
					m.Shared[a.ID][key] = append(m.Shared[a.ID][key], b.Name)
				}
			}
			for _, b := range ts {
				if a == b {
					continue
				}
				pk := [2]string{a.ID, b.ID}
				o := byPair[pk]
				if o == nil {
					o = &Overlap{With: b}
					byPair[pk] = o
				}
				o.Files = append(o.Files, path.Join(BaseName(k.repo), k.file))
			}
		}
	}
	for pk, o := range byPair {
		sort.Strings(o.Files)
		m.Overlaps[pk[0]] = append(m.Overlaps[pk[0]], *o)
	}
	for _, list := range m.Overlaps {
		sort.Slice(list, func(i, j int) bool { return list[i].With.Name < list[j].With.Name })
	}
	m.Tell(OverlapsMsg{})
}
