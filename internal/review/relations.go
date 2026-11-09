package review

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// relLine is one row of the PR screen (F2). A row is actionable on enter
// when it carries a URL to open in the browser.
type relLine struct {
	text    string
	openURL string
	// button makes a click on the row open openURL. Other rows are only
	// selected by a click, so a stray one cannot open a browser.
	button bool
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
