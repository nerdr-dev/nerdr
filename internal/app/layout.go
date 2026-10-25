package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Layouts: the ways the screen can be laid out, each set by a key in the
// view group (prefix z). The layout dialog (prefix z l) sketches them for
// whoever does not remember the keys; the same keys work without it. A
// layout key sets its layout whatever was on screen before, unlike z e,
// z z and z v, which toggle: pressing it again changes nothing.

// agentWideReview is the review panel's share, in percent, when the agent
// has three quarters of the window.
const agentWideReview = 25

// layout is one of them: its key, its name, the parts of its sketch and
// its name for the layout setting in the config ("" when it has none).
type layout struct {
	key, name string
	parts     []sketchPart
	config    string
}

// sketchPart is one column of a sketch: its label and its share of the
// sketch's width, in columns.
type sketchPart struct {
	label string
	w     int
}

var layouts = []layout{
	{"=", "normal", []sketchPart{{"agent", 8}, {"F1-F5", 8}}, "normal"},
	{"+", "agent 75%", []sketchPart{{"agent", 12}, {"F", 4}}, "wide"},
	{"a", "agent alone", []sketchPart{{"agent", 17}}, "agent"},
	{"z", "F-menu alone", []sketchPart{{"F1-F5", 17}}, "panel"},
	// Not in the config: two agents need a second tab, which nerdr may
	// not have at start.
	{"v", "two agents", []sketchPart{{"agent", 8}, {"agent 2", 8}}, ""},
}

// startLayout lays the screen out as the config's layout setting says,
// before nerdr has a window size: only the fields, no resize.
func (m *Model) startLayout() {
	switch m.cfg.Layout {
	case "wide":
		m.reviewOpen, m.reviewWidth = true, agentWideReview
	case "small":
		m.panelSmall, m.reviewOpen = true, false
	case "agent":
		m.reviewOpen = false
	case "panel":
		m.reviewOpen, m.reviewZoom = true, true
		m.focus = focusReview
		m.review = m.review.SetFocused(true)
	default: // "normal"
		m.reviewOpen = true
	}
}

// currentLayout is the key of the layout on screen, or "" when it is none
// of them (the panel dragged to another width, say).
func (m *Model) currentLayout() string {
	switch {
	case m.splitShown():
		return "v"
	case m.reviewOpen && m.reviewZoom:
		return "z"
	case !m.reviewOpen:
		return "a"
	case m.reviewWidth == agentWideReview:
		return "+"
	case m.reviewWidth == m.cfg.PanelWidth:
		return "="
	}
	return ""
}

// setLayout lays the screen out as the layout with key k sketches.
func (m *Model) setLayout(k string) tea.Cmd {
	if k != "v" {
		// A layout picked keeps the panel as it shows it, panes or no.
		m.panelSmall, m.panelAuto, m.panelKept = false, false, k == "=" || k == "+"
	}
	switch k {
	case "=", "+":
		m.reviewOpen, m.reviewZoom = true, false
		m.reviewWidth = m.cfg.PanelWidth
		if k == "+" {
			m.reviewWidth = agentWideReview
		}
		m.setFocus(focusAgent)
	case "a":
		m.reviewOpen, m.reviewZoom = false, false
		if m.split != nil {
			m.split = nil
			m.saveSession()
		}
		m.setFocus(focusAgent)
	case "z":
		m.reviewOpen, m.reviewZoom = true, true
		m.setFocus(focusReview)
	case "v":
		if m.splitShown() {
			return nil
		}
		// toggleSplit brings a split hidden behind the panel back, or
		// makes one with a neighbour.
		return m.toggleSplit()
	default:
		return nil
	}
	return m.resize()
}

// layoutPicker is the layout dialog: a sketch of each layout over its key.
// The arrow keys lay the screen out as the one they move to, behind the
// dialog; enter keeps it and esc goes back to the layout from before.
type layoutPicker struct {
	cursor int
	// perRow is how many sketches the last view put in a row, for up and
	// down.
	perRow int
	before layoutSnapshot
}

// layoutSnapshot is everything a layout key can change, to put back.
type layoutSnapshot struct {
	reviewOpen, reviewZoom bool
	reviewWidth            int
	split                  *tab
	splitLeft              bool
	focus                  focusArea
	active                 *tab
}

func (m *Model) openLayouts() tea.Cmd {
	p := &layoutPicker{perRow: len(layouts), before: layoutSnapshot{
		reviewOpen: m.reviewOpen, reviewZoom: m.reviewZoom,
		reviewWidth: m.reviewWidth,
		split:       m.split, splitLeft: m.splitLeft,
		focus: m.focus, active: m.cur(),
	}}
	cur := m.currentLayout()
	for i, l := range layouts {
		if l.key == cur {
			p.cursor = i
		}
	}
	m.dialog = p
	return nil
}

// restore puts the layout back as it was when the dialog opened.
func (p *layoutPicker) restore(m *Model) tea.Cmd {
	b := p.before
	var cmd tea.Cmd
	if i := m.tabIndex(b.active); i >= 0 && b.active != m.cur() {
		cmd = m.selectTab(i) // two agents moved the keyboard to the neighbour
	}
	splitChanged := m.split != b.split
	m.reviewOpen, m.reviewZoom, m.reviewWidth = b.reviewOpen, b.reviewZoom, b.reviewWidth
	m.split, m.splitLeft = b.split, b.splitLeft
	// Not setFocus: it would also take the zoom back off.
	m.focus = b.focus
	m.review = m.review.SetFocused(b.focus == focusReview)
	if splitChanged {
		m.saveSession()
	}
	return tea.Batch(cmd, m.resize())
}

func (p *layoutPicker) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	for _, l := range layouts {
		if l.key == k {
			m.dialog = nil
			return m.setLayout(k)
		}
	}
	move := 0
	switch k {
	case "left", "h":
		move = -1
	case "right", "l":
		move = 1
	case "up", "k":
		move = -p.perRow
	case "down", "j":
		move = p.perRow
	case "enter":
		m.dialog = nil
	case "esc", "q", "ctrl+c":
		m.dialog = nil
		return p.restore(m)
	}
	if move == 0 {
		return nil
	}
	i := p.cursor + move
	if i < 0 || i >= len(layouts) {
		return nil
	}
	p.cursor = i
	return m.setLayout(layouts[i].key)
}

func (p *layoutPicker) view(m *Model, w, h int) string {
	var cells []string
	for i, l := range layouts {
		colour := lipgloss.Color("244")
		name := l.name
		if i == p.cursor {
			colour = lipgloss.Color("81")
			name = lipgloss.NewStyle().Bold(true).Render(name)
		}
		cell := lipgloss.NewStyle().Foreground(colour).Render(sketch(l.parts)) +
			"\n" + keyHint(l.key) + " " + name
		cell = lipgloss.NewStyle().Width(sketchWidth + 3).Render(cell)
		cells = append(cells, m.onClick(cell, press(l.key)))
	}
	// As many sketches to a row as the window has room for.
	perRow := max(min(len(cells), (w-10)/(sketchWidth+3)), 1)
	p.perRow = perRow
	var rows []string
	for i := 0; i < len(cells); i += perRow {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cells[i:min(i+perRow, len(cells))]...), "")
	}
	lines := strings.Split(strings.Join(rows, "\n"), "\n")
	lines = append(lines,
		dim("The keys work straight after "+m.pk("z")+" too, without this."),
		dim("e, < and > still show/hide the panel and change its width."),
		"",
		keyHint("←→")+" "+dim("try one")+dim(" · ")+m.keys("enter", "keep it", "esc", "back to the one before"))
	return box("Layouts", lines, w)
}

// sketchWidth is a sketch's width inside its frame: its parts and the
// lines between them.
const sketchWidth = 17

// sketch draws a small window split into parts, each labelled in the
// middle.
func sketch(parts []sketchPart) string {
	const height = 3
	edge := func(left, mid, right string) string {
		var b strings.Builder
		b.WriteString(left)
		for i, p := range parts {
			if i > 0 {
				b.WriteString(mid)
			}
			b.WriteString(strings.Repeat("─", p.w))
		}
		return b.String() + right
	}
	lines := []string{edge("┌", "┬", "┐")}
	for row := 0; row < height; row++ {
		var b strings.Builder
		b.WriteString("│")
		for i, p := range parts {
			if i > 0 {
				b.WriteString("│")
			}
			text := ""
			if row == height/2 {
				text = p.label
			}
			pad := max(p.w-len(text), 0)
			b.WriteString(strings.Repeat(" ", pad/2) + text + strings.Repeat(" ", pad-pad/2))
		}
		lines = append(lines, b.String()+"│")
	}
	lines = append(lines, edge("└", "┴", "┘"))
	return strings.Join(lines, "\n")
}
