package review

import (
	"strings"
	"sync"

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

// syntaxCache is the lipgloss style of each token type, per chroma style.
// Diffs are coloured in the background, several at once, so it is
// locked; a chroma style's colours never change, so it is never cleared.
var syntaxCache struct {
	sync.Mutex
	m map[*chroma.Style]map[chroma.TokenType]lipgloss.Style
}

// syntaxLipglossStyle is token type t in the diff's colours now. Only on
// the UI's goroutine: a diff read in the background uses its request's.
func syntaxLipglossStyle(t chroma.TokenType) lipgloss.Style {
	return syntaxStyleOf(syntaxStyle, t)
}

// syntaxStyleOf is token type t in chroma style st.
func syntaxStyleOf(st *chroma.Style, t chroma.TokenType) lipgloss.Style {
	syntaxCache.Lock()
	defer syntaxCache.Unlock()
	if s, ok := syntaxCache.m[st][t]; ok {
		return s
	}
	entry := st.Get(t)
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
	if syntaxCache.m == nil {
		syntaxCache.m = map[*chroma.Style]map[chroma.TokenType]lipgloss.Style{}
	}
	if syntaxCache.m[st] == nil {
		syntaxCache.m[st] = map[chroma.TokenType]lipgloss.Style{}
	}
	syntaxCache.m[st][t] = s
	return s
}

// maxHighlightSize caps how large a file we'll bother tokenising; beyond
// this it's not worth the cost and we fall back to plain text.
const maxHighlightSize = 2 * 1024 * 1024

// highlightSource returns source split into lines with chroma-based syntax
// colouring applied (like a code editor), or nil if the file's language
// isn't recognised, unsupported, or the content is too large.
func highlightSource(path, source string) []string {
	return highlightSourceIn(syntaxStyle, path, source)
}

// highlightSourceIn is highlightSource in chroma style st.
func highlightSourceIn(st *chroma.Style, path, source string) []string {
	byLine := highlightTokens(path, source)
	if byLine == nil {
		return nil
	}
	out := make([]string, len(byLine))
	for i, toks := range byLine {
		out[i] = renderTokens(st, toks)
	}
	return out
}

// renderTokens is one line's tokens in chroma style st.
func renderTokens(st *chroma.Style, toks []chroma.Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(syntaxStyleOf(st, t.Type).Render(t.Value))
	}
	return b.String()
}

// highlightTokens is source's lines as chroma's tokens, without their line
// ends, or nil as for highlightSource.
func highlightTokens(path, source string) [][]chroma.Token {
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
	out := make([][]chroma.Token, len(byLine))
	for i, toks := range byLine {
		for _, t := range toks {
			if v := strings.TrimSuffix(t.Value, "\n"); v != "" {
				out[i] = append(out[i], chroma.Token{Type: t.Type, Value: v})
			}
		}
	}
	return out
}
