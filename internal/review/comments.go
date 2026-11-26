package review

// comments.go is the user's own comments on lines of the change, as on a
// pull request before it is merged: r writes one on the line under the
// cursor, or on the lines chosen, and it is shown above them, » in the
// diff, the way an agent's note (✎) and a reviewer's finding (⚑) are. R
// sends all the worktree's comments to its agent in one message, which
// nerdr opens to be read and changed, and queues for when the agent is
// idle. They are kept until they are sent: in a linecomments.Store that
// all of nerdr's panels share, in nerdr's state folder on the machine the
// panel runs on, since they are the user's and not the repository's.

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/linecomments"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// commentMark is what a comment's row starts with, and what marks a file
// with comments in the list.
const commentMark = "»"

// CommentsSentMsg is what nerdr hands back once the message R prepared
// has been queued for the agent: the comments in it, by ID with the text
// each was sent with, are then no longer pending. Until it comes they are
// kept, so a message cancelled loses none of them, and one changed since
// is kept as it is now.
type CommentsSentMsg struct{ Sent map[int]string }

// SetComments gives the panel the store its comments are kept in, shared
// with nerdr's other panels. Without one, r has nowhere to keep them.
func (m Model) SetComments(s *linecomments.Store) Model {
	m.comments = s
	m.commentsVer = -1 // drawn at the next update
	return m
}

// commentDraft is the comment being written: a new one at c's place, or
// comment edit's new text.
type commentDraft struct {
	edit int
	c    linecomments.Comment
}

// shownFile is the file whose lines the diff shows: the one selected, or
// the one peeked at.
func (m *Model) shownFile() (string, bool) {
	if m.peek != nil {
		return m.peek.file, true
	}
	return m.currentFile()
}

// curWorktree is the path of the worktree shown, or "".
func (m *Model) curWorktree() string {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return ""
	}
	return m.worktrees[m.wtIdx].Path
}

// commentMachine is the machine the worktrees shown are on; see
// Pin.Machine.
func (m *Model) commentMachine() string {
	if len(m.pins) == 0 {
		return ""
	}
	return m.pins[0].Machine
}

// pendingComments are the comments on the worktree shown.
func (m *Model) pendingComments() []linecomments.Comment {
	wt := m.curWorktree()
	if wt == "" {
		return nil
	}
	return m.comments.For(m.commentMachine(), wt)
}

// commentCounts is how many comments each file of the worktree shown has.
func (m *Model) commentCounts() map[string]int {
	out := map[string]int{}
	for _, c := range m.pendingComments() {
		out[c.File]++
	}
	return out
}

// commentAt is the comment whose row idx is in the diff, if it is one.
func (m *Model) commentAt(idx int) (linecomments.Comment, bool) {
	if idx < 0 || idx >= len(m.diffLines) || m.diffLines[idx].comment == 0 {
		return linecomments.Comment{}, false
	}
	id := m.diffLines[idx].comment
	for _, c := range m.pendingComments() {
		if c.ID == id {
			return c, true
		}
	}
	return linecomments.Comment{}, false
}

// startComment is r: a new comment on the lines chosen (sel) or the line
// under the cursor, or on its row, the comment's own text to change.
func (m *Model) startComment(sel lineSpan) tea.Cmd {
	if m.comments == nil {
		return nil
	}
	if m.focus != focusDiff {
		m.setToast("comments go on lines: → to the diff, then r")
		return nil
	}
	if c, ok := m.commentAt(m.diffCursor); ok {
		return m.editComment(c)
	}
	if m.diffMode == diffModeOld {
		m.setToast("comments go on the change: v for the diff or the whole file")
		return nil
	}
	if m.diffPending {
		// The rows are still the last file's: its code is not this one's.
		m.setToast("the diff is still loading")
		return nil
	}
	file, ok := m.shownFile()
	if !ok || m.curWorktree() == "" {
		return nil
	}
	from, to := m.diffCursor, m.diffCursor
	if sel.on {
		from, to = min(sel.from, sel.to), max(sel.from, sel.to)
	}
	var rows []diffLine
	for i := max(from, 0); i <= to && i < len(m.diffLines); i++ {
		// Only lines of the code, with their place: not a note that the
		// file is binary, or git's own lines outside a hunk.
		switch dl := m.diffLines[i]; {
		case dl.comment != 0:
		case (dl.kind == '+' || dl.kind == ' ') && dl.newLine > 0, dl.kind == '-' && dl.oldLine > 0:
			rows = append(rows, dl)
		}
	}
	// Removed lines with others are left out: the comment is on the code
	// as it is now. Only removed lines alone are commented on as such.
	removed := len(rows) > 0
	for _, r := range rows {
		removed = removed && r.kind == '-'
	}
	c := linecomments.Comment{Machine: m.commentMachine(), Worktree: m.curWorktree(), File: file, Removed: removed}
	prev := 0
	for _, r := range rows {
		if r.kind == '-' && !removed {
			continue
		}
		// One run of lines, as they follow in the file: across hunks, the
		// rows chosen leave lines out between them.
		at := r.newLine
		if removed {
			at = r.oldLine
		}
		if prev != 0 && at != prev+1 {
			m.setToast("comment on lines that follow each other, within one hunk")
			return nil
		}
		prev = at
		if len(c.Code) == 0 {
			c.Line, c.OldLine = r.newLine, r.oldLine
		}
		c.Code = append(c.Code, codeOf(r))
	}
	if removed {
		c.Line = 0
	}
	if len(c.Code) == 0 {
		m.setToast("select a line of code to comment on")
		return nil
	}
	// All of it: the store quotes the first linecomments.MaxCode lines,
	// and keeps a hash of the whole to tell it is all still there.
	return m.openCommentInput(commentDraft{c: c}, "")
}

// codeOf is a line of the diff as it is in the file: without its sign.
func codeOf(dl diffLine) string {
	text := dl.text
	if text != "" && strings.IndexByte("+- ", text[0]) >= 0 {
		text = text[1:]
	}
	return text
}

// editComment opens c's text to be changed.
func (m *Model) editComment(c linecomments.Comment) tea.Cmd {
	return m.openCommentInput(commentDraft{edit: c.ID, c: c}, c.Text)
}

func (m *Model) openCommentInput(d commentDraft, text string) tea.Cmd {
	in := textinput.New()
	in.Prompt = commentMark + " " + printableLine(d.c.Where()) + ": "
	in.Placeholder = "your comment · enter keeps it · esc cancels"
	in.CharLimit = linecomments.MaxText
	in.Width = max(m.width-lipgloss.Width(in.Prompt)-2, 20)
	in.SetValue(text)
	m.commentInput = in
	m.commentDraft = &d
	m.commenting = true
	m.setDiffCursor(m.diffCursor) // the status row took a row from the diff
	return m.commentInput.Focus()
}

// handleCommentKey is a key while a comment is written.
func (m Model) handleCommentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeCommentInput()
		return m, nil
	case "enter":
		d := *m.commentDraft
		text := strings.TrimSpace(m.commentInput.Value())
		m.closeCommentInput()
		m.saveComment(d, text)
		return m, nil
	}
	var cmd tea.Cmd
	m.commentInput, cmd = m.commentInput.Update(msg)
	return m, cmd
}

func (m *Model) closeCommentInput() {
	m.commenting = false
	m.commentDraft = nil
	m.commentInput.Blur()
	m.setDiffCursor(m.diffCursor)
}

// saveComment keeps what d's comment says, text: a new comment, or the
// new text of one; an empty text deletes one.
func (m *Model) saveComment(d commentDraft, text string) {
	var err error
	switch {
	case d.edit != 0 && text == "":
		err = m.comments.Delete(d.edit)
		m.setToast("comment deleted")
	case d.edit != 0:
		err = m.comments.SetText(d.edit, text)
		m.setToast("comment changed")
	case text == "":
		return
	default:
		d.c.Text = text
		_, err = m.comments.Add(d.c)
		n := len(m.pendingComments())
		m.setToast(fmt.Sprintf("comment kept · %s", sendHint(n)))
	}
	if err != nil {
		m.setToast("comment not kept: " + err.Error())
	}
	m.placeComments()
}

func sendHint(n int) string {
	if n == 1 {
		return "R sends it to the agent"
	}
	return fmt.Sprintf("R sends all %d to the agent", n)
}

// deleteComment is x on a comment's row.
func (m *Model) deleteComment() {
	if m.focus != focusDiff {
		return
	}
	c, ok := m.commentAt(m.diffCursor)
	if !ok {
		m.setToast("x deletes a comment: put the cursor on its » row")
		return
	}
	if err := m.comments.Delete(c.ID); err != nil {
		m.setToast("comment not deleted: " + err.Error())
	} else {
		m.setToast("comment deleted")
	}
	m.placeComments()
}

// syncComments draws the comments again when the store has changed, in
// this panel or another.
func (m *Model) syncComments() {
	if m.comments == nil {
		return
	}
	if v := m.comments.Version(); v != m.commentsVer {
		m.placeComments()
	}
}

// followComments finds the shown file's comments again in the content
// just loaded, so they stay on their code as the agent changes the file,
// or are marked outdated when it is gone. lines are the diff's.
func (m *Model) followComments(msg diffMsg, lines []diffLine) {
	if m.comments == nil || (!msg.peek && m.diffMode == diffModeOld) {
		return
	}
	src := msg.src
	if msg.peek {
		src = msg.diff
	}
	// Nothing to do, most often: the file has no comments.
	has := false
	for _, c := range m.comments.For(m.commentMachine(), msg.wt) {
		has = has || c.File == msg.path
	}
	if !has {
		return
	}
	// Only the diff itself has the removed lines: the whole file does
	// not, and a comment on one is not outdated for that.
	withRemoved := !msg.peek && m.diffMode == diffModeHunk
	m.comments.Follow(m.commentMachine(), msg.wt, msg.path, linecomments.Seen{
		Src: src, SrcOK: msg.peek || msg.srcOK || msg.srcGone,
		Old: oldSide(lines), OldOK: withRemoved,
	})
}

// oldSide is the lines the diff removes, by their numbers in the file
// before the change.
func oldSide(lines []diffLine) map[int]string {
	out := map[int]string{}
	for _, dl := range lines {
		if dl.kind == '-' && dl.oldLine > 0 {
			out[dl.oldLine] = codeOf(dl)
		}
	}
	return out
}

// placeComments puts the shown file's comments in the diff, above the
// lines they are on, keeping the cursor on the row it was on.
func (m *Model) placeComments() {
	m.commentsVer = m.comments.Version()
	// The cursor's row, by its place among the rows that are not
	// comments: a comment's own row is the line it is above.
	onComment := 0
	code := 0
	for i := 0; i < m.diffCursor && i < len(m.diffLines); i++ {
		if m.diffLines[i].comment == 0 {
			code++
		}
	}
	if m.diffCursor >= 0 && m.diffCursor < len(m.diffLines) {
		onComment = m.diffLines[m.diffCursor].comment
	}
	var lines []diffLine
	for _, dl := range m.diffLines {
		if dl.comment == 0 {
			lines = append(lines, dl)
		}
	}
	m.diffLines = m.withComments(lines)
	cursor, seen := len(m.diffLines)-1, 0
	for i, dl := range m.diffLines {
		if onComment != 0 && dl.comment == onComment {
			cursor = i
			break
		}
		if dl.comment != 0 {
			continue
		}
		if seen == code {
			cursor = i
			break
		}
		seen++
	}
	m.diffSel = lineSpan{}
	m.setDiffCursor(cursor)
}

// withComments is lines with the shown file's comments put in: above the
// line each is on, or at the top for those whose line the diff does not
// show, and those outdated.
func (m *Model) withComments(lines []diffLine) []diffLine {
	file, ok := m.shownFile()
	if !ok || m.comments == nil || (m.peek == nil && m.diffMode == diffModeOld) {
		return lines
	}
	var cs []linecomments.Comment
	for _, c := range m.pendingComments() {
		if c.File == file {
			cs = append(cs, c)
		}
	}
	if len(cs) == 0 {
		return lines
	}
	above := map[int][]linecomments.Comment{}
	var top []linecomments.Comment
	for _, c := range cs {
		if i, ok := commentRow(lines, c); ok {
			above[i] = append(above[i], c)
		} else {
			top = append(top, c)
		}
	}
	out := make([]diffLine, 0, len(lines)+len(cs))
	for _, c := range top {
		out = append(out, commentLine(c, true))
	}
	for i, l := range lines {
		for _, c := range above[i] {
			out = append(out, commentLine(c, false))
		}
		out = append(out, l)
	}
	return out
}

// commentRow is the row of lines c goes above: the one of its line, with
// its code on it.
func commentRow(lines []diffLine, c linecomments.Comment) (int, bool) {
	if c.Outdated {
		return 0, false
	}
	if c.Removed {
		n, ok := linecomments.LocateOld(c, oldSide(lines))
		if !ok {
			return 0, false
		}
		for i, dl := range lines {
			if dl.kind == '-' && dl.oldLine == n {
				return i, true
			}
		}
		return 0, false
	}
	for i, dl := range lines {
		if (dl.kind == '+' || dl.kind == ' ') && dl.newLine == c.Line && linecomments.Same(codeOf(dl), c.Code[0]) {
			return i, true
		}
	}
	return 0, false
}

// commentLine is c's row in the diff; top for one not above its line,
// which says where it is instead.
func commentLine(c linecomments.Comment, top bool) diffLine {
	text := firstLine(c.Text)
	switch {
	case c.Outdated:
		text = fmt.Sprintf("outdated, was on %s `%s`: %s", c.Where(), strings.TrimSpace(c.Code[0]), text)
	case top:
		text = fmt.Sprintf("%s, not in this view: %s", c.Where(), text)
	case c.Removed && c.Lines() > 1:
		text = fmt.Sprintf("removed lines %d-%d: %s", c.OldLine, c.OldLine+c.Lines()-1, text)
	case c.Lines() > 1:
		text = fmt.Sprintf("lines %d-%d: %s", c.Line, c.Line+c.Lines()-1, text)
	}
	return diffLine{kind: 'w', comment: c.ID, text: "  " + commentMark + " " + printableLine(text)}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// commentsReadyMsg is the files of the worktree's comments as they are
// now, read for R to send them: the comments are found again in them
// before the message is made.
type commentsReadyMsg struct {
	machine, wt string
	against     string
	// pin is the panel's pin when R was pressed: read since another, the
	// files may be another machine's (see Model.pinGen).
	pin   int
	files []commentFile
}

// commentFile is one file read for commentsReadyMsg.
type commentFile struct {
	path string
	seen linecomments.Seen
}

// sendComments is R: the worktree's comments, each found again in its
// file first, as one message for the agent.
func (m *Model) sendComments() tea.Cmd {
	cs := m.pendingComments()
	if len(cs) == 0 {
		if m.comments != nil {
			m.setToast("no comments to send: r on a line of the diff writes one")
		}
		return nil
	}
	machine, wt, src, c, pin := m.commentMachine(), m.curWorktree(), m.src, m.comparison(), m.pinGen
	type fileReq struct {
		path, original string
		untracked      bool
		removed        bool
	}
	byFile := map[string]*fileReq{}
	var files []*fileReq
	for _, cm := range cs {
		f := byFile[cm.File]
		if f == nil {
			f = &fileReq{path: cm.File, original: m.originalPath(cm.File), untracked: m.untracked(cm.File)}
			byFile[cm.File] = f
			files = append(files, f)
		}
		f.removed = f.removed || cm.Removed
	}
	against := m.againstBase()
	return func() tea.Msg {
		out := commentsReadyMsg{machine: machine, wt: wt, against: against, pin: pin}
		for _, f := range files {
			// A file that cannot be read is not known to be gone: its
			// comments are left as they are (Seen).
			var seen linecomments.Seen
			seen.Src, seen.SrcOK = readOK(src.ReadFile(wt, f.path))
			if f.removed {
				if d, err := src.Diff(c, f.path, f.untracked, f.original); err == nil {
					seen.Old, seen.OldOK = oldSide(buildDiffLines(d)), true
				}
			}
			out.files = append(out.files, commentFile{path: f.path, seen: seen})
		}
		return out
	}
}

// readOK is a file read for Seen: known when it was read, and known to
// be empty when it is not there, deleted; not known when it could not be
// read.
func readOK(s string, err error) (string, bool) {
	if errors.Is(err, fs.ErrNotExist) {
		return "", true
	}
	return s, err == nil
}

// onCommentsReady finds the comments again in the files read, and hands
// the message with them to nerdr, to open for the user to read and send.
func (m *Model) onCommentsReady(msg commentsReadyMsg) tea.Cmd {
	if msg.pin != m.pinGen {
		// The source the panel reads through follows the tab shown, so the
		// files may have been read from another machine's worktree.
		m.setToast("the tab changed while the comments were read: R again")
		return nil
	}
	for _, f := range msg.files {
		m.comments.Follow(msg.machine, msg.wt, f.path, f.seen)
	}
	cs := m.comments.For(msg.machine, msg.wt)
	if len(cs) == 0 {
		return nil
	}
	sent := map[int]string{}
	for _, c := range cs {
		sent[c.ID] = c.Text
	}
	// For the agent of the worktree on the machine it was read from.
	out := AgentMsg{Dir: msg.wt, Machine: msg.machine, OnMachine: true,
		Text: linecomments.Prompt(cs, msg.against), Sent: CommentsSentMsg{Sent: sent}}
	m.placeComments()
	return func() tea.Msg { return out }
}

// onCommentsSent takes the comments sent away.
func (m *Model) onCommentsSent(msg CommentsSentMsg) {
	if err := m.comments.RemoveSent(msg.Sent); err != nil {
		m.setToast("comments sent, but not taken away: " + err.Error())
	}
	m.placeComments()
}

// commentBar is the comment being written, in the status row.
func (m Model) commentBar(W int) string {
	return fit(m.commentInput.View(), W)
}
