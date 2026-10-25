package review

import (
	"strings"

	"nerdr/internal/github"

	"github.com/charmbracelet/x/ansi"
)

// relLine is one row of the relations panel. A row is actionable on enter
// when it carries a URL to open in the browser.
type relLine struct {
	text    string
	openURL string
	// button makes a click on the row open openURL. Other rows are only
	// selected by a click, so a stray one cannot open a browser.
	button bool
}

func (r relLine) actionable() bool { return r.openURL != "" }

const relationsTabCount = 2

// relationsTabWhy is the tab the diff pane opens a ✎ rationale into.
const relationsTabWhy = 1

var relationsTabLabels = [relationsTabCount]string{
	"1 github", "2 why",
}

// buildRelRows rebuilds m.relRows for the active tab and re-syncs the
// cursor/viewport into its (possibly very different) new bounds. Called
// whenever the underlying data (selected file, PR, agent session) or the
// active tab changes.
func (m *Model) buildRelRows() {
	switch m.relationsTab {
	case 0:
		m.relRows = m.githubRelLines()
	case relationsTabWhy:
		m.relRows = m.whyRelLines()
	}
	m.setRelCursor(m.relCursor)
}

// whyRelLines shows the rationale opened from a ✎ line in the diff pane.
func (m Model) whyRelLines() []relLine {
	if m.whyDetail == "" {
		if m.whyIndex.Len() == 0 {
			return []relLine{{text: "(no rationale notes recorded for this worktree)"}}
		}
		return []relLine{{text: "(select a ✎ line in the diff and press enter for the full rationale)"}}
	}
	var out []relLine
	for _, para := range strings.Split(m.whyDetail, "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, relLine{text: ""})
			continue
		}
		for _, l := range wrapText(para, 110, 0) {
			out = append(out, relLine{text: l})
		}
	}
	return out
}

func (m Model) githubRelLines() []relLine {
	if !github.Available() {
		return []relLine{{text: "(the gh CLI is not installed)"}}
	}
	if !m.prOK {
		msg := m.prMsg
		if msg == "" {
			msg = "no PR found"
		}
		return []relLine{{text: "(" + printableLine(msg) + ")"}}
	}
	// The branch's own PR, summarised as the PR screen (F2) has it.
	out := append(prHeader(m.pr), relLine{text: ""})
	return append(out, prConversationLines(m.pr, 110, true)...)
}

// wrapText splits body into plain-text lines wrapped to width, collapsing
// existing newlines to spaces first (comment bodies are free-form prose,
// not code, so no need to preserve original line breaks). maxLines caps the
// result with an ellipsis; 0 means keep everything.
func wrapText(body string, width, maxLines int) []string {
	width = max(width, 1)
	body = strings.Join(strings.Fields(printable(body)), " ")
	if body == "" {
		return nil
	}
	// By columns, not bytes: a word longer than the line is cut between
	// characters, never inside one.
	var lines []string
	for _, l := range strings.Split(ansi.Wrap(body, width, ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] += " …"
	}
	return lines
}
