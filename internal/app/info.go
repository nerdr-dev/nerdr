package app

import (
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"nerdr/internal/config"
	"nerdr/internal/git"
	"nerdr/internal/github"
	"nerdr/internal/review"
	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Each tab's branches are looked at in the background: how much they
// change, how far behind main they are, their pull request and its CI, and
// which files they change — so the tab bar can say where each tab stands,
// and warn when two tabs change the same files and are heading for a merge
// conflict.

const (
	// infoEvery is how often a tab's branches are looked at again, and
	// prEvery how often its pull request, which asks GitHub: the tab on
	// screen, and one whose agent is working. The intervals of asking
	// GitHub are at the default github_refresh; see githubEvery.
	infoEvery = 20 * time.Second
	prEvery   = 90 * time.Second
	// The others change little but by their agent, whose turn ending has
	// them looked at anyway: a look is eight git commands a worktree, and
	// many tabs looked at as often kept git running all the time.
	idleInfoEvery = 5 * time.Minute
	idlePREvery   = 10 * time.Minute
	infoTickN     = 2 * time.Second
	// A look finds out first, without git, whether the branches can have
	// changed (see git.Stamp and maySkip), and when not, keeps what it
	// found last: a tab on screen with nothing going on runs no git. It
	// runs git anyway after fullInfoEvery, for what that does not see.
	fullInfoEvery = 2 * time.Minute
	// prPanelEvery is how often F2 reads the pull request it shows again.
	prPanelEvery = 2 * time.Minute
)

// wtInfo is what was found about one of a tab's worktrees.
type wtInfo struct {
	repo string
	// branch is the one checked out now, which need not be the one the tab
	// was made on: switching branch in the tab's shell, or by the agent,
	// leaves the same worktree on another branch. "" when detached.
	branch   string
	stats    workspace.Stats
	add, del int
	unpushed int // commits on no remote, which p would push
	// uncommitted is how many files have changes not committed, which c
	// would commit.
	uncommitted int
	pr          github.Status
	hasPR       bool
	err         string
}

// tabInfo is what was found about a tab.
type tabInfo struct {
	wts  []wtInfo
	at   time.Time // when the branches were last looked at
	prAt time.Time // when the pull requests were
	// stale asks for a look at the next tick, e.g. when the agent has just
	// finished something.
	stale   bool
	stalePR bool
	// fullAt is when git was last run for the branches, stamp what their
	// git files looked like then, and watchN how many changes the review
	// panel's watch had reported then; see maySkip.
	fullAt time.Time
	stamp  string
	watchN int
}

func (in tabInfo) known() bool { return !in.at.IsZero() }

func (in tabInfo) sums() (add, del, ahead, behind int) {
	for _, w := range in.wts {
		add += w.add
		del += w.del
		ahead = max(ahead, w.stats.Ahead)
		behind = max(behind, w.stats.Behind)
	}
	return
}

// canPush is whether p has commits to push: some branch has commits on no
// remote. Before the branches are looked at, it is not known not to.
func (in tabInfo) canPush() bool {
	if !in.known() {
		return true
	}
	for _, w := range in.wts {
		if w.err == "" && w.unpushed > 0 {
			return true
		}
	}
	return false
}

// canCommit is whether c has changes to commit. Before the branches are
// looked at, it is not known not to.
func (in tabInfo) canCommit() bool {
	if !in.known() {
		return true
	}
	for _, w := range in.wts {
		if w.uncommitted > 0 {
			return true
		}
	}
	return false
}

// canOpenPR is whether c has a pull request to open: some branch has
// commits ahead of its base and no open PR yet.
func (in tabInfo) canOpenPR() bool {
	if !in.known() {
		return true
	}
	for _, w := range in.wts {
		if w.err == "" && w.stats.Ahead > 0 && !(w.hasPR && w.pr.State == "OPEN") {
			return true
		}
	}
	return false
}

// ci is the worst CI state of the tab's pull requests.
func (in tabInfo) ci() string {
	rank := map[string]int{"": 0, "pass": 1, "pending": 2, "fail": 3}
	best := ""
	for _, w := range in.wts {
		if w.hasPR && rank[w.pr.CI] > rank[best] {
			best = w.pr.CI
		}
	}
	return best
}

// prs are the tab's pull requests.
func (in tabInfo) prs() []github.Status {
	var out []github.Status
	for _, w := range in.wts {
		if w.hasPR {
			out = append(out, w.pr)
		}
	}
	return out
}

// merged reports whether every worktree of the tab has a merged pull
// request: its work is safely in main.
func (in tabInfo) merged() bool {
	if len(in.wts) == 0 {
		return false
	}
	for _, w := range in.wts {
		if !w.hasPR || w.pr.State != "MERGED" {
			return false
		}
	}
	return true
}

type infoTickMsg struct{}

type infoMsg struct {
	id   string
	info tabInfo
	// asks and prAsks are the tab's counts of looks asked for when this
	// one started; see lookAgain.
	asks, prAsks int
}

// lookAgain asks for a fresh look at t's branches, and with pr at its
// pull requests too, at the next tick. A look already under way does not
// answer it: it started before whatever made this one wanted — a commit,
// a push — and what it found is from before that too.
func (t *tab) lookAgain(pr bool) {
	t.info.stale = true
	t.infoAsks++
	if pr {
		t.info.stalePR = true
		t.prAsks++
	}
}

func infoTick() tea.Cmd {
	return tea.Tick(infoTickN, func(time.Time) tea.Msg { return infoTickMsg{} })
}

// refreshInfo starts a look at the tab that most wants one, if any. One at
// a time: the look runs git in every worktree of the tab, and a slow
// repository must not pile them up.
func (m *Model) refreshInfo() tea.Cmd {
	if m.infoBusy {
		return nil
	}
	now := time.Now()
	var pick *tab
	for _, t := range m.tabs {
		every, _ := m.infoEvery(t)
		due := t.info.stale || now.Sub(t.info.at) > every
		if !due {
			continue
		}
		// The active tab first, then the one waited on longest.
		if pick == nil || t == m.cur() || (pick != m.cur() && t.info.at.Before(pick.info.at)) {
			pick = t
		}
	}
	if pick == nil {
		return nil
	}
	m.infoBusy = true
	prev := pick.info
	_, prEvery := m.infoEvery(pick)
	withPR := prev.stalePR || now.Sub(prev.prAt) > prEvery
	skip, watchN := m.maySkip(pick, now)
	id, wts := pick.ID, append([]workspace.Worktree(nil), pick.Worktrees...)
	asks, prAsks := pick.infoAsks, pick.prAsks
	return func() tea.Msg {
		in := lookAt(wts, prev, withPR, skip)
		in.watchN = watchN
		return infoMsg{id: id, info: in, asks: asks, prAsks: prAsks}
	}
}

// maySkip reports whether a look at t can keep what the last one found
// when its git files are as they were then: nothing asked for a fresh
// look, the last full one is recent, and no file was edited since — as
// far as can be told: the review panel's watch sees edits in the worktree
// it shows; in another, only an agent that is not working is taken not to
// edit. watchN is the watch's count of changes to keep with the look.
func (m *Model) maySkip(t *tab, now time.Time) (skip bool, watchN int) {
	prev := t.info
	watchN = prev.watchN
	if !prev.known() || prev.stale || now.Sub(prev.fullAt) >= fullInfoEvery {
		return false, watchN
	}
	wt, n := m.review.WatchChanges()
	for _, w := range t.Worktrees {
		if wt != "" && filepath.Clean(w.Path) == filepath.Clean(wt) {
			return n == prev.watchN && (len(t.Worktrees) == 1 || t.status != stWorking), n
		}
	}
	return t.status != stWorking, watchN
}

// infoEvery is how often t's branches, and its pull requests, are looked
// at: often while it is on screen or its agent works, seldom otherwise.
func (m *Model) infoEvery(t *tab) (branches, prs time.Duration) {
	if t == m.cur() || (t == m.splitTab() && m.splitShown()) || t.status == stWorking {
		return infoEvery, m.githubEvery(prEvery)
	}
	return idleInfoEvery, m.githubEvery(idlePREvery)
}

// githubEvery is d, an interval of asking GitHub written for the default
// github_refresh, made as much longer or shorter as the config's is.
func (m *Model) githubEvery(d time.Duration) time.Duration {
	n := m.cfg.GitHubRefresh
	if n <= 0 {
		return d
	}
	return d * time.Duration(n) / config.DefaultGitHubRefresh
}

// lookAt finds out about wts; the pull requests only when withPR is set,
// else they are carried over from prev. With skip, the branches are only
// looked at with git when their git files changed since prev.
func lookAt(wts []workspace.Worktree, prev tabInfo, withPR, skip bool) tabInfo {
	in := tabInfo{at: time.Now(), prAt: prev.prAt}
	if withPR {
		in.prAt = in.at
	}
	var stamps []string
	for _, w := range wts {
		stamps = append(stamps, git.Stamp(w.Path))
	}
	in.stamp = strings.Join(stamps, "\x00")
	if skip && !slices.Contains(stamps, "") && in.stamp == prev.stamp && len(prev.wts) == len(wts) {
		in.fullAt = prev.fullAt
		in.wts = slices.Clone(prev.wts)
		if withPR && github.Available() {
			for i, w := range wts {
				in.wts[i].pr, in.wts[i].hasPR = github.BranchStatus(w.Path)
			}
		}
		return in
	}
	in.fullAt = in.at
	for i, w := range wts {
		wi := wtInfo{repo: w.Repo, branch: git.CurrentBranch(w.Path)}
		if wi.branch != "" {
			w.Branch = wi.branch
		}
		st, err := workspace.BranchStats(w)
		wi.stats = st
		if err != nil {
			wi.err = err.Error()
		} else {
			_, wi.add, wi.del = git.ShortStatBase(w.Path, st.Base)
			wi.unpushed = workspace.UnpushedCount(w)
		}
		wi.uncommitted = workspace.Uncommitted(w)
		// Another branch has another pull request: the old one's is not
		// carried over to it.
		moved := wi.branch != "" && i < len(prev.wts) && prev.wts[i].branch != "" && prev.wts[i].branch != wi.branch
		switch {
		case (withPR || moved) && github.Available():
			wi.pr, wi.hasPR = github.BranchStatus(w.Path)
		case i < len(prev.wts):
			wi.pr, wi.hasPR = prev.wts[i].pr, prev.wts[i].hasPR
		}
		in.wts = append(in.wts, wi)
	}
	return in
}

// setInfo takes a finished look and works out the overlaps again.
func (m *Model) setInfo(msg infoMsg) {
	m.infoBusy = false
	for _, t := range m.tabs {
		if t.ID == msg.id {
			if len(msg.info.wts) != len(t.Worktrees) {
				t.info.stale = true // the tab changed while it was looked at
				return
			}
			merged := t.info.merged()
			t.info = msg.info
			t.info.stale = t.infoAsks != msg.asks
			t.info.stalePR = t.prAsks != msg.prAsks
			m.followBranches(t)
			if t.info.merged() != merged {
				m.resizePanes() // the panel says so, or no longer does
			}
		}
	}
	m.findOverlaps()
}

// followBranches takes in the branches a tab's worktrees are on now. A tab
// still named after the branch it was made on is renamed with it; one
// renamed by the user keeps its name.
func (m *Model) followBranches(t *tab) {
	changed := false
	for i, wi := range t.info.wts {
		if i >= len(t.Worktrees) || wi.branch == "" || wi.branch == t.Worktrees[i].Branch {
			continue
		}
		if i == 0 && t.Name == t.Worktrees[0].Branch {
			t.Name = wi.branch
		}
		t.Worktrees[i].Branch = wi.branch
		changed = true
	}
	if changed {
		m.saveSession()
	}
}

// findOverlaps records, for every tab, the other tabs that change some of
// the same files in the same repository.
func (m *Model) findOverlaps() {
	type key struct{ repo, file string }
	who := map[key][]*tab{}
	for _, t := range m.tabs {
		for _, w := range t.info.wts {
			for _, f := range w.stats.Changed {
				k := key{w.repo, f}
				who[k] = append(who[k], t)
			}
		}
	}
	m.overlaps = map[string][]overlap{}
	m.shared = map[string]map[string][]string{}
	byPair := map[[2]string]*overlap{}
	for k, ts := range who {
		if len(ts) < 2 {
			continue
		}
		for _, a := range ts {
			if m.shared[a.ID] == nil {
				m.shared[a.ID] = map[string][]string{}
			}
			for _, b := range ts {
				if a != b {
					key := review.SharedKey(k.repo, k.file)
					m.shared[a.ID][key] = append(m.shared[a.ID][key], b.Name)
				}
			}
			for _, b := range ts {
				if a == b {
					continue
				}
				pk := [2]string{a.ID, b.ID}
				o := byPair[pk]
				if o == nil {
					o = &overlap{with: b}
					byPair[pk] = o
				}
				o.files = append(o.files, path.Join(baseName(k.repo), k.file))
			}
		}
	}
	for pk, o := range byPair {
		sort.Strings(o.files)
		m.overlaps[pk[0]] = append(m.overlaps[pk[0]], *o)
	}
	for _, list := range m.overlaps {
		sort.Slice(list, func(i, j int) bool { return list[i].with.Name < list[j].with.Name })
	}
	m.shareOverlaps()
}

// overlap is another tab that changes some of the same files.
type overlap struct {
	with  *tab
	files []string
}

// branches are the other tab's branches in the repositories the files are
// in.
func (o overlap) branches() []string {
	repos := map[string]bool{}
	for _, f := range o.files {
		repo, _, _ := strings.Cut(f, "/")
		repos[repo] = true
	}
	var out []string
	for i, w := range o.with.Worktrees {
		b := w.Branch
		if i < len(o.with.info.wts) && o.with.info.wts[i].branch != "" {
			b = o.with.info.wts[i].branch // checked out now
		}
		if repos[baseName(w.Repo)] && b != "" && !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		out = append(out, o.with.Name)
	}
	return out
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// details is the short state of a tab for the tab bar, e.g.
// "+142−37 ↓3 #12✗ ⚠ ✉1". The lines added and deleted are for the active
// tab only: on every tab they make the row long, and say little about who
// needs you.
func (m *Model) details(t *tab, bg lipgloss.Color, active bool) string {
	st := func(c string) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(c)) }
	var parts []string
	if n := len(t.panes()) - 1; n > 0 {
		parts = append(parts, m.tip(st("114").Render("⧉"+strconv.Itoa(n)), plural(n, "pane")+" beside the agent: terminals, agents, reviews"))
	}
	if t.info.known() {
		add, del, _, behind := t.info.sums()
		if active && add+del > 0 {
			parts = append(parts, m.tip(st("71").Render("+"+strconv.Itoa(add))+st("167").Render("−"+strconv.Itoa(del)),
				"Lines added and removed on the branch"))
		}
		if behind > 0 {
			parts = append(parts, m.tip(st("179").Render("↓"+strconv.Itoa(behind)),
				plural(behind, "commit")+" on the base branch not in this one"))
		}
		if t.info.merged() {
			parts = append(parts, m.tip(st("141").Render("✓merged"), "The branch is merged"))
		} else if prs := t.info.prs(); len(prs) > 0 {
			s := "#" + strconv.Itoa(prs[0].Number)
			tip := "Pull request #" + strconv.Itoa(prs[0].Number)
			if prs[0].State == "CLOSED" {
				s += " closed"
				tip += ", closed"
			}
			s = st("110").Render(s)
			switch t.info.ci() {
			case "pass":
				s += st("71").Render("✓")
				tip += " · checks passed"
			case "fail":
				s += st("196").Render("✗")
				tip += " · checks failed"
			case "pending":
				s += st("220").Render("…")
				tip += " · checks running"
			}
			parts = append(parts, m.tip(s, tip))
		}
	}
	if len(m.overlaps[t.ID]) > 0 {
		parts = append(parts, m.tip(st("214").Render("⚠"), "Other tabs change some of the same files"))
	}
	if n := len(t.Messages); n > 0 {
		s := "✉" + strconv.Itoa(n)
		tip := plural(n, "task") + " waiting to be sent to the agent"
		if t.queuePaused {
			s += "⏸"
			tip += " · paused: the agent did not take the last one up"
		}
		parts = append(parts, m.tip(st("81").Render(s), tip))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, st("240").Render(" "))
}
