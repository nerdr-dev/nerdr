package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/state"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The search, the prefix and /: what was said in the agents'
// conversations had in the folders nerdr works in — the one it started
// in, where worktrees go and each tab's — found by typing words. enter
// goes to the agent carrying on a conversation found, or carries it on in
// a new pane of the tab working in its worktree. The conversations are
// read from claude's and codex's logs in the background (agents.Index),
// once, and again only from a log that has changed.

// searchLimit is the most conversations listed.
const searchLimit = 200

type searchDialog struct {
	input   textinput.Model
	cursor  int
	top     int
	reading bool
	hits    []agents.Hit
	words   []string
}

// searchReadMsg says the index is read.
type searchReadMsg struct{ d *searchDialog }

func (m *Model) openSearch() tea.Cmd {
	in := textinput.New()
	in.Prompt = "search: "
	in.Placeholder = "words said in a conversation…"
	in.Focus()
	d := &searchDialog{input: in, reading: true}
	m.dialog = d
	if m.convIndex == nil {
		m.convIndex = &agents.Index{}
	}
	idx, roots := m.convIndex, m.searchRoots()
	return func() tea.Msg {
		idx.Update(roots)
		return searchReadMsg{d}
	}
}

// searchRoots are the folders whose conversations are searched.
func (m *Model) searchRoots() []string {
	roots := []string{m.root, m.wtRoot}
	for _, t := range m.tabs {
		roots = append(roots, t.Dir)
		for _, w := range t.Worktrees {
			roots = append(roots, w.Path)
		}
	}
	return roots
}

func (m *Model) onSearchRead(msg searchReadMsg) {
	msg.d.reading = false
	msg.d.find(m)
}

// find searches again for what is typed.
func (d *searchDialog) find(m *Model) {
	d.words = agents.Words(d.input.Value())
	d.hits = m.convIndex.Search(d.words, searchLimit)
	d.cursor, d.top = 0, 0
}

func (d *searchDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "up", "ctrl+p", "ctrl+k":
		d.cursor = max(d.cursor-1, 0)
		return nil
	case "down", "ctrl+n", "ctrl+j", "tab":
		d.cursor = min(d.cursor+1, max(len(d.hits)-1, 0))
		return nil
	case "pgup":
		d.cursor = max(d.cursor-10, 0)
		return nil
	case "pgdown":
		d.cursor = min(d.cursor+10, max(len(d.hits)-1, 0))
		return nil
	case "enter":
		if d.cursor < len(d.hits) {
			m.dialog = nil
			return m.openConversation(d.hits[d.cursor].Conv)
		}
		return nil
	}
	var cmd tea.Cmd
	before := d.input.Value()
	d.input, cmd = d.input.Update(msg)
	if d.input.Value() != before && !d.reading {
		d.find(m)
	}
	return cmd
}

// openConversation goes to the agent carrying on c, or carries it on in
// a new pane of the tab working in its folder.
func (m *Model) openConversation(c *agents.Conversation) tea.Cmd {
	for _, a := range m.allAgents() {
		if a.SessionID == c.ID && a.reviewOf == nil {
			m.setFocus(focusAgent)
			return tea.Batch(m.goToAgent(a), m.resize())
		}
	}
	t := m.tabOfDir(c.Dir)
	if t == nil {
		m.toast("no tab works in " + m.shortDir(c.Dir) + "; carry it on there with " + resumeHint(c))
		return nil
	}
	if !agentAvailable(m, c.Kind) {
		m.toast(string(c.Kind) + " is not on PATH")
		return nil
	}
	if st, err := os.Stat(c.Dir); err != nil || !st.IsDir() {
		m.toast(m.shortDir(c.Dir) + " is gone: the conversation cannot be carried on there")
		return nil
	}
	m.setFocus(focusAgent)
	sel := m.selectTabCmd(t)
	// In the folder the conversation was had in, which may be one of the
	// tab's repositories rather than the tab's own folder: the agent finds
	// its conversations by folder, and its paths are from there.
	a := &tab{Tab: state.Tab{ID: newTabID(), Name: t.Name, Dir: c.Dir, Agent: c.Kind, Worktrees: t.Worktrees,
		Num: t.nextNum(), SessionID: c.ID}, status: stStarting, partOf: t}
	cmd := m.addPane(t, &paneTab{kind: paneAgent, a: a}, false)
	start := m.start(a, true)
	m.saveSession()
	m.focusTerms(t)
	m.toast(t.Name + ": " + m.agentLabel(t, a) + " carries on the conversation")
	return tea.Batch(sel, cmd, start, m.resize())
}

// selectTabCmd goes to t, if it is not the active tab.
func (m *Model) selectTabCmd(t *tab) tea.Cmd {
	if i := m.tabIndex(t); i >= 0 && t != m.cur() {
		return m.selectTab(i)
	}
	return nil
}

func agentAvailable(m *Model, k agents.Kind) bool {
	for _, a := range agents.Available(m.cfg.Agents) {
		if a == k {
			return true
		}
	}
	return false
}

// resumeHint is the command that carries on c in a terminal.
func resumeHint(c *agents.Conversation) string {
	if c.Kind == agents.Codex {
		return "codex resume " + c.ID
	}
	return "claude --resume " + c.ID
}

// shortDir is dir from the folder nerdr started in, when under it.
func (m *Model) shortDir(dir string) string {
	if rel, err := filepath.Rel(m.root, dir); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return filepath.Base(dir)
		}
		return rel
	}
	return dir
}

func (d *searchDialog) view(m *Model, w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	lines := []string{d.input.View(), ""}
	n := 0
	if m.convIndex != nil {
		n = m.convIndex.Len()
	}
	switch {
	case d.reading && n == 0:
		lines = append(lines, dim("  reading the agents' conversations…"))
	case len(d.words) == 0:
		lines = append(lines, dim("  "+plural(n, "conversation")+" in these repositories and worktrees; type words to find"))
	case len(d.hits) == 0:
		lines = append(lines, dim("  no conversation has all of these words"))
	}
	lineW := max(boxInner(w)-2, 30)
	room := max((h-12)/2, 3)
	if d.cursor < d.top {
		d.top = d.cursor
	}
	if d.cursor >= d.top+room {
		d.top = d.cursor - room + 1
	}
	if d.top > 0 {
		lines = append(lines, dim("  …"))
	}
	for i := d.top; i < min(len(d.hits), d.top+room); i++ {
		hit := d.hits[i]
		c := hit.Conv
		where := m.shortDir(c.Dir)
		if t := m.tabOfDir(c.Dir); t != nil {
			where = t.Name
		}
		head := c.Modified.Format("Jan 2 15:04") + "  " + where + dim("  "+string(c.Kind))
		if hit.Count > 1 {
			head += dim("  " + strconv.Itoa(hit.Count) + " matches")
		}
		who := c.Said[hit.At].Who
		snip := dim(who+": ") + hit.Snippet(d.words, lineW)
		i := i
		lines = append(lines,
			m.onClick(cursorLine(i == d.cursor, ansi.Truncate(head, lineW, "…"), sel), func(m *Model) tea.Cmd {
				d.cursor = i
				return d.key(m, keyMsg("enter"))
			}),
			"    "+ansi.Truncate(snip, lineW-2, "…"))
	}
	if d.top+room < len(d.hits) {
		lines = append(lines, dim("  …"))
	}
	if d.reading && n > 0 {
		lines = append(lines, "", dim("  reading the conversations changed since…"))
	}
	lines = append(lines, "", m.keys("", "type to find", "", "↑↓ move", "enter", "go to it / carry it on", "esc", "close"))
	return box("Search conversations", lines, w)
}
