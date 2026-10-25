package app

import (
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// switcher finds a tab by typing part of its name, across all groups. With
// nothing typed the tabs that want you come first.
type switcher struct {
	input  textinput.Model
	cursor int
}

func (m *Model) openSwitcher() tea.Cmd {
	if len(m.tabs) == 0 {
		return nil
	}
	in := textinput.New()
	in.Prompt = "tab: "
	in.Placeholder = "type to filter…"
	in.Focus()
	m.dialog = &switcher{input: in}
	return nil
}

// matches are the indexes of the tabs matching what is typed, best first.
func (s *switcher) matches(m *Model) []int {
	q := strings.ToLower(strings.TrimSpace(s.input.Value()))
	type hit struct{ i, score int }
	var hits []hit
	for i, t := range m.tabs {
		score, ok := fuzzy(q, strings.ToLower(t.Name+" "+t.group()+" "+string(t.Agent)))
		if !ok {
			continue
		}
		hits = append(hits, hit{i, score})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		ta, tb := m.tabs[hits[a].i], m.tabs[hits[b].i]
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		if ta.attention() != tb.attention() {
			return ta.attention() > tb.attention()
		}
		return hits[a].i < hits[b].i
	})
	out := make([]int, len(hits))
	for n, h := range hits {
		out[n] = h.i
	}
	return out
}

// fuzzy reports whether every character of q appears in s in order, and
// scores the match: characters that follow each other, or start a word,
// count for more.
func fuzzy(q, s string) (int, bool) {
	if q == "" {
		return 0, true
	}
	rs, score, qi := []rune(s), 0, 0
	qr := []rune(q)
	prev := -2
	for i, r := range rs {
		if qi == len(qr) {
			break
		}
		if r != qr[qi] {
			continue
		}
		score++
		if i == prev+1 {
			score += 3
		}
		if i == 0 || !unicode.IsLetter(rs[i-1]) && !unicode.IsDigit(rs[i-1]) {
			score += 2
		}
		prev = i
		qi++
	}
	return score, qi == len(qr)
}

func (s *switcher) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	hits := s.matches(m)
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "up", "ctrl+p", "ctrl+k":
		s.cursor = max(s.cursor-1, 0)
		return nil
	case "down", "ctrl+n", "ctrl+j", "tab":
		s.cursor = min(s.cursor+1, max(len(hits)-1, 0))
		return nil
	case "enter":
		m.dialog = nil
		if s.cursor < len(hits) {
			m.setFocus(focusAgent)
			return tea.Batch(m.selectTab(hits[s.cursor]), m.resize())
		}
		return nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.cursor = 0
	return cmd
}

func (s *switcher) view(m *Model, w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	lines := []string{s.input.View(), ""}
	hits := s.matches(m)
	if len(hits) == 0 {
		lines = append(lines, dim("  no tab matches"))
	}
	for n, i := range hits {
		if n >= max(h-10, 5) {
			lines = append(lines, dim("  …"))
			break
		}
		t := m.tabs[i]
		line := light(t.lit()) + " " + t.Name + dim("  "+t.group()+" · "+string(t.Agent)+" · "+t.lit().label())
		if i == m.active {
			line += dim("  (here)")
		}
		lines = append(lines, m.onClick(cursorLine(n == s.cursor, line, sel), func(m *Model) tea.Cmd {
			s.cursor = n
			return s.key(m, keyMsg("enter"))
		}))
	}
	lines = append(lines, "", m.keys("", "type to filter", "", "↑↓ move", "enter", "go", "esc", "cancel"))
	return box("Go to tab", lines, w)
}

// renamer gives the active tab a name of its own.
type renamer struct {
	t     *tab
	input textinput.Model
}

func (m *Model) openRename() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	in := textinput.New()
	in.Prompt = "name: "
	in.SetValue(t.Name)
	in.CharLimit = 60
	in.Focus()
	in.CursorEnd()
	m.dialog = &renamer{t: t, input: in}
	return nil
}

func (r *renamer) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "enter":
		m.dialog = nil
		if name := strings.TrimSpace(r.input.Value()); name != "" && m.tabIndex(r.t) >= 0 {
			r.t.Name = name
			m.saveSession()
			m.reportHerdr()
		}
		return nil
	}
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(msg)
	return cmd
}

func (r *renamer) view(m *Model, w, h int) string {
	return box("Rename tab", []string{r.input.View(), "",
		dim("The branch stays " + branchOf(r.t) + "; this is only the tab's name."), "",
		m.keys("enter", "save", "esc", "cancel")}, w)
}

// groupRenamer names the group of tabs spanning several repositories.
type groupRenamer struct {
	group string
	input textinput.Model
}

func (m *Model) openGroupRename(g string) tea.Cmd {
	in := textinput.New()
	in.Prompt = "group: "
	in.Placeholder = multiGroup
	if g != multiGroup {
		in.SetValue(g)
	}
	in.CharLimit = 40
	in.Focus()
	in.CursorEnd()
	m.dialog = &groupRenamer{group: g, input: in}
	return nil
}

func (r *groupRenamer) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "enter":
		m.dialog = nil
		m.renameGroup(r.group, strings.TrimSpace(r.input.Value()))
		return nil
	}
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(msg)
	return cmd
}

func (r *groupRenamer) view(m *Model, w, h int) string {
	return box("Rename group", []string{r.input.View(), "",
		dim("For its tabs spanning several repositories, and their worktrees"),
		dim("opened again later; empty for " + multiGroup + "."), "",
		m.keys("enter", "save", "esc", "cancel")}, w)
}

// renameGroup moves the tabs of group g that span several repositories
// into the group named name, multi when it is empty.
func (m *Model) renameGroup(g, name string) {
	if name == multiGroup {
		name = ""
	}
	moved := false
	for _, i := range m.groupTabs(g) {
		t := m.tabs[i]
		if len(t.Worktrees) < 2 {
			continue
		}
		t.Group, moved = name, true
		if name == "" {
			delete(m.groupNames, t.Dir)
		} else {
			m.rememberGroup(t.Dir, name, t.Worktrees)
		}
	}
	if !moved {
		return
	}
	to := name
	if to == "" {
		to = multiGroup
	}
	if id, ok := m.lastInGroup[g]; ok && m.lastInGroup[to] == "" {
		m.lastInGroup[to] = id
	}
	m.saveSession()
}

func branchOf(t *tab) string {
	if len(t.Worktrees) > 0 && t.Worktrees[0].Branch != "" {
		return t.Worktrees[0].Branch
	}
	return t.Name
}

// moveTab moves the active tab one place left (-1) or right (1) among the
// tabs of its group.
func (m *Model) moveTab(delta int) tea.Cmd {
	idx := m.groupTabs(m.curGroup())
	for pos, i := range idx {
		if i != m.active {
			continue
		}
		to := pos + delta
		if to < 0 || to >= len(idx) {
			return nil
		}
		j := idx[to]
		m.tabs[i], m.tabs[j] = m.tabs[j], m.tabs[i]
		m.active = j
		m.saveSession()
		return nil
	}
	return nil
}
