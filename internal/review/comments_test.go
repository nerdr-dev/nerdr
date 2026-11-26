package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/linecomments"

	tea "github.com/charmbracelet/bubbletea"
)

func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// rowOf is the diff row whose text contains s, failing the test if none.
func rowOf(t *testing.T, m Model, kind byte, s string) int {
	t.Helper()
	for i, dl := range m.diffLines {
		if dl.kind == kind && strings.Contains(dl.text, s) {
			return i
		}
	}
	t.Fatalf("no %q row with %q in %#v", kind, s, m.diffLines)
	return -1
}

// commentRows are the diff's comment rows, as text.
func commentRows(m Model) []string {
	var out []string
	for _, dl := range m.diffLines {
		if dl.comment != 0 {
			out = append(out, strings.TrimSpace(dl.text))
		}
	}
	return out
}

// writeComment is r on the diff's row, the text typed, and enter.
func writeComment(t *testing.T, m Model, row int, text string) Model {
	t.Helper()
	m.diffCursor = row
	m = m.send(keyRunes("r"))
	if !m.commenting {
		t.Fatalf("r should open the comment's input on row %d (%#v)", row, m.diffLines[row])
	}
	m = m.send(keyRunes(text))
	return m.send(tea.KeyMsg{Type: tea.KeyEnter})
}

func commentRepo(t *testing.T) (Model, string) {
	t.Helper()
	parent := setupJumpRepo(t, map[string]string{
		"a.go": "package a\n\nfunc one() int {\n\treturn 1\n}\n\nfunc two() int {\n\treturn 2\n}\n",
	}, nil)
	repo := filepath.Join(parent, "repo")
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n\nfunc one() int {\n\treturn 10\n}\n\nfunc two() int {\n\treturn 2\n}\n")
	m := pinned(t, repo).SetComments(linecomments.Open(filepath.Join(t.TempDir(), linecomments.FileName)))
	m = openFileDiff(t, m, "a.go")
	return m, repo
}

// TestCommentsGoAboveTheirLinesAndFollowThem covers r writing a comment
// on a line, shown above it and marked in the file list, which stays on
// its code when the agent adds lines above it, and is marked outdated
// when the code is gone.
func TestCommentsGoAboveTheirLinesAndFollowThem(t *testing.T) {
	m, repo := commentRepo(t)
	row := rowOf(t, m, '+', "return 10")
	m = writeComment(t, m, row, "why ten?")

	if got := commentRows(m); len(got) != 1 || got[0] != commentMark+" why ten?" {
		t.Fatalf("comment rows: %q", got)
	}
	at := rowOf(t, m, 'w', "why ten?")
	if m.diffLines[at+1].kind != '+' || !strings.Contains(m.diffLines[at+1].text, "return 10") {
		t.Fatalf("the comment should be above its line: %#v", m.diffLines[at+1])
	}
	if m.diffCursor != at+1 {
		t.Errorf("the cursor should stay on the line commented on: %d, want %d", m.diffCursor, at+1)
	}
	if items := strings.Join(m.treeItems(80), "\n"); !strings.Contains(stripANSI(items), commentMark+"1") {
		t.Errorf("the file list should mark the file with its comment:\n%s", stripANSI(items))
	}
	if h := m.diffHint(); !strings.HasPrefix(h, "R send 1 comment") {
		t.Errorf("the hint should offer to send it: %q", h)
	}

	// The agent adds lines above it: the comment goes with its code.
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n\n// one is one.\n// Really.\nfunc one() int {\n\treturn 10\n}\n\nfunc two() int {\n\treturn 2\n}\n")
	m = m.settle(m.loadDiff())
	cs := m.pendingComments()
	if len(cs) != 1 || cs[0].Line != 6 || cs[0].Outdated {
		t.Fatalf("after the edit: %#v", cs)
	}
	at = rowOf(t, m, 'w', "why ten?")
	if !strings.Contains(m.diffLines[at+1].text, "return 10") {
		t.Fatalf("the comment should still be above its line: %#v", m.diffLines[at+1])
	}

	// The agent rewrites it: outdated, at the top of the diff.
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n\nfunc one() int {\n\treturn 11\n}\n\nfunc two() int {\n\treturn 2\n}\n")
	m = m.settle(m.loadDiff())
	if cs := m.pendingComments(); len(cs) != 1 || !cs[0].Outdated {
		t.Fatalf("after the rewrite: %#v", cs)
	}
	if got := commentRows(m); len(got) != 1 || !strings.Contains(got[0], "outdated, was on a.go:6 `return 10`: why ten?") {
		t.Fatalf("outdated comment rows: %q", got)
	}
}

// TestCommentsAreEditedAndDeletedOnTheirRow covers enter and r changing
// a comment on its row, x deleting one, and an emptied text deleting it.
func TestCommentsAreEditedAndDeletedOnTheirRow(t *testing.T) {
	m, _ := commentRepo(t)
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "first")
	m = writeComment(t, m, rowOf(t, m, ' ', "func two"), "second")
	if got := commentRows(m); len(got) != 2 {
		t.Fatalf("comment rows: %q", got)
	}

	m.diffCursor = rowOf(t, m, 'w', "first")
	if h := m.diffHint(); !strings.HasPrefix(h, "enter edit · x delete") {
		t.Errorf("on a comment's row the hint is its keys: %q", h)
	}
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.commenting || m.commentInput.Value() != "first" {
		t.Fatalf("enter should open the comment to change: %v %q", m.commenting, m.commentInput.Value())
	}
	m = m.send(keyRunes(", changed"))
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	if got := commentRows(m); got[0] != commentMark+" first, changed" {
		t.Fatalf("after editing: %q", got)
	}

	// esc leaves it as it was.
	m.diffCursor = rowOf(t, m, 'w', "second")
	m = m.send(keyRunes("r"))
	m = m.send(keyRunes("zzz"))
	m = m.send(tea.KeyMsg{Type: tea.KeyEsc})
	if m.commenting || commentRows(m)[1] != commentMark+" second" {
		t.Fatalf("esc should change nothing: %q", commentRows(m))
	}

	m.diffCursor = rowOf(t, m, 'w', "second")
	m.focus = focusFiles
	if m = m.send(keyRunes("x")); len(commentRows(m)) != 2 {
		t.Fatal("x in the file list deletes nothing")
	}
	m.focus = focusDiff
	m = m.send(keyRunes("x"))
	if got := commentRows(m); len(got) != 1 || got[0] != commentMark+" first, changed" {
		t.Fatalf("after x: %q", got)
	}

	m.diffCursor = rowOf(t, m, 'w', "first")
	m = m.send(keyRunes("r"))
	m.commentInput.SetValue("")
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	if got := commentRows(m); len(got) != 0 {
		t.Fatalf("an emptied comment should be deleted: %q", got)
	}
}

// TestCommentOnLinesChosen covers a comment on a range chosen with
// shift+↓, quoting all of it, and one on removed lines alone.
func TestCommentOnLinesChosen(t *testing.T) {
	m, _ := commentRepo(t)
	m.diffCursor = rowOf(t, m, ' ', "func one()")
	m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	if !m.diffSel.on {
		t.Fatal("shift+↓ should choose lines")
	}
	m = m.send(keyRunes("r"))
	m = m.send(keyRunes("fold"))
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	cs := m.pendingComments()
	if len(cs) != 1 || cs[0].Removed || cs[0].Line != 3 ||
		strings.Join(cs[0].Code, "|") != "func one() int {|\treturn 10|}" {
		t.Fatalf("a range with a removed line in it is on the new code: %#v", cs)
	}
	if got := commentRows(m); got[0] != commentMark+" lines 3-5: fold" {
		t.Errorf("the range's row: %q", got)
	}

	m = writeComment(t, m, rowOf(t, m, '-', "return 1"), "keep one")
	var removed linecomments.Comment
	for _, c := range m.pendingComments() {
		if c.Removed {
			removed = c
		}
	}
	if removed.Text != "keep one" || removed.Code[0] != "\treturn 1" {
		t.Fatalf("on a removed line: %#v", m.pendingComments())
	}
	at := rowOf(t, m, 'w', "keep one")
	if m.diffLines[at+1].kind != '-' {
		t.Errorf("the comment should be above the removed line: %#v", m.diffLines[at+1])
	}
}

// TestSendCommentsAsOneMessage covers R: all the worktree's comments in
// one message for its agent, kept until nerdr says it was sent.
func TestSendCommentsAsOneMessage(t *testing.T) {
	m, repo := commentRepo(t)
	if cmd := m.sendComments(); cmd != nil {
		t.Fatal("with no comments, R sends nothing")
	}
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "why ten?")
	m = writeComment(t, m, rowOf(t, m, ' ', "func two"), "and two")

	var ready tea.Msg
	for _, msg := range runCmds(m.sendComments()) {
		if _, ok := msg.(commentsReadyMsg); ok {
			ready = msg
		}
	}
	if ready == nil {
		t.Fatal("R should find the comments again first")
	}
	nm, cmd := m.Update(ready)
	m = nm.(Model)
	var am AgentMsg
	for _, msg := range runCmds(cmd) {
		if a, ok := msg.(AgentMsg); ok {
			am = a
		}
	}
	if am.Dir != repo {
		t.Fatalf("the message should be for the worktree's agent: %#v", am)
	}
	for _, want := range []string{"left 2 comments", "1. a.go:4\n```\n\treturn 10\n```\nwhy ten?", "2. a.go:7\n```\nfunc two() int {\n```\nand two"} {
		if !strings.Contains(am.Text, want) {
			t.Errorf("message lacks %q:\n%s", want, am.Text)
		}
	}
	if len(m.pendingComments()) != 2 {
		t.Fatal("the comments are kept until the message is sent")
	}
	if !am.OnMachine || am.Machine != "" {
		t.Errorf("the message should name the worktree's machine: %#v", am)
	}
	sent, ok := am.Sent.(CommentsSentMsg)
	if !ok || len(sent.Sent) != 2 {
		t.Fatalf("Sent: %#v", am.Sent)
	}
	m = m.send(sent)
	if len(m.pendingComments()) != 0 || len(commentRows(m)) != 0 {
		t.Fatalf("sent, they are gone: %#v", m.pendingComments())
	}
}

// TestCommentsAreSharedBetweenPanels covers two panels on one store: a
// comment written in one is drawn in the other at its next update.
func TestCommentsAreSharedBetweenPanels(t *testing.T) {
	m, repo := commentRepo(t)
	other := openFileDiff(t, pinned(t, repo).SetComments(m.comments), "a.go")
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "seen there")
	other = other.send(tickMsg{})
	if got := commentRows(other); len(got) != 1 {
		t.Fatalf("the other panel's rows: %q", got)
	}
}

// twoHunksRepo has a change in two hunks, each removing a line that
// reads the same: "\treturn nil".
func twoHunksRepo(t *testing.T) Model {
	t.Helper()
	body := func(ret string) string {
		return "package a\n\nfunc a() error {\n\t" + ret + "\n}\n\n// 1\n// 2\n// 3\n// 4\n// 5\n// 6\n// 7\n// 8\n\nfunc b() error {\n\t" + ret + "\n}\n"
	}
	parent := setupJumpRepo(t, map[string]string{"a.go": body("return nil")}, nil)
	repo := filepath.Join(parent, "repo")
	writeFile(t, filepath.Join(repo, "a.go"), body("return err"))
	m := pinned(t, repo).SetComments(linecomments.Open(filepath.Join(t.TempDir(), linecomments.FileName)))
	return openFileDiff(t, m, "a.go")
}

// TestCommentOnTheSecondOfTwoLinesAlike covers removed lines that read
// the same: the comment is on the one chosen, by its place in the old
// file, and stays there in the diff after v shows the whole file, which
// has no removed lines.
func TestCommentOnTheSecondOfTwoLinesAlike(t *testing.T) {
	m := twoHunksRepo(t)
	second := -1
	for i, dl := range m.diffLines {
		if dl.kind == '-' && strings.Contains(dl.text, "return nil") {
			second = i
		}
	}
	m = writeComment(t, m, second, "b's")
	cs := m.pendingComments()
	if len(cs) != 1 || !cs[0].Removed || cs[0].OldLine != 17 {
		t.Fatalf("kept as %#v", cs)
	}
	at := rowOf(t, m, 'w', "b's")
	if dl := m.diffLines[at+1]; dl.kind != '-' || dl.oldLine != 17 {
		t.Fatalf("the comment should be above the second: %#v", dl)
	}
	m = m.send(keyRunes("v")) // the whole file
	if cs := m.pendingComments(); cs[0].Outdated {
		t.Fatal("the whole file has no removed lines: that does not make the comment outdated")
	}
	m = m.send(keyRunes("v"))
	m = m.send(keyRunes("v")) // the diff again
	at = rowOf(t, m, 'w', "b's")
	if dl := m.diffLines[at+1]; dl.kind != '-' || dl.oldLine != 17 {
		t.Fatalf("back in the diff, above the second: %#v", dl)
	}
}

// TestCommentStaysWithinOneHunk covers lines chosen across two hunks,
// with lines not shown between them: not one range of the file, so no
// comment.
func TestCommentStaysWithinOneHunk(t *testing.T) {
	m := twoHunksRepo(t)
	m.diffCursor = rowOf(t, m, '+', "return err")
	for range 12 {
		m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	}
	if !m.diffSel.has(len(m.diffLines) - 3) {
		t.Fatalf("the choice should reach the second hunk: %#v", m.diffSel)
	}
	m = m.send(keyRunes("r"))
	if m.commenting || !strings.Contains(m.toast, "within one hunk") {
		t.Fatalf("a range across hunks should not be commented on: %v %q", m.commenting, m.toast)
	}
}

// TestNoCommentWhileTheDiffLoads covers r while another file's diff is
// on its way: the rows shown are still the last file's.
func TestNoCommentWhileTheDiffLoads(t *testing.T) {
	m, _ := commentRepo(t)
	m.diffCursor = rowOf(t, m, '+', "return 10")
	m.diffPending = true
	if m = m.send(keyRunes("r")); m.commenting {
		t.Fatal("r should wait for the diff")
	}
}

// TestCommentsAreKeptByMachine covers two machines with a worktree at the
// same path, as nerdr pins them: each panel has its own machine's.
func TestCommentsAreKeptByMachine(t *testing.T) {
	m, repo := commentRepo(t)
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "here")
	box := pinNow(t, NewEmbedded(Local{}).SetComments(m.comments), []Pin{{Repo: repo, Path: repo, Machine: "box"}})
	if got := box.pendingComments(); len(got) != 0 {
		t.Fatalf("another machine's worktree at the same path: %#v", got)
	}
	if got := m.pendingComments(); len(got) != 1 || got[0].Machine != "" {
		t.Fatalf("this machine's: %#v", got)
	}
}

// TestCommentsReadUnderAnotherPinAreNotTaken covers R while the panel
// moves to another machine's tab with a worktree at the same path: the
// files may have been read from either, so nothing is taken from them,
// and nothing sent.
func TestCommentsReadUnderAnotherPinAreNotTaken(t *testing.T) {
	m, repo := commentRepo(t)
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "here")
	cmd := m.sendComments()
	m = pinNow(t, m, []Pin{{Repo: repo, Path: repo, Machine: "box"}})
	var ready commentsReadyMsg
	for _, msg := range runCmds(cmd) {
		if r, ok := msg.(commentsReadyMsg); ok {
			ready = r
		}
	}
	// What was read says the code is gone: it is not taken.
	ready.files = []commentFile{{path: "a.go", seen: linecomments.Seen{Src: "gone\n", SrcOK: true}}}
	nm, cmd := m.Update(ready)
	m = nm.(Model)
	for _, msg := range runCmds(cmd) {
		if _, ok := msg.(AgentMsg); ok {
			t.Fatal("nothing should be sent from files read under another pin")
		}
	}
	if !strings.Contains(m.toast, "R again") {
		t.Errorf("toast: %q", m.toast)
	}
	if cs := m.comments.For("", repo); len(cs) != 1 || cs[0].Outdated {
		t.Fatalf("this machine's comment is as it was: %#v", cs)
	}
}

// TestSendKeepsCommentsOnAFileNotRead covers R when a file cannot be
// read: its comments go as they were, not as outdated.
func TestSendKeepsCommentsOnAFileNotRead(t *testing.T) {
	m, repo := commentRepo(t)
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "here")
	if err := os.Remove(filepath.Join(repo, "a.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "a.go"), 0o755); err != nil {
		t.Fatal(err) // a folder in its place: there, but not read
	}
	var ready tea.Msg
	for _, msg := range runCmds(m.sendComments()) {
		if _, ok := msg.(commentsReadyMsg); ok {
			ready = msg
		}
	}
	nm, cmd := m.Update(ready)
	m = nm.(Model)
	var am AgentMsg
	for _, msg := range runCmds(cmd) {
		if a, ok := msg.(AgentMsg); ok {
			am = a
		}
	}
	if !am.OnMachine || am.Machine != "" || strings.Contains(am.Text, "outdated") || !strings.Contains(am.Text, "a.go:4") {
		t.Fatalf("sent as: %#v", am)
	}
}

// TestARangeOfRemovedLinesSaysWhereItIs covers a comment on removed lines
// that follow each other: shown with their numbers in the old file.
func TestARangeOfRemovedLinesSaysWhereItIs(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n\nvar x = 1\nvar y = 2\n"}, nil)
	repo := filepath.Join(parent, "repo")
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n")
	m := pinned(t, repo).SetComments(linecomments.Open(filepath.Join(t.TempDir(), linecomments.FileName)))
	m = openFileDiff(t, m, "a.go")
	m.diffCursor = rowOf(t, m, '-', "var x")
	m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = m.send(keyRunes("r"))
	m = m.send(keyRunes("why go"))
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	if got := commentRows(m); len(got) != 1 || got[0] != commentMark+" removed lines 3-4: why go" {
		t.Fatalf("rows: %q", got)
	}
	if cs := m.pendingComments(); len(cs) != 1 || cs[0].Where() != "a.go, removed lines 3-4 of the old file" {
		t.Fatalf("kept as %#v", cs)
	}
}

// sendNow is R and what comes of it: the message for the agent.
func sendNow(t *testing.T, m Model) (Model, AgentMsg) {
	t.Helper()
	var ready tea.Msg
	for _, msg := range runCmds(m.sendComments()) {
		if _, ok := msg.(commentsReadyMsg); ok {
			ready = msg
		}
	}
	nm, cmd := m.Update(ready)
	m = nm.(Model)
	var am AgentMsg
	for _, msg := range runCmds(cmd) {
		if a, ok := msg.(AgentMsg); ok {
			am = a
		}
	}
	return m, am
}

// TestSendMarksTheCodeOfADeletedFileOutdated covers the file deleted
// before R: not there is known, and its comments are outdated.
func TestSendMarksTheCodeOfADeletedFileOutdated(t *testing.T) {
	m, repo := commentRepo(t)
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "here")
	if err := os.Remove(filepath.Join(repo, "a.go")); err != nil {
		t.Fatal(err)
	}
	_, am := sendNow(t, m)
	if !strings.Contains(am.Text, "a.go:4 (outdated") {
		t.Fatalf("sent as: %s", am.Text)
	}
}

// TestALineAsThePaneShowsItIsTheFilesLine covers a line the pane draws
// otherwise than the file has it, an emoji with its variation selector:
// the comment on it is not outdated for that.
func TestALineAsThePaneShowsItIsTheFilesLine(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil)
	repo := filepath.Join(parent, "repo")
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n\nvar love = \"\u2764\ufe0f\"\n")
	m := pinned(t, repo).SetComments(linecomments.Open(filepath.Join(t.TempDir(), linecomments.FileName)))
	m = openFileDiff(t, m, "a.go")
	m = writeComment(t, m, rowOf(t, m, '+', "var love"), "a heart")
	m = m.settle(m.loadDiff())
	if got := commentRows(m); len(got) != 1 || strings.Contains(got[0], "outdated") {
		t.Fatalf("rows: %q", got)
	}
	_, am := sendNow(t, m)
	if strings.Contains(am.Text, "outdated") || !strings.Contains(am.Text, "a.go:3") {
		t.Fatalf("sent as: %s", am.Text)
	}
}

// TestARangeToTheDiffsEndIsOfItsLinesOnly covers lines chosen to the end
// of a real git diff: what follows its last new line is no line of the
// file, and the comment is on the code alone, found as it is.
func TestARangeToTheDiffsEndIsOfItsLinesOnly(t *testing.T) {
	m, _ := commentRepo(t)
	last := len(m.diffLines) - 1
	if dl := m.diffLines[last]; dl.text == "" {
		t.Fatalf("the diff's last row is nothing of the file: %#v", dl)
	}
	m.diffCursor = rowOf(t, m, ' ', "func two")
	m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = m.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = m.send(keyRunes("r"))
	m = m.send(keyRunes("to the end"))
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	cs := m.pendingComments()
	if len(cs) != 1 || strings.Join(cs[0].Code, "|") != "func two() int {" {
		t.Fatalf("kept as %#v", cs)
	}
	m = m.settle(m.loadDiff())
	if cs := m.pendingComments(); cs[0].Outdated {
		t.Fatal("unchanged code is not outdated")
	}
}

// TestAFileNameIsSafeWhereItIsShown covers a file named with a control
// character and a tab: shown made safe, kept and found as git names it.
func TestAFileNameIsSafeWhereItIsShown(t *testing.T) {
	parent := setupJumpRepo(t, nil, nil)
	repo := filepath.Join(parent, "repo")
	name := "a\tb\x1b[2J.go"
	writeFile(t, filepath.Join(repo, name), "package a\n")
	m := pinned(t, repo).SetComments(linecomments.Open(filepath.Join(t.TempDir(), linecomments.FileName)))
	m = openFileDiff(t, m, name)
	m.diffCursor = rowOf(t, m, '+', "package a")
	m = m.send(keyRunes("r"))
	if strings.ContainsRune(m.commentInput.Prompt, 0x1b) || strings.Contains(m.View(), "\x1b[2J") {
		t.Fatalf("the prompt draws the name's escape: %q", m.commentInput.Prompt)
	}
	m = m.send(keyRunes("odd name"))
	m = m.send(tea.KeyMsg{Type: tea.KeyEnter})
	cs := m.pendingComments()
	if len(cs) != 1 || cs[0].File != name {
		t.Fatalf("kept as %#v", cs)
	}
	if strings.Contains(cs[0].Where(), "\x1b") || strings.Contains(linecomments.Prompt(cs, ""), "\x1b") {
		t.Fatalf("shown as %q", cs[0].Where())
	}
	_, am := sendNow(t, m)
	if strings.Contains(am.Text, "outdated") {
		t.Fatalf("the file is found by its own name: %s", am.Text)
	}
}

// TestClickingRSendsTheComments covers the file list's R button.
func TestClickingRSendsTheComments(t *testing.T) {
	m, _ := commentRepo(t)
	m = writeComment(t, m, rowOf(t, m, '+', "return 10"), "click")
	m.focus = focusFiles
	if h := m.filesHints()[0]; !strings.HasPrefix(h, "R send 1 comment") {
		t.Fatalf("the first button: %q", h)
	}
	var ready bool
	for _, msg := range runCmds(m.clickFilesHint(0, 200)) {
		_, ready = msg.(commentsReadyMsg)
	}
	if !ready {
		t.Fatal("clicking R should send the comments")
	}
}
