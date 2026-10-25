package review

import (
	"strings"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// syntaxStyle is the colour palette used for syntax highlighting unmarked
// (context) code, chosen for readability on a dark terminal background —
// the common case for a developer's terminal.
// It follows the diff's colours; see colors.go.
var syntaxStyle = styles.Get("monokai")

var syntaxStyleCache = map[chroma.TokenType]lipgloss.Style{}

func syntaxLipglossStyle(t chroma.TokenType) lipgloss.Style {
	if s, ok := syntaxStyleCache[t]; ok {
		return s
	}
	entry := syntaxStyle.Get(t)
	s := lipgloss.NewStyle()
	if entry.Colour.IsSet() {
		s = s.Foreground(lipgloss.Color(entry.Colour.String()))
	}
	if entry.Bold == chroma.Yes {
		s = s.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		s = s.Italic(true)
	}
	syntaxStyleCache[t] = s
	return s
}

// maxHighlightSize caps how large a file we'll bother tokenising; beyond
// this it's not worth the cost and we fall back to plain text.
const maxHighlightSize = 2 * 1024 * 1024

// highlightSource returns source split into lines with chroma-based syntax
// colouring applied (like a code editor), or nil if the file's language
// isn't recognised, unsupported, or the content is too large.
func highlightSource(path, source string) []string {
	source = printable(source)
	if len(source) == 0 || len(source) > maxHighlightSize {
		return nil
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		return nil
	}
	lexer = chroma.Coalesce(lexer)
	tokens, err := chroma.Tokenise(lexer, nil, source)
	if err != nil {
		return nil
	}

	byLine := chroma.SplitTokensIntoLines(tokens)
	out := make([]string, len(byLine))
	for i, toks := range byLine {
		var b strings.Builder
		for _, t := range toks {
			v := strings.TrimSuffix(t.Value, "\n")
			if v == "" {
				continue
			}
			b.WriteString(syntaxLipglossStyle(t.Type).Render(v))
		}
		out[i] = b.String()
	}
	return out
}
