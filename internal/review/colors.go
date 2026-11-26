package review

import (
	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DiffColors are the colour schemes F1 can show a change in, the first
// the default: GitHub's, its dark diff laid out as GitHub lays it out,
// and nerdr's own, soft tints with the changed lines' text in a tone of
// the same hue. c in F1 steps through them; diff_colors picks one for
// every start.
var DiffColors = []string{"github", "nerdr"}

func init() { useDiffColors(DiffColors[0]) }

// diffScheme is one of DiffColors: the styles of a change, and the
// chroma style its unchanged code is coloured with.
type diffScheme struct {
	add, del, addSign, delSign, wordAdd, wordDel lipgloss.Style
	statAdd, statDel, hunk                       lipgloss.Style
	syntax                                       *chroma.Style

	// The gutter: the line numbers' style on a context, added, removed
	// and hunk row, and whether the old side's number is shown beside
	// the new one's.
	num, addNum, delNum, hunkNum lipgloss.Style
	bothNums                     bool

	// syntaxChanged is whether added and removed lines' code is coloured
	// by its syntax too, on the tint; bg the pane's background, if the
	// scheme has its own.
	syntaxChanged bool
	bg, fg        lipgloss.Color
}

// Primer's dark colours: the background, the text, and muted text.
var (
	ghBG    = lipgloss.Color("#0d1117")
	ghFG    = lipgloss.Color("#f0f6fc")
	ghMuted = lipgloss.Color("#9198a1")
	ghText  = lipgloss.NewStyle().Foreground(ghFG)
)

// githubDark is the code's colours on GitHub in its dark mode, its
// "prettylights": chroma's own github-dark has bold and italics GitHub
// does not.
var githubDark = chroma.MustNewStyle("github-web-dark", chroma.StyleEntries{
	chroma.Background:          "#f0f6fc bg:#0d1117",
	chroma.Keyword:             "#ff7b72",
	chroma.KeywordConstant:     "#79c0ff",
	chroma.KeywordPseudo:       "#79c0ff",
	chroma.KeywordType:         "#ff7b72",
	chroma.Name:                "#f0f6fc",
	chroma.NameAttribute:       "#79c0ff",
	chroma.NameBuiltin:         "#79c0ff",
	chroma.NameClass:           "#ffa657",
	chroma.NameConstant:        "#79c0ff",
	chroma.NameDecorator:       "#d2a8ff",
	chroma.NameEntity:          "#ffa657",
	chroma.NameException:       "#ffa657",
	chroma.NameFunction:        "#d2a8ff",
	chroma.NameLabel:           "#79c0ff",
	chroma.NameTag:             "#7ee787",
	chroma.NameVariable:        "#ffa657",
	chroma.Literal:             "#a5d6ff",
	chroma.LiteralNumber:       "#79c0ff",
	chroma.LiteralStringEscape: "#79c0ff",
	chroma.LiteralStringRegex:  "#7ee787",
	chroma.Operator:            "#ff7b72",
	chroma.OperatorWord:        "#ff7b72",
	chroma.Comment:             "#9198a1",
	chroma.GenericDeleted:      "#ffdcd7 bg:#67060c",
	chroma.GenericInserted:     "#aff5b4 bg:#033a16",
	chroma.GenericHeading:      "#1f6feb",
	chroma.GenericEmph:         "italic",
	chroma.GenericStrong:       "bold",
	chroma.GenericSubheading:   "#79c0ff",
	chroma.Error:               "#ffa198",
})

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
		hunk:    lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
		syntax:  styles.Get("monokai"),
		num:     dimStyle,
		addNum:  dimStyle,
		delNum:  dimStyle,
		hunkNum: dimStyle,
	},
	// GitHub's dark theme, as its "Files changed" draws a diff, in the
	// colours of Primer, its design system: the old and the new line
	// number side by side, in a stronger tint of the row's on a changed
	// line, each hunk's header on a blue band, the code coloured by its
	// syntax on every line, changed ones too, and all of it on GitHub's
	// background. Primer's tints are see-through, over that background;
	// these are what they come to on it.
	"github": {
		add:           ghText.Background(lipgloss.Color("#12261e")), // #2ea04326
		del:           ghText.Background(lipgloss.Color("#25181c")), // #f851491a
		addSign:       ghText.Background(lipgloss.Color("#12261e")),
		delSign:       ghText.Background(lipgloss.Color("#25181c")),
		wordAdd:       ghText.Background(lipgloss.Color("#1a4a29")), // #2ea04366
		wordDel:       ghText.Background(lipgloss.Color("#6b2b2b")), // #f8514966
		statAdd:       lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950")),
		statDel:       lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")),
		hunk:          lipgloss.NewStyle().Background(lipgloss.Color("#111d2e")).Foreground(ghMuted), // #388bfd1a
		syntax:        githubDark,
		num:           lipgloss.NewStyle().Background(ghBG).Foreground(ghMuted),
		addNum:        ghText.Background(lipgloss.Color("#1c4428")), // #3fb9504d
		delNum:        ghText.Background(lipgloss.Color("#542426")), // #f851494d
		hunkNum:       lipgloss.NewStyle().Background(lipgloss.Color("#0c2d6b")).Foreground(ghMuted),
		bothNums:      true,
		syntaxChanged: true,
		bg:            ghBG,
		fg:            ghFG,
	},
}

// curScheme is the scheme changes are drawn in now. A diff is coloured
// in the background in the scheme its request took from here, never in
// whatever this is by the time it is read, since c changes it on the UI's
// goroutine.
var curScheme diffScheme

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
	numStyle, addNumStyle, delNumStyle, hunkNumStyle = s.num, s.addNum, s.delNum, s.hunkNum
	bothNums = s.bothNums
	paneBG, paneFG = s.bg, s.fg
	curScheme = s
	return true
}

// SetDiffColors shows changes in scheme name, one of DiffColors; an
// unknown name is the first's.
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
