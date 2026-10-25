package review

import (
	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DiffColors are the colour schemes F1 can show a change in, the first
// the default: nerdr's own, soft tints with the changed lines' text in a
// tone of the same hue, and GitHub's, its dark diff with its syntax
// colours. c in F1 steps through them; diff_colors picks one for every
// start.
var DiffColors = []string{"nerdr", "github"}

// diffScheme is one of DiffColors: the styles of a change, and the
// chroma style its unchanged code is coloured with.
type diffScheme struct {
	add, del, addSign, delSign, wordAdd, wordDel lipgloss.Style
	statAdd, statDel, hunk                       lipgloss.Style
	syntax                                       *chroma.Style
}

var diffSchemes = map[string]diffScheme{
	"nerdr": {
		add:     addStyle,
		del:     delStyle,
		addSign: addSignStyle,
		delSign: delSignStyle,
		wordAdd: wordAddStyle,
		wordDel: wordDelStyle,
		statAdd: statAddStyle,
		statDel: statDelStyle,
		hunk:    hunkStyle,
		syntax:  styles.Get("monokai"),
	},
	// GitHub's dark theme: its tints over its background, its signs'
	// green and red, and its text and syntax colours.
	"github": {
		add:     lipgloss.NewStyle().Background(lipgloss.Color("#12261e")).Foreground(lipgloss.Color("#e6edf3")),
		del:     lipgloss.NewStyle().Background(lipgloss.Color("#25171c")).Foreground(lipgloss.Color("#e6edf3")),
		addSign: lipgloss.NewStyle().Background(lipgloss.Color("#12261e")).Foreground(lipgloss.Color("#3fb950")),
		delSign: lipgloss.NewStyle().Background(lipgloss.Color("#25171c")).Foreground(lipgloss.Color("#f85149")),
		wordAdd: lipgloss.NewStyle().Background(lipgloss.Color("#1a4a29")).Foreground(lipgloss.Color("#e6edf3")),
		wordDel: lipgloss.NewStyle().Background(lipgloss.Color("#6b2b2b")).Foreground(lipgloss.Color("#e6edf3")),
		statAdd: lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950")),
		statDel: lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")),
		hunk:    lipgloss.NewStyle().Foreground(lipgloss.Color("#8d96a0")),
		syntax:  styles.Get("github-dark"),
	},
}

// useDiffColors makes scheme name the one changes are drawn in, reporting
// whether there is one by that name.
func useDiffColors(name string) bool {
	s, ok := diffSchemes[name]
	if !ok {
		return false
	}
	addStyle, delStyle, addSignStyle, delSignStyle = s.add, s.del, s.addSign, s.delSign
	wordAddStyle, wordDelStyle = s.wordAdd, s.wordDel
	statAddStyle, statDelStyle, hunkStyle = s.statAdd, s.statDel, s.hunk
	syntaxStyle = s.syntax
	syntaxStyleCache = map[chroma.TokenType]lipgloss.Style{}
	return true
}

// SetDiffColors shows changes in scheme name, one of DiffColors; an
// unknown name is nerdr's.
func (m Model) SetDiffColors(name string) (Model, tea.Cmd) {
	if _, ok := diffSchemes[name]; !ok {
		name = DiffColors[0]
	}
	if name == m.diffColors {
		return m, nil
	}
	return m, m.showDiffColors(name)
}

// showDiffColors draws the change in scheme name: the diff is coloured as
// it is loaded, so it is loaded again.
func (m *Model) showDiffColors(name string) tea.Cmd {
	useDiffColors(name)
	m.diffColors = name
	if len(m.worktrees) == 0 {
		return nil
	}
	return m.loadDiff()
}

// cycleDiffColors is c in F1: the next of DiffColors.
func (m *Model) cycleDiffColors() tea.Cmd {
	next := DiffColors[0]
	for i, n := range DiffColors {
		if n == m.diffColors || (m.diffColors == "" && i == 0) {
			next = DiffColors[(i+1)%len(DiffColors)]
		}
	}
	cmd := m.showDiffColors(next)
	m.setToast("diff colours: " + next + " · diff_colors in the settings for every start")
	return cmd
}
