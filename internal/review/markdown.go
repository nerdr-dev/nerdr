package review

import (
	"html"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// GitHub comments are Markdown with HTML in it, and the bots' are mostly
// markup: badges, links to dashboards, tables, folded sections, hidden
// anchors. readable turns one into lines a person can read in the pane —
// the words kept, the links' and images' addresses left out (enter opens
// the comment on GitHub, where they are) — and wraps them width wide.

var (
	mdComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	mdImage     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdBreak     = regexp.MustCompile(`(?i)<br\s*/?>|</?(p|div|details|table|tr|ul|ol|h[1-6]|hr)\b[^>]*>`)
	mdSummary   = regexp.MustCompile(`(?i)<summary\b[^>]*>`)
	mdItem      = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	mdCell      = regexp.MustCompile(`(?i)</t[dh]>`)
	mdTag       = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	mdHeading   = regexp.MustCompile(`^#{1,6}\s+`)
	mdBullet    = regexp.MustCompile(`^[-*+]\s+`)
	mdTableRule = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
	mdEmphasis  = strings.NewReplacer("**", "", "__", "", "`", "")
	headStyle   = lipgloss.NewStyle().Bold(true)
)

// readable is a comment's body as lines width wide, for the PR screen.
func readable(body string, width int) []string {
	width = max(width, 1)
	s := strings.ReplaceAll(printable(body), "\r\n", "\n")
	s = mdComment.ReplaceAllString(s, "")
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdSummary.ReplaceAllString(s, "\n▸ ")
	s = mdItem.ReplaceAllString(s, "\n• ")
	s = mdCell.ReplaceAllString(s, " · ")
	s = mdBreak.ReplaceAllString(s, "\n")
	s = mdTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	var out []string
	blank := true // no blank line first, nor two in a row
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		head := false
		switch {
		case line == "":
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		case mdTableRule.MatchString(line) && strings.Contains(line, "-"):
			continue
		case mdHeading.MatchString(line):
			line, head = mdHeading.ReplaceAllString(line, ""), true
		case mdBullet.MatchString(line):
			line = "• " + mdBullet.ReplaceAllString(line, "")
		case strings.HasPrefix(line, ">"):
			line = "│ " + strings.TrimSpace(strings.TrimLeft(line, ">"))
		case strings.HasPrefix(line, "|"):
			line = tableRow(line)
		}
		line = strings.Join(strings.Fields(mdEmphasis.Replace(line)), " ")
		line = strings.TrimSuffix(line, " ·")
		if line == "" || line == "•" || line == "▸" {
			continue
		}
		for _, l := range wrapText(line, width, 0) {
			if head {
				l = headStyle.Render(l)
			}
			out = append(out, l)
		}
		blank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// tableRow is a Markdown table's row as its cells, with dots between.
func tableRow(line string) string {
	var cells []string
	for _, c := range strings.Split(strings.Trim(line, "|"), "|") {
		if c = strings.TrimSpace(c); c != "" {
			cells = append(cells, c)
		}
	}
	return strings.Join(cells, " · ")
}
