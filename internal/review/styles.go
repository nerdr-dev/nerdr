package review

import "github.com/charmbracelet/lipgloss"

var (
	// Pane titles are drawn as a bar across the pane so they read as
	// headings rather than as a first row of content. The focused pane's
	// bar is brighter, which doubles as a focus cue alongside the border.
	headingStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("238")).
			Foreground(lipgloss.Color("252")).
			Bold(true)

	// Deliberately not the selection colour: the light blue means "this is
	// the thing you have selected" (row cursor, active tab), so a heading
	// wearing it would be indistinguishable from a selected row.
	headingActiveStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("25")).
				Foreground(lipgloss.Color("231")).
				Bold(true)

	// The pane with the keyboard: a quiet blue, as the agent's frame
	// beside it has.
	borderActive = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("110"))

	borderInactive = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("110")).
			Bold(true)

	// The same selection, seen from another pane: a pane you have stepped
	// out of still has a selected row, and while you read the diff that row
	// is what the diff belongs to. Darker blue keeps it recognisably the
	// selection without competing with the pane that holds the focus.
	selectedInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("231")).
				Background(lipgloss.Color("24"))

	// The top menu: the selected mode wears the same blue as a focused
	// pane heading, the other sits on the darker bar.
	menuActiveStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("25")).
			Foreground(lipgloss.Color("231")).
			Bold(true)

	menuItemStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("246"))

	// PR status glyphs: the review verdict and the CI rollup.
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	pendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("221"))

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// Key hints, as nerdr draws them in its own panels: the key, and
	// what it does.
	hintKeyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	hintTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	// One style per kind, keyed the same way statusMark classifies.
	markStyle = map[string]lipgloss.Style{
		"m": lipgloss.NewStyle().Foreground(lipgloss.Color("221")),
		"n": lipgloss.NewStyle().Foreground(lipgloss.Color("120")),
		"d": lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
	}

	// Changed lines are marked by a tint across the whole row rather than
	// by bright, bold text. They still have to stand out from the
	// syntax-highlighted context around them — bright green did that, but
	// glared — and a block of background does it by area instead of by
	// intensity, the way delta and GitHub mark a diff. The text on the tint
	// is a soft tone of the same hue, and the words that changed within a
	// line get a stronger tint, not white-on-saturated.
	addStyle     = lipgloss.NewStyle().Background(lipgloss.Color("#1c3326")).Foreground(lipgloss.Color("#bfe3c6"))
	delStyle     = lipgloss.NewStyle().Background(lipgloss.Color("#3a2025")).Foreground(lipgloss.Color("#eec3c9"))
	addSignStyle = addStyle.Foreground(lipgloss.Color("#6fbf86"))
	delSignStyle = delStyle.Foreground(lipgloss.Color("#e07b88"))
	wordAddStyle = lipgloss.NewStyle().Background(lipgloss.Color("#2f5e3d")).Foreground(lipgloss.Color("#e3f7e7"))
	wordDelStyle = lipgloss.NewStyle().Background(lipgloss.Color("#6b2d37")).Foreground(lipgloss.Color("#fbe3e6"))

	hunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	recentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))

	// The file list's counts and marks: the sign colours without the tint.
	statAddStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6fbf86"))
	statDelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e07b88"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	refStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))

	cursorMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	selStyle        = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("231"))
	whyStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Italic(true)
	toastStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
)

// statusColor returns the style for a porcelain status code, by the kind
// of change it represents.
func statusColor(code string) lipgloss.Style {
	if s, ok := markStyle[statusMark(code)]; ok {
		return s
	}
	return lipgloss.NewStyle()
}
