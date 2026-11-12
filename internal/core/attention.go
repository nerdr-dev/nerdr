package core

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// summaryCommits is how many new commits the summary lists by name.
const SummaryCommits = 5

// headsMsg carries the commits a tab's worktrees were at when it was left.
type HeadsMsg struct {
	T     *Tab
	At    time.Time
	Heads []string
}

// leave records how tab t looks as you leave it.
func (m *Core) Leave(t *Tab, now time.Time) Cmd {
	v := &Visit{At: now, Known: t.Info.Known(), Files: ChangedFiles(t.Info), Ci: t.Info.Ci(), Pr: PrLabel(t.Info)}
	v.Add, v.Del, _, _ = t.Info.Sums()
	t.Visit, t.Away = v, AwayLog{}
	t.LastActive = now
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	return func() Event {
		heads := make([]string, len(wts))
		for i, w := range wts {
			heads[i] = workspace.Head(w)
		}
		return HeadsMsg{T: t, At: now, Heads: heads}
	}
}

// setHeads files the commits found for a visit, if it is still the last.
func (m *Core) SetHeads(msg HeadsMsg) {
	if v := msg.T.Visit; v != nil && v.At.Equal(msg.At) {
		v.Heads = msg.Heads
	}
}

// enter sums up what happened in t since it was last left; the commits
// are looked up in the background and the summary follows as summaryMsg.
func (m *Core) Enter(t *Tab, now time.Time) Cmd {
	t.LastActive = now
	v := t.Visit
	if v == nil {
		return nil
	}
	t.Visit = nil
	s := Summary{
		T: t, Since: v.At, Away: now.Sub(v.At), Log: t.Away,
		Finished: t.Light == StDone, Blocked: t.Light == StBlocked,
		Known: v.Known && t.Info.Known(), WasAdd: v.Add, WasDel: v.Del, WasCI: v.Ci, WasPR: v.Pr,
		Ci: t.Info.Ci(), Pr: PrLabel(t.Info),
	}
	s.Add, s.Del, _, _ = t.Info.Sums()
	for f := range ChangedFiles(t.Info) {
		if !v.Files[f] {
			s.NewFiles = append(s.NewFiles, f)
		}
	}
	sort.Strings(s.NewFiles)
	heads := v.Heads
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	return func() Event {
		for i, w := range wts {
			if i < len(heads) {
				subj, n := workspace.CommitsSince(w, heads[i], SummaryCommits)
				s.Commits = append(s.Commits, subj...)
				s.NCommits += n
			}
		}
		return SummaryMsg{s}
	}
}

// changedFiles are the files the tab's branches change, as repo/file.
func ChangedFiles(in TabInfo) map[string]bool {
	out := map[string]bool{}
	for _, w := range in.Wts {
		for _, f := range w.Stats.Changed {
			out[BaseName(w.Repo)+"/"+f] = true
		}
	}
	return out
}

// prLabel is the state of the tab's first pull request, e.g. "#12 open".
func PrLabel(in TabInfo) string {
	prs := in.Prs()
	if len(prs) == 0 {
		return ""
	}
	if in.Merged() {
		return "#" + strconv.Itoa(prs[0].Number) + " merged"
	}
	return "#" + strconv.Itoa(prs[0].Number) + " " + PrState(prs[0])
}

type SummaryMsg struct{ S Summary }

// onSummary keeps the summary for H.
func (m *Core) OnSummary(s Summary) {
	if m.TabIndex(s.T) < 0 {
		return
	}
	s.T.Summary = &s
}

func OrNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// duration says how long d is, roughly: "40s", "12m", "3h 5m", "2d 4h".
func Duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h " + strconv.Itoa(int(d.Minutes())%60) + "m"
	}
	return strconv.Itoa(int(d.Hours())/24) + "d " + strconv.Itoa(int(d.Hours())%24) + "h"
}

// Waiting are the agents that need you, a tab's own or another in one of
// its panes: those asking something first, then those that have finished,
// each the longest waiting first.
func (l TabList) Waiting() []*Tab {
	var out []*Tab
	for _, o := range l {
		for _, t := range append([]*Tab{o}, o.ExtraAgents()...) {
			if t.Light == StBlocked || t.Light == StDone {
				out = append(out, t)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Light == StBlocked, out[j].Light == StBlocked; a != b {
			return a
		}
		return out[i].Changed.Before(out[j].Changed)
	})
	return out
}

// activeEvery is how far LastActive may lag behind an agent at work. It is
// saved, and sent to a client: kept to the moment, the tab of every agent
// at work would be sent again at each look at the agents.
const activeEvery = 15 * time.Second

// active notes that t's agent works at now.
func (t *Tab) active(now time.Time) {
	if now.Sub(t.LastActive) >= activeEvery {
		t.LastActive = now
	}
}

// cold reports whether t has gone cold: neither its agent nor you have
// done anything in it for cold_days.
func (m *Core) Cold(t *Tab, now time.Time) bool {
	days := IntOr0(m.Cfg.ColdDays)
	if days == 0 || t.LastActive.IsZero() {
		return false
	}
	switch t.Light {
	case StWorking, StBlocked, StDone:
		return false
	}
	return now.Sub(t.LastActive) >= time.Duration(days)*24*time.Hour
}

func (m *Core) ColdTabs() []*Tab {
	now := time.Now()
	var out []*Tab
	for _, t := range m.Tabs {
		if m.Cold(t, now) {
			out = append(out, t)
		}
	}
	return out
}

// CountStatus is how many tabs have status s.
func (l TabList) CountStatus(s Status) int {
	n := 0
	for _, t := range l {
		if t.Lit() == s {
			n++
		}
	}
	return n
}

// screenTail is what the tab's agent last said: its integration's message
// when it gave one, else the last lines on its screen with text.
func ScreenTail(t *Tab, n int) []string {
	if msg := strings.TrimSpace(t.Hook.Msg); msg != "" {
		return strings.Split(msg, "\n")
	}
	if t.Pane == nil {
		return nil
	}
	var out []string
	rows := strings.Split(t.Pane.Snapshot().Screen, "\n")
	for i := len(rows) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(rows[i]) != "" {
			out = append([]string{rows[i]}, out...)
		}
	}
	return out
}

// tabLimitReached reports whether max_tabs are open.
func (m *Core) TabLimitReached() bool {
	n := IntOr0(m.Cfg.MaxTabs)
	return n > 0 && len(m.Tabs) >= n
}

// isOn and intOr0 read a setting that config.Load always fills in, and a
// Model built without it (in tests) leaves nil: off, and 0.
func IsOn(b *bool) bool { return b != nil && *b }

func IntOr0(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// Visit is how a tab looked when you last left it.
type Visit struct {
	At    time.Time
	Heads []string // per worktree; filled in in the background
	Known bool     // whether the tab's info was known then
	Add   int
	Del   int
	Files map[string]bool
	Ci    string
	Pr    string
}

// AwayLog counts what a tab's agent did while the tab was not on screen.
type AwayLog struct {
	Done, Blocked, Sent int
}

// Summary is what happened in a tab while you were elsewhere.
type Summary struct {
	T     *Tab
	Since time.Time
	Away  time.Duration
	Log   AwayLog
	// Finished: the agent was done and waiting to be looked at when you
	// came back; blocked: it asked something then. How it is now is read
	// from the tab, not kept here.
	Finished, Blocked bool

	Commits  []string
	NCommits int

	Known          bool // the before and after of the changes are both known
	Add, Del       int
	WasAdd, WasDel int
	NewFiles       []string
	Ci, WasCI      string
	Pr, WasPR      string
}

// GoalText is what the tab is about, for the inbox: your note, or else
// its task.
func (t *Tab) GoalText() (label, text string) {
	if t == nil {
		return "", ""
	}
	if n := strings.TrimSpace(t.Note); n != "" {
		return "note", n
	}
	return "task", strings.TrimSpace(t.Goal)
}
