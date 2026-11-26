package review

// change.go holds what the selected worktree's change is measured against,
// and loading it into the diff pane in each of its views.

import (
	"bytes"
	"errors"
	"io/fs"
	"time"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// comparison is what the selected worktree's change is measured against,
// by the base-diff mode. Everything that lists, diffs or counts the change
// goes through it, so they cannot disagree.
func (m *Model) comparison() git.Comparison {
	c := git.Comparison{Worktree: m.worktrees[m.wtIdx].Path}
	if m.showBase && m.baseRef != "" {
		c.Base = m.baseRef
	}
	return c
}

func (m *Model) originalPath(path string) string {
	for _, f := range m.files {
		if f.Path == path && f.OldPath != "" {
			return f.OldPath
		}
	}
	return path
}

// cycleDiffMode is v: the diff, then the whole file with the changes
// marked, then the old code, and round to the diff again.
func (m *Model) cycleDiffMode() tea.Cmd {
	switch m.diffMode {
	case diffModeHunk:
		m.diffMode = diffModeNewFull
		m.setToast("the whole file, changes marked · v the old code")
	case diffModeNewFull:
		m.diffMode = diffModeOld
		m.setToast("the old code · v the diff")
	default:
		m.diffMode = diffModeHunk
		m.setToast("the diff · v the whole file")
	}
	return m.loadDiff()
}

// toggleBase switches between working-tree changes and diff against the base
// branch.
func (m *Model) toggleBase() tea.Cmd {
	if m.baseRef == "" {
		m.setToast("no base branch (main/master) found")
		return nil
	}
	m.showBase = !m.showBase
	// A refresh begun before is of the other comparison, and is
	// overtaken. The diff is read again when the list comes.
	cmd := m.loadFiles()
	if m.showBase {
		m.setToast("showing changes against " + m.baseRef)
	} else {
		m.setToast("showing worktree changes")
	}
	return cmd
}

// loadDiff reads the diff of the file shown, or of the file peeked at, in
// the background, and puts it in place when it comes (onDiff). Until then
// the pane keeps what it had, with a spinner on its title once it takes a
// while (diffSlowMsg).
func (m *Model) loadDiff() tea.Cmd {
	m.diffToken++ // a load in the background is overtaken
	m.diffSel = lineSpan{}
	path, ok := m.currentFile()
	if m.peek != nil {
		path, ok = m.peek.file, true
	}
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) || !ok {
		m.diff = ""
		m.diffLines = nil
		m.diffAim = nil
		m.diffPending, m.diffSlow = false, false
		m.symbols, m.symbolRefs = nil, nil
		m.analysisIncomplete = ""
		return nil
	}
	r := diffReq{
		src: m.src, wt: m.worktrees[m.wtIdx].Path, path: path,
		token: m.diffToken, peek: m.peek != nil, relations: m.projectVer != 0,
		scheme: curScheme,
	}
	if !r.peek {
		r.c, r.mode = m.comparison(), m.diffMode
		r.untracked, r.original = m.untracked(path), m.originalPath(path)
	}
	m.diffPending = true
	token := m.diffToken
	return tea.Batch(func() tea.Msg { return r.read() },
		tea.Tick(diffSlowAfter, func(time.Time) tea.Msg { return diffSlowMsg{token} }))
}

// diffSlowAfter is how long a diff may take before its title says it is
// on its way.
var diffSlowAfter = 150 * time.Millisecond

// diffSlowMsg is a diff load, token, taking a while.
type diffSlowMsg struct{ token int }

// diffAim is where the diff cursor goes when the diff being loaded comes:
// to a line of the file (line > 0), or else to row cursor with the view's
// top at top.
type diffAim struct {
	line        int
	cursor, top int
}

// diffReq is what loadDiff reads, taken from the model so the read runs
// without it.
type diffReq struct {
	src       Source
	c         git.Comparison
	wt, path  string
	original  string // path before a rename, or path
	untracked bool
	mode      diffMode
	peek      bool // path is a file peeked at, shown as it is
	relations bool // the worktree's code has been read, for its symbols
	token     int
	scheme    diffScheme // the colours the diff is drawn in, as they were asked for
}

// read is the diff and its source.
func (r diffReq) read() diffMsg {
	if r.scheme.syntax == nil {
		r.scheme = diffSchemes[DiffColors[0]] // a request that names none
	}
	msg := diffMsg{wt: r.wt, path: r.path, token: r.token, peek: r.peek, scheme: r.scheme}
	if r.relations {
		msg.symbols, msg.refs, msg.incomplete = r.src.Relations(r.wt, r.path)
	}
	if r.peek {
		msg.diff, msg.err = r.src.ReadFile(r.wt, r.path)
	} else {
		msg.diff, msg.err = r.fileDiff()
		if r.mode == diffModeOld {
			src, err := r.src.OldSource(r.c, r.original)
			msg.src, msg.srcOK = src, err == nil
			// The old file is shown on the new side, to be coloured and
			// opened by; its numbers are its own, in one column.
			msg.oneSide = !r.untracked
		} else {
			src, err := r.src.ReadFile(r.wt, r.path)
			msg.src, msg.srcOK = src, err == nil
			// The whole new file's numbers are its own: the change's
			// removed lines are not in it to count the old side by.
			msg.oneSide = r.mode == diffModeNewFull && !r.untracked
			if r.scheme.syntaxChanged && !r.untracked && msg.err == nil && hasRemovedLines(msg.diff) && lexers.Match(r.path) != nil {
				// The removed lines' code is coloured from the file before.
				// Read only when there are some to colour: a deleted
				// binary file's diff has none, and the file could be huge.
				msg.old, _ = r.src.OldSource(r.c, r.original)
			}
			msg.srcGone = errors.Is(err, fs.ErrNotExist)
		}
	}
	if msg.err == nil {
		// Here, off the UI's goroutine: parsing, word-diffing and chroma
		// tokenising a large file takes long enough to freeze the screen.
		msg.lines, msg.built = msg.diffLines(), true
	}
	return msg
}

// fileDiff is the diff pane's content for the file, by the base-diff mode
// and the view. A file not yet added has no prior version to diff
// against, so it is always shown as plain code, whatever the view.
func (r diffReq) fileDiff() (string, error) {
	change := func() (string, error) {
		return r.src.Diff(r.c, r.path, r.untracked, r.original)
	}
	if r.untracked {
		return change()
	}
	switch r.mode {
	case diffModeOld:
		return r.src.OldFile(r.c, r.original)
	case diffModeNewFull:
		raw, err := change()
		if err != nil {
			return "", err
		}
		return r.src.FullNewFile(r.wt, r.path, raw)
	default:
		return change()
	}
}

// diffMsg is the diff of path, and its source, read in the background.
type diffMsg struct {
	wt, path   string
	token      int
	peek       bool
	diff, src  string
	srcOK      bool
	old        string // the file before the change, when its removed lines are coloured by syntax
	oneSide    bool   // one whole file, the new or the old, numbered in one column
	scheme     diffScheme
	srcGone    bool              // the file is not there: deleted, and its src known empty
	symbols    []analysis.Symbol // what path declares, and where each is used
	refs       [][]analysis.Ref
	incomplete string
	err        error
	lines      []diffLine // diff as lines, highlighted, when built
	built      bool
}

// diffLines is the diff as the pane's lines, syntax- and word-highlighted,
// before the notes and references that hang on the model are put in.
func (msg diffMsg) diffLines() []diffLine {
	if msg.built {
		return msg.lines
	}
	if msg.peek {
		return plainSourceLines(msg.scheme.syntax, msg.path, msg.diff)
	}
	lines := builtDiffLines(msg.path, msg.diff, msg.src, msg.srcOK, msg.old, msg.scheme)
	if msg.oneSide {
		for i := range lines {
			lines[i].oldLine = 0
		}
	}
	return lines
}

// onDiff puts a diff read in the background in place, if it is still the
// file shown.
func (m *Model) onDiff(msg diffMsg) {
	path, ok := m.currentFile()
	if m.peek != nil {
		path, ok = m.peek.file, true
	}
	if msg.token != m.diffToken || msg.wt != m.curWT || !ok || path != msg.path || msg.peek != (m.peek != nil) {
		return
	}
	m.diffPending, m.diffSlow = false, false
	aim := m.diffAim
	m.diffAim = nil
	if msg.err != nil {
		m.err = msg.err
		m.diff, m.diffLines = "", nil
		return
	}
	m.diff = msg.diff
	// First: displayLines annotates the lines with them.
	m.symbols, m.symbolRefs = msg.symbols, msg.refs
	m.analysisIncomplete = msg.incomplete
	if msg.peek {
		m.diffLines = annotateWhyLines(msg.diffLines(), path, m.whyIndex)
	} else {
		m.diffLines = m.annotateLines(path, msg.diffLines())
	}
	m.followComments(msg, m.diffLines)
	m.commentsVer = m.comments.Version()
	m.diffLines = m.withComments(m.diffLines)
	m.diffSel = lineSpan{}
	switch {
	case aim == nil:
		// The same cursor, kept within the new content.
		m.setDiffCursor(m.diffCursor)
	case aim.line > 0:
		m.diffViewTop = 0 // fresh content: the old file's scroll means nothing
		m.setDiffCursor(m.diffRowForLine(aim.line))
	default:
		m.diffViewTop = aim.top
		m.setDiffCursor(aim.cursor)
	}
}

// displayLines turns the raw diff text for path into the lines shown in
// the diff pane: word-highlighted, syntax-highlighted on unmarked context
// lines from src, and annotated with symbol references. Computed once per
// load rather than per render frame, since chroma tokenisation of a full
// file is too expensive to redo on every keystroke, scroll or tick.
func (m *Model) displayLines(path, diff, src string, srcOK bool) []diffLine {
	return m.annotateLines(path, builtDiffLines(path, diff, src, srcOK, "", curScheme))
}

// builtDiffLines is the costly part of displayLines, which diffReq.read
// does in the background: it needs nothing of the model.
func builtDiffLines(path, diff, src string, srcOK bool, old string, s diffScheme) []diffLine {
	lines := buildDiffLinesIn(diff, s)
	// Each file is tokenised once, for all the lines that show it.
	var newToks [][]chroma.Token
	if srcOK {
		newToks = highlightTokens(path, src)
		lines = contextFromTokens(s.syntax, lines, newToks)
	}
	if s.syntaxChanged && hasKind(lines, '+', '-') {
		var oldToks [][]chroma.Token
		if old != "" && hasKind(lines, '-') {
			oldToks = highlightTokens(path, old)
		}
		lines = highlightChangedLines(lines, newToks, oldToks, s)
	}
	return lines
}

// hasKind is whether any of lines is of one of kinds.
func hasKind(lines []diffLine, kinds ...byte) bool {
	for _, l := range lines {
		if bytes.IndexByte(kinds, l.kind) >= 0 {
			return true
		}
	}
	return false
}

// hasRemovedLines is whether diff removes any line.
func hasRemovedLines(diff string) bool {
	for _, d := range git.ParseDiff(diff) {
		if d.Kind == '-' {
			return true
		}
	}
	return false
}

// annotateLines puts the symbol references and the notes in lines.
func (m *Model) annotateLines(path string, lines []diffLine) []diffLine {
	lines = annotateDiffLines(lines, m.symbols, m.symbolRefs, m.analysisIncomplete)
	return annotateWhyLines(lines, path, m.whyIndex)
}
