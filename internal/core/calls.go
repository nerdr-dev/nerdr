package core

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// allCalls is the name of the row that sums the others.
const AllCalls = "all"

// callRow is one tab's calls, or those in no tab's worktree.
type CallRow struct {
	Name            string
	Git, Gh, Others int
	Cmds            map[string]int // by program and what it was asked, "git status"
}

func (r CallRow) Total() int { return r.Git + r.Gh + r.Others }

// callRows sums calls by the tab whose worktree each ran in.
func (m *Core) CallRows(calls map[proc.Call]int) []CallRow {
	byTab := map[*Tab]*CallRow{}
	elsewhere := &CallRow{Name: "elsewhere", Cmds: map[string]int{}}
	for c, n := range calls {
		t := m.TabOfDir(c.Dir)
		r := elsewhere
		if t != nil {
			if byTab[t] == nil {
				byTab[t] = &CallRow{Name: t.Name, Cmds: map[string]int{}}
			}
			r = byTab[t]
		}
		r.Cmds[strings.TrimSpace(c.Program+" "+c.Sub)] += n
		switch c.Program {
		case "git":
			r.Git += n
		case "gh":
			r.Gh += n
		default:
			r.Others += n
		}
	}
	var out []CallRow
	for _, r := range byTab {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total() != out[j].Total() {
			return out[i].Total() > out[j].Total()
		}
		return out[i].Name < out[j].Name
	})
	if elsewhere.Total() > 0 {
		out = append(out, *elsewhere)
	}
	return out
}

// tabOfDir is the tab with dir in one of its worktrees, or nil.
func (m *Core) TabOfDir(dir string) *Tab {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var best *Tab
	bestLen := 0
	for _, t := range m.Tabs {
		paths := []string{t.Dir}
		for _, w := range t.Worktrees {
			paths = append(paths, w.Path)
		}
		for _, p := range paths {
			p = filepath.Clean(p)
			if p != "." && (dir == p || strings.HasPrefix(dir, p+string(filepath.Separator))) && len(p) > bestLen {
				best, bestLen = t, len(p)
			}
		}
	}
	return best
}

// deniedShown is how many of the denials the dialog lists.
const DeniedShown = 6

// callCmdsShown is how many of a row's commands the dialog lists.
const CallCmdsShown = 12

// truncName cuts name to n columns.
func TruncName(name string, n int) string {
	r := []rune(name)
	if len(r) <= n {
		return name
	}
	return string(r[:n-1]) + "…"
}
