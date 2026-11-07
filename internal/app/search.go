package app

import (
	"os"
	"strconv"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"

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
	idx, roots := m.convIndex, m.SearchRoots()
	return func() tea.Msg {
		idx.Update(roots)
		return searchReadMsg{d}
	}
}

func (m *Model) onSearchRead(msg searchReadMsg) {
	msg.d.reading = false
	msg.d.find(m)
}

// find searches again for what is typed.
func (d *searchDialog) find(m *Model) {
	d.words = agents.Words(d.input.Value())
	d.hits = m.convIndex.Search(d.words, core.SearchLimit)
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
	for _, a := range m.AllAgents() {
		if a.SessionID == c.ID && a.ReviewOf == nil {
			m.setFocus(core.FocusAgent)
			return tea.Batch(m.goToAgent(a), m.resize())
		}
	}
	t := m.TabOfDir(c.Dir)
	if t == nil {
		m.toast("no tab works in " + m.ShortDir(c.Dir) + "; carry it on there with " + core.ResumeHint(c))
		return nil
	}
	if !agentAvailable(m, c.Kind) {
		m.toast(string(c.Kind) + " is not on PATH")
		return nil
	}
	if st, err := os.Stat(c.Dir); err != nil || !st.IsDir() {
		m.toast(m.ShortDir(c.Dir) + " is gone: the conversation cannot be carried on there")
		return nil
	}
	m.setFocus(core.FocusAgent)
	sel := m.selectTabCmd(t)
	// In the folder the conversation was had in, which may be one of the
	// tab's repositories rather than the tab's own folder: the agent finds
	// its conversations by folder, and its paths are from there.
	cmd := m.do(core.CarryOnConversation{T: m.Ref(t), ID: core.NewTabID(), Kind: c.Kind, Session: c.ID, Dir: c.Dir})
	m.focusTerms(t)
	return tea.Batch(sel, cmd, m.resize())
}

// selectTabCmd goes to t, if it is not the active tab.
func (m *Model) selectTabCmd(t *core.Tab) tea.Cmd {
	if i := m.TabIndex(t); i >= 0 && t != m.Cur() {
		return m.selectTab(i)
	}
	return nil
}

func agentAvailable(m *Model, k agents.Kind) bool {
	for _, a := range agents.Available(m.Cfg.Agents) {
		if a == k {
			return true
		}
	}
	return false
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
		lines = append(lines, dim("  "+core.Plural(n, "conversation")+" in these repositories and worktrees; type words to find"))
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
		where := m.ShortDir(c.Dir)
		if t := m.TabOfDir(c.Dir); t != nil {
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
