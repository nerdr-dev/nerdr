package app

import (
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Each tab's branches are looked at in the background: how much they
// change, how far behind main they are, their pull request and its CI, and
// which files they change — so the tab bar can say where each tab stands,
// and warn when two tabs change the same files and are heading for a merge
// conflict.

// refreshInfo starts a look at the tab that most wants one, if any. One at
// a time: the look runs git in every worktree of the tab, and a slow
// repository must not pile them up.
func (m *Model) refreshInfo() tea.Cmd {
	if m.infoBusy {
		return nil
	}
	now := time.Now()
	var pick *core.Tab
	for _, t := range m.Tabs {
		every, _ := m.infoEvery(t)
		due := t.Info.Stale || now.Sub(t.Info.At) > every
		if !due {
			continue
		}
		// The active tab first, then the one waited on longest.
		if pick == nil || t == m.Cur() || (pick != m.Cur() && t.Info.At.Before(pick.Info.At)) {
			pick = t
		}
	}
	if pick == nil {
		return nil
	}
	m.infoBusy = true
	prev := pick.Info
	_, prEvery := m.infoEvery(pick)
	withPR := prev.StalePR || now.Sub(prev.PrAt) > prEvery
	skip, watchN := m.maySkip(pick, now)
	id, wts := pick.ID, append([]workspace.Worktree(nil), pick.Worktrees...)
	asks, prAsks := pick.InfoAsks, pick.PrAsks
	return func() tea.Msg {
		in := core.LookAt(wts, prev, withPR, skip)
		in.WatchN = watchN
		return core.InfoMsg{Id: id, Info: in, Asks: asks, PrAsks: prAsks}
	}
}

// maySkip reports whether a look at t can keep what the last one found
// when its git files are as they were then: nothing asked for a fresh
// look, the last full one is recent, and no file was edited since — as
// far as can be told: the review panel's watch sees edits in the worktree
// it shows; in another, only an agent that is not working is taken not to
// edit. watchN is the watch's count of changes to keep with the look.
func (m *Model) maySkip(t *core.Tab, now time.Time) (skip bool, watchN int) {
	prev := t.Info
	watchN = prev.WatchN
	if !prev.Known() || prev.Stale || now.Sub(prev.FullAt) >= core.FullInfoEvery {
		return false, watchN
	}
	wt, n := m.review.WatchChanges()
	for _, w := range t.Worktrees {
		if wt != "" && filepath.Clean(w.Path) == filepath.Clean(wt) {
			return n == prev.WatchN && (len(t.Worktrees) == 1 || t.Light != core.StWorking), n
		}
	}
	return t.Light != core.StWorking, watchN
}

// infoEvery is how often t's branches, and its pull requests, are looked
// at: often while it is on screen or its agent works, seldom otherwise.
func (m *Model) infoEvery(t *core.Tab) (branches, prs time.Duration) {
	if t == m.Cur() || (t == m.splitTab() && m.splitShown()) || t.Light == core.StWorking {
		return core.InfoEvery, m.GithubEvery(core.PrEvery)
	}
	return core.IdleInfoEvery, m.GithubEvery(core.IdlePREvery)
}

// setInfo takes a finished look and works out the overlaps again.
func (m *Model) setInfo(msg core.InfoMsg) {
	m.infoBusy = false
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
				m.resizePanes() // the panel says so, or no longer does
			}
		}
	}
	m.findOverlaps()
}

// findOverlaps records, for every tab, the other tabs that change some of
// the same files in the same repository.
func (m *Model) findOverlaps() {
	type key struct{ repo, file string }
	who := map[key][]*core.Tab{}
	for _, t := range m.Tabs {
		for _, w := range t.Info.Wts {
			for _, f := range w.Stats.Changed {
				k := key{w.Repo, f}
				who[k] = append(who[k], t)
			}
		}
	}
	m.overlaps = map[string][]core.Overlap{}
	m.shared = map[string]map[string][]string{}
	byPair := map[[2]string]*core.Overlap{}
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
					o = &core.Overlap{With: b}
					byPair[pk] = o
				}
				o.Files = append(o.Files, path.Join(core.BaseName(k.repo), k.file))
			}
		}
	}
	for pk, o := range byPair {
		sort.Strings(o.Files)
		m.overlaps[pk[0]] = append(m.overlaps[pk[0]], *o)
	}
	for _, list := range m.overlaps {
		sort.Slice(list, func(i, j int) bool { return list[i].With.Name < list[j].With.Name })
	}
	m.shareOverlaps()
}

// details is the short state of a tab for the tab bar, e.g.
// "+142−37 ↓3 #12✗ ⚠ ✉1". The lines added and deleted are for the active
// tab only: on every tab they make the row long, and say little about who
// needs you.
func (m *Model) details(t *core.Tab, bg lipgloss.Color, active bool) string {
	st := func(c string) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(c)) }
	var parts []string
	if n := len(t.Panes()) - 1; n > 0 {
		parts = append(parts, m.tip(st("114").Render("⧉"+strconv.Itoa(n)), core.Plural(n, "pane")+" beside the agent: terminals, agents, reviews"))
	}
	if t.Info.Known() {
		add, del, _, behind := t.Info.Sums()
		if active && add+del > 0 {
			parts = append(parts, m.tip(st("71").Render("+"+strconv.Itoa(add))+st("167").Render("−"+strconv.Itoa(del)),
				"Lines added and removed on the branch"))
		}
		if behind > 0 {
			parts = append(parts, m.tip(st("179").Render("↓"+strconv.Itoa(behind)),
				core.Plural(behind, "commit")+" on the base branch not in this one"))
		}
		if t.Info.Merged() {
			parts = append(parts, m.tip(st("141").Render("✓merged"), "The branch is merged"))
		} else if prs := t.Info.Prs(); len(prs) > 0 {
			s := "#" + strconv.Itoa(prs[0].Number)
			tip := "Pull request #" + strconv.Itoa(prs[0].Number)
			switch {
			case prs[0].After > 0:
				// The branch went on after it: what is on top is in no
				// pull request.
				s += " " + strings.ToLower(prs[0].State) + "·" + strconv.Itoa(prs[0].After) + " after"
				tip += ", " + strings.ToLower(prs[0].State) + " · " + core.Plural(prs[0].After, "commit") +
					" after it, in no pull request"
			case prs[0].State == "CLOSED":
				s += " closed"
				tip += ", closed"
			}
			s = st("110").Render(s)
			switch t.Info.Ci() {
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
			if active {
				// The active tab's, as a link: on another tab a click
				// goes to the tab. The other buttons do what they do on
				// the tab.
				tip += " · click to open it in the browser"
				s = m.zone(s, func(m *Model, ev tea.MouseMsg) tea.Cmd {
					if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
						m.openTabPRs()
						return nil
					}
					return m.tabMouse(t, ev)
				})
			}
			parts = append(parts, m.tip(s, tip))
		}
	}
	if len(m.overlaps[t.ID]) > 0 {
		parts = append(parts, m.tip(st("214").Render("⚠"), "Other tabs change some of the same files"))
	}
	if n := len(t.TaskRefs()); n > 0 {
		s := "✉" + strconv.Itoa(n)
		tip := core.Plural(n, "task") + " waiting to be sent to the agent"
		if len(t.ExtraAgents()) > 0 {
			tip = core.Plural(n, "task") + " waiting to be sent to the tab's agents"
		}
		if gone := len(t.Inactive); gone > 0 {
			tip += " · " + strconv.Itoa(gone) + " for a closed agent"
		}
		if t.QueuePaused {
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
