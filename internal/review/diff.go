package review

import (
	"fmt"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/why"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// diffLine is a single line of a unified diff, classified and optionally
// word-highlighted for added/removed lines.
type diffLine struct {
	text    string
	kind    byte // 'h' header, '@' hunk, '+' added, '-' removed, ' ' context, 'a' annotation, 'w' why
	hl      string
	newLine int // 1-based line number in the "new" side of the diff; 0 if not applicable
	oldLine int // 1-based line number in the "old" side of the diff; 0 if not applicable

	// changed is the byte ranges of an added or removed line's text, after
	// its sign, that the word diff marks as changed.
	changed [][2]int

	// refFile/refLine are the jump target for an 'a' annotation line (the
	// first reference it lists), so the user can select it and jump there.
	refFile string
	refLine int

	// whyDetail is the full rationale behind a 'w' line; whyMore marks a
	// line of it opened in the diff (see toggleWhyInline).
	whyDetail string
	whyMore   bool

	// comment is the user's comment a 'w' line is, by its ID; see
	// comments.go.
	comment int
}

// buildDiffLines parses a unified diff, applies word-level highlighting to
// added/removed lines within each run of changes, and tracks each line's position on
// the "new" side (needed to align syntax highlighting from a full-file
// tokenisation with the diff's context/added lines).
func buildDiffLines(diff string) []diffLine {
	return buildDiffLinesIn(diff, curScheme)
}

// buildDiffLinesIn is buildDiffLines in scheme s.
func buildDiffLinesIn(diff string, s diffScheme) []diffLine {
	// A diff ends in a line end, not in an empty line after it.
	diff = strings.TrimSuffix(printable(diff), "\n")
	raw := strings.Split(diff, "\n")
	lines := make([]diffLine, 0, len(raw))
	for i, d := range git.ParseDiff(diff) {
		if d.Kind == 'h' && gitHeader(raw[i]) {
			continue // the file's name is the pane's title; the rest is git's bookkeeping
		}
		lines = append(lines, diffLine{text: raw[i], kind: d.Kind, newLine: d.NewLine, oldLine: d.OldLine})
	}

	var removed, added []int
	flush := func() {
		applyWordHighlight(lines, removed, added, s)
		removed = nil
		added = nil
	}
	// Removed and added lines are paired within one run of changes, not
	// across a hunk: a context line between two runs means the lines on
	// either side of it are not edits of each other.
	for i, l := range lines {
		switch l.kind {
		case '-':
			if len(added) > 0 {
				flush() // a new run starts after the previous one's additions
			}
			removed = append(removed, i)
		case '+':
			added = append(added, i)
		default:
			flush()
		}
	}
	flush()
	return lines
}

// printable makes text safe to write to the terminal as one line per "\n".
// A control character reaching the terminal is obeyed, not shown: a
// carriage return or an escape sequence in a file moves the cursor, and
// what is drawn after it lands on the menu or the file list. A CRLF line
// ending loses its "\r"; any other control character but a tab is shown as
// its Unicode control picture (␍, ␛), one cell wide like the byte was
// meant to be. Line breaks are kept, so line numbers still line up.
func printable(s string) string {
	return term.Printable(s)
}

func printableLine(s string) string {
	return term.PrintableLine(s)
}

// gitHeader is whether line is one of the lines git starts a file's diff
// with — diff --git, index, --- and +++ — which say nothing the pane does
// not. "new file" and "deleted file" are kept: they do.
func gitHeader(line string) bool {
	for _, p := range []string{"diff ", "index ", "--- ", "+++ "} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// highlightContextLines applies chroma-based syntax colouring to unmarked
// (context) lines, using source (the full file content whose line numbers
// align with this diff — see diffReq.read) so that unchanged
// code isn't left monochrome and hard to read next to the coloured
// added/removed lines. Added/removed lines keep their existing diff
// colouring untouched.
func highlightContextLines(lines []diffLine, path, source string) []diffLine {
	return highlightContextLinesIn(syntaxStyle, lines, path, source)
}

// highlightContextLinesIn is highlightContextLines in chroma style st.
func highlightContextLinesIn(st *chroma.Style, lines []diffLine, path, source string) []diffLine {
	return contextFromTokens(st, lines, highlightTokens(path, source))
}

// contextFromTokens colours the context lines from toks, the new file's
// tokens, drawing only the lines the diff shows.
func contextFromTokens(st *chroma.Style, lines []diffLine, toks [][]chroma.Token) []diffLine {
	if toks == nil {
		return lines
	}
	out := make([]diffLine, len(lines))
	copy(out, lines)
	for i, l := range out {
		if l.kind != ' ' || l.newLine <= 0 || l.newLine > len(toks) {
			continue
		}
		out[i].hl = " " + renderTokens(st, toks[l.newLine-1])
	}
	return out
}

// annotateWhyLines inserts a one-line rationale above the code an agent
// left a note for (see internal/why). Notes are deliberately selective —
// only changes whose reason isn't apparent from the code get one — so most
// lines get nothing, and a ✎ line means there is something worth reading.
//
// The note goes directly above the line its anchor names, rather than above
// the hunk. That puts the reason on the code it explains, and it is what
// makes a note outlive its change: after a commit there are no hunks left,
// but opening the file still shows the line, and so the note.
func annotateWhyLines(lines []diffLine, file string, ix why.Index) []diffLine {
	if ix.Len() == 0 || file == "" {
		return lines
	}

	whyAt := map[int]why.Note{}
	shown := map[string]bool{}
	for i, l := range lines {
		switch l.kind {
		case '+', ' ':
		default:
			continue
		}
		text := strings.TrimPrefix(strings.TrimPrefix(l.text, "+"), " ")
		n, ok := ix.Match(file, text)
		if !ok || shown[n.Anchor] {
			continue // one ✎ per note, on the first line it matches
		}
		shown[n.Anchor] = true
		whyAt[i] = n
	}

	if len(whyAt) == 0 {
		return lines
	}
	out := make([]diffLine, 0, len(lines)+len(whyAt))
	for i, l := range lines {
		if e, ok := whyAt[i]; ok {
			out = append(out, diffLine{
				kind:      'w',
				text:      "  ✎ " + printableLine(e.Short),
				whyDetail: formatWhyDetail(e),
			})
		}
		out = append(out, l)
	}
	return out
}

// formatWhyDetail lays out a note to open in the diff, falling back to
// the short line when the agent didn't write a longer explanation.
func formatWhyDetail(n why.Note) string {
	var b strings.Builder
	if by := strings.TrimSpace(strings.Join([]string{n.Agent, n.Time}, " · ")); by != "·" && by != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.Trim(by, " ·"))
	}
	b.WriteString(n.Short)
	if strings.TrimSpace(n.Detail) != "" {
		b.WriteString("\n\n")
		b.WriteString(n.Detail)
	}
	return b.String()
}

// plainSourceLines renders a file's full content as unmarked, syntax-
// highlighted diff lines — used to "peek" at a file that isn't part of the
// current diff (e.g. a jump target outside the changed-file tree).
func plainSourceLines(st *chroma.Style, path, source string) []diffLine {
	source = printable(source)
	src := strings.Split(source, "\n")
	if len(src) > 0 && src[len(src)-1] == "" {
		src = src[:len(src)-1]
	}
	colored := highlightSourceIn(st, path, source)
	lines := make([]diffLine, len(src))
	for i, l := range src {
		dl := diffLine{text: " " + l, kind: ' ', newLine: i + 1}
		if colored != nil && i < len(colored) {
			dl.hl = " " + colored[i]
		}
		lines[i] = dl
	}
	return lines
}

// maxShownRefAnnotations caps how many individual reference rows are
// inserted per symbol — each is its own selectable/jumpable line, so a
// symbol with many callers doesn't flood the diff pane.
const maxShownRefAnnotations = 6

// annotateDiffLines inserts "referenced by" lines right after each added
// line that declares an exported symbol with known references elsewhere, so
// a reviewer sees the blast radius of a changed declaration inline instead
// of having to switch to the separate symbol tab. Each reference gets its
// own line (not one line listing several) so every one of them is
// individually selectable and jumpable — a symbol can be used from more
// than one place.
func annotateDiffLines(lines []diffLine, symbols []analysis.Symbol, refs [][]analysis.Ref, incomplete ...string) []diffLine {
	partial := len(incomplete) > 0 && incomplete[0] != ""
	if partial {
		lines = append([]diffLine{{kind: 'a', text: "    ↳ Reference analysis incomplete: " + incomplete[0]}}, lines...)
	}
	if len(symbols) == 0 {
		return lines
	}
	refByName := make(map[string][]analysis.Ref, len(symbols))
	for i, s := range symbols {
		refByName[s.Name] = refs[i]
	}

	out := make([]diffLine, 0, len(lines))
	seen := map[string]bool{}
	for _, l := range lines {
		out = append(out, l)
		if l.kind != '+' {
			continue
		}
		name, _, ok := analysis.ParseSymbolDecl(strings.TrimPrefix(l.text, "+"))
		if !ok || seen[name] {
			continue
		}
		rs, ok := refByName[name]
		if !ok {
			continue
		}
		seen[name] = true
		out = append(out, refAnnotationLines(name, rs, partial)...)
	}
	return out
}

// refAnnotationLines builds one jumpable diffLine per reference (up to
// maxShownRefAnnotations), each carrying its own refFile/refLine target.
func refAnnotationLines(name string, refs []analysis.Ref, partial bool) []diffLine {
	if len(refs) == 0 {
		if partial {
			return []diffLine{{kind: 'a', text: "    ↳ " + name + ": no references found (analysis incomplete)"}}
		}
		return []diffLine{{kind: 'a', text: "    ↳ " + name + ": no other references"}}
	}
	header := fmt.Sprintf("    ↳ %s used (%d):", name, len(refs))
	if partial {
		header = fmt.Sprintf("    ↳ %s used (at least %d; analysis incomplete):", name, len(refs))
	}
	out := []diffLine{{kind: 'a', text: header}}

	shown := refs
	more := 0
	if len(shown) > maxShownRefAnnotations {
		shown = shown[:maxShownRefAnnotations]
		more = len(refs) - maxShownRefAnnotations
	}
	for _, r := range shown {
		out = append(out, diffLine{
			kind:    'a',
			text:    fmt.Sprintf("        • %s:%d", r.File, r.Line),
			refFile: r.File,
			refLine: r.Line,
		})
	}
	if more > 0 {
		out = append(out, diffLine{kind: 'a', text: fmt.Sprintf("        (+%d more not shown)", more)})
	}
	return out
}

// wordDiffMax is the longest line, in bytes, whose changed words are
// marked: past it, a minified file's line, the character diff is slow
// and the marks no help. The row tint still says the line changed.
const wordDiffMax = 2048

// wordDiffTimeout is how long the character diff of one pair of lines may
// take. diffmatchpatch's own default is a second, a second per pair, and
// a diff of many pairs then hangs the pane while it is read.
const wordDiffTimeout = 50 * time.Millisecond

func applyWordHighlight(lines []diffLine, removed, added []int, s diffScheme) {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = wordDiffTimeout
	n := min(len(removed), len(added))
	for i := 0; i < n; i++ {
		rIdx, aIdx := removed[i], added[i]
		rText := strings.TrimPrefix(lines[rIdx].text, "-")
		aText := strings.TrimPrefix(lines[aIdx].text, "+")
		if len(rText) > wordDiffMax || len(aText) > wordDiffMax {
			continue
		}
		diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(rText, aText, false))
		if !worthWordHighlight(diffs, rText, aText) {
			continue // the row tint alone says the line changed
		}
		lines[rIdx].hl = renderInline(diffs, true, s)
		lines[aIdx].hl = renderInline(diffs, false, s)
		lines[rIdx].changed, lines[aIdx].changed = changedRanges(diffs)
	}
}

// worthWordHighlight reports whether marking the changed words within a
// line pair helps. A character diff of two lines that were rewritten rather
// than edited matches scattered letters — "hunk" against "line" shares an
// n — and marking those turns the line into confetti that is harder to read
// than no marks at all. Only a pair that is mostly the same text gets them.
func worthWordHighlight(diffs []diffmatchpatch.Diff, removed, added string) bool {
	same := 0
	for _, d := range diffs {
		if d.Type == diffmatchpatch.DiffEqual {
			same += len(strings.TrimSpace(d.Text))
		}
	}
	longest := max(len(strings.TrimSpace(removed)), len(strings.TrimSpace(added)))
	return longest > 0 && same*2 >= longest
}

// changedRanges is where diffs marks the removed line's words and the
// added line's, as byte ranges of each.
func changedRanges(diffs []diffmatchpatch.Diff) (removed, added [][2]int) {
	r, a := 0, 0
	for _, d := range diffs {
		n := len(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			r, a = r+n, a+n
		case diffmatchpatch.DiffDelete:
			removed = append(removed, [2]int{r, r + n})
			r += n
		case diffmatchpatch.DiffInsert:
			added = append(added, [2]int{a, a + n})
			a += n
		}
	}
	return removed, added
}

// highlightChangedLines colours the code of added and removed lines by its
// syntax too, on their tint, as GitHub does: added lines from src, the
// new file, and removed ones from old, the file before. A line whose text
// is not its file's line, as when the file was read after the diff, keeps
// the tint's own colour.
func highlightChangedLines(lines []diffLine, newToks, oldToks [][]chroma.Token, s diffScheme) []diffLine {
	if newToks == nil && oldToks == nil {
		return lines
	}
	out := make([]diffLine, len(lines))
	copy(out, lines)
	for i, l := range out {
		toks, n := newToks, l.newLine
		line, word := s.add, s.wordAdd
		switch l.kind {
		case '+':
		case '-':
			toks, n = oldToks, l.oldLine
			line, word = s.del, s.wordDel
		default:
			continue
		}
		if n <= 0 || n > len(toks) {
			continue
		}
		text := l.text[1:]
		var joined strings.Builder
		for _, t := range toks[n-1] {
			joined.WriteString(t.Value)
		}
		if joined.String() != text {
			continue
		}
		out[i].hl = tintedSyntax(s.syntax, toks[n-1], l.changed, line, word)
	}
	return out
}

// tintedSyntax draws a line's tokens in their syntax colours on line's
// background, and the bytes in changed on word's.
func tintedSyntax(st *chroma.Style, toks []chroma.Token, changed [][2]int, line, word lipgloss.Style) string {
	var b strings.Builder
	pos := 0
	for _, t := range toks {
		fg := syntaxStyleOf(st, t.Type)
		if _, unset := fg.GetForeground().(lipgloss.NoColor); unset {
			fg = fg.Foreground(line.GetForeground())
		}
		v := t.Value
		for v != "" {
			// The run up to the next edge of a changed range.
			in, end := false, pos+len(v)
			for _, c := range changed {
				switch {
				case pos >= c[0] && pos < c[1]:
					in, end = true, min(end, c[1])
				case c[0] > pos:
					end = min(end, c[0])
				}
			}
			bg := line
			if in {
				bg = word
			}
			b.WriteString(fg.Background(bg.GetBackground()).Render(v[:end-pos]))
			v, pos = v[end-pos:], end
		}
	}
	return b.String()
}

func renderInline(diffs []diffmatchpatch.Diff, isRemoved bool, s diffScheme) string {
	var b strings.Builder
	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			if isRemoved {
				b.WriteString(s.del.Render(d.Text))
			} else {
				b.WriteString(s.add.Render(d.Text))
			}
		case diffmatchpatch.DiffDelete:
			if isRemoved {
				b.WriteString(s.wordDel.Render(d.Text))
			}
		case diffmatchpatch.DiffInsert:
			if !isRemoved {
				b.WriteString(s.wordAdd.Render(d.Text))
			}
		}
	}
	return b.String()
}
